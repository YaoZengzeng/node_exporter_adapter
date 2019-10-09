package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

const (
	resyncPeriod = 10 * time.Minute
	metricsPath  = "/metrics"
	healthzPath  = "/healthz"
)

var (
	node             string
	host             string
	port             int
	nodeExporterPort int
)

func init() {
	flag.StringVar(&host, "host", "0.0.0.0", "Host to expose metrics on")
	flag.IntVar(&port, "port", 9101, "Port to expose metrics on")
	flag.IntVar(&nodeExporterPort, "node-exporter-port", 9100, "Port which node-exporter to expose metrics on")
}

func main() {
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mux := http.NewServeMux()

	m, err := NewMetricsHandler(ctx)
	if err != nil {
		log.Fatalf("construct new metrics handler failed: %v", err)
	}

	mux.Handle(metricsPath, m)
	mux.HandleFunc(healthzPath, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(http.StatusText(http.StatusOK)))
	})

	listenAddress := net.JoinHostPort(host, strconv.Itoa(port))
	log.Fatal(http.ListenAndServe(listenAddress, mux))
}

type MetricsHandler struct {
	node  string
	store cache.Store
}

func NewMetricsHandler(ctx context.Context) (*MetricsHandler, error) {
	node := os.Getenv("NODE")
	if node == "" {
		return nil, fmt.Errorf("node name should not be empty")
	}

	kcfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	client, err := kubernetes.NewForConfig(kcfg)
	if err != nil {
		return nil, err
	}

	nlw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return client.CoreV1().Nodes().List(options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return client.CoreV1().Nodes().Watch(options)
		},
	}

	informer := cache.NewSharedInformer(nlw, &apiv1.Node{}, resyncPeriod)
	store := informer.GetStore()

	go informer.Run(ctx.Done())

	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return nil, fmt.Errorf("failed to sync cache")
	}

	return &MetricsHandler{
		node:  node,
		store: store,
	}, nil
}

func (m *MetricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	resHeader := w.Header()
	resHeader.Set("Content-Type", `text/plain; version=`+"0.0.4")

	buf := &bytes.Buffer{}
	err := m.GetMetrics(buf)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to get metrics from node exporter: %v", err), http.StatusInternalServerError)
		return
	}

	labels, err := m.NodeLabels()
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to get node labels: %v", err), http.StatusInternalServerError)
		return
	}

	skip := false
	res := &bytes.Buffer{}
	scanner := bufio.NewScanner(buf)
	for scanner.Scan() {
		line := scanner.Text()

		// Skip comments.
		if !strings.Contains(line, "#") {
			if !skip {
				line, err = appendNodeLabels(line, labels)
				if err != nil {
					http.Error(w, fmt.Sprintf("failed to append node labels to metric: %v", err), http.StatusInternalServerError)
					return
				}
			}
		} else {
			if strings.Contains(line, "TYPE") {
				if strings.Contains(line, "summary") || strings.Contains(line, "histogram") {
					// Avoid to add node labels to metrics of type Summary and Histogram.
					skip = true
				} else {
					skip = false
				}
			}
		}

		n, err := res.WriteString(line + "\n")
		if err != nil {
			http.Error(w, fmt.Sprintf("write line to buffer failed: %v", err), http.StatusInternalServerError)
			return
		}
		if n != (len(line) + 1) {
			http.Error(w, fmt.Sprintf("expect to write %v bytes into buffer, but actually write %v", len(line)+1, n), http.StatusInternalServerError)
			return
		}
	}

	err = scanner.Err()
	if err != nil {
		http.Error(w, fmt.Sprintf("scan metrics failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(res.Bytes())
}

func (m *MetricsHandler) NodeLabels() (map[string]string, error) {
	o, exists, err := m.store.GetByKey(m.node)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("can't find node %v in the store", node)
	}

	node, ok := o.(*apiv1.Node)
	if !ok {
		return nil, fmt.Errorf("received object which is not node type")
	}

	return node.Labels, nil
}

func (m *MetricsHandler) GetMetrics(w io.Writer) error {
	resp, err := http.Get(fmt.Sprintf("http://localhost:%s/metrics", strconv.Itoa(nodeExporterPort)))
	if err != nil {
		return err
	}
	defer func() {
		io.Copy(ioutil.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("node exporter returned HTTP status %s", resp.Status)
	}

	io.Copy(w, resp.Body)

	return nil
}

func appendNodeLabels(line string, labels map[string]string) (string, error) {
	// Join the sorted labels.
	s := []string{}
	for k, v := range labels {
		s = append(s, fmt.Sprintf("%s=\"%s\"", k, v))
	}
	sort.Strings(s)
	l := strings.Join(s, ",")

	// Inject the labels into metric.
	var res string
	if strings.Contains(line, "}") {
		index := strings.Index(line, "}")
		res = line[:index] + "," + l + line[index:]
	} else {
		line = strings.TrimSpace(line)
		items := strings.Split(line, " ")
		if len(items) != 2 {
			return "", fmt.Errorf("split the metric line into more than 2 parts")
		}
		items[0] = items[0] + "{" + l + "}"
		res = strings.Join(items, " ")
	}

	return res, nil
}
