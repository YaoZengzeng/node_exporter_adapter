package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"sort"
	"strings"
)

func main() {
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
	return map[string]string{
		"k1": "v1",
		"k2": "v2",
	}, nil
}
