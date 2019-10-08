package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"sort"
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

const resyncPeriod = 10 * time.Minute

var node string

func main() {
	node = os.Getenv("NODE")
	buf := &bytes.Buffer{}
	err := getMetrics(buf)
	if err != nil {
		log.Fatalf("failed to get metrics from node exporter: %v", err)
	}

	labels, err := getNodeLabels()
	if err != nil {
		log.Fatalf("failed to get node labels: %v", err)
	}

	res := &bytes.Buffer{}
	scanner := bufio.NewScanner(buf)
	for scanner.Scan() {
		line := scanner.Text()

		// Skip comments.
		if !strings.Contains(line, "#") {
			line, err = appendNodeLabels(line, labels)
			if err != nil {
				log.Fatalf("failed to append node labels to metric: %v", err)
			}
		}

		n, err := res.WriteString(line + "\n")
		if err != nil {
			log.Fatalf("write line to buffer failed: %v", err)
		}
		if n != (len(line) + 1) {
			log.Fatalf("expect to write %v bytes into buffer, but actually write %v", len(line), n)
		}
	}

	err = scanner.Err()
	if err != nil {
		log.Fatalf("scan metrics failed")
	}

	fmt.Println(res.String())
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

func getMetrics(w io.Writer) error {
	resp, err := http.Get("http://localhost:9100/metrics")
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

func getNodeLabels() (map[string]string, error) {
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

	o, exists, err := store.GetByKey(node)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("can't find node %v in the informer store", node)
	}

	node, ok := o.(*apiv1.Node)
	if !ok {
		return nil, fmt.Errorf("received object which is not node type")
	}

	return node.Labels, nil
}
