# node_exporter_adapter
The prometheus Node Exporter expose machine level metrics.

However sometimes we need to associate the metrics that Node Exporter exposed with the Kubernetes Node Object. For example, I want to define the following alert rules:

The alert will be triggered if available memory of the nodes with label `{k1="v1"}` is less than 1G.

But the label is associated with the Kubernetes Node Object, the metrics exposed by Node Exporter will not include them. Without the labels in metrics, it will be easy to define expression in alert rules.

So the goal of this project is dynamicaly inject the labels of Kubernetes Node into the metrics exposed by Node Exporter.

The implementation method is as follows:

1. Node Exporter Adapter will be deployed as the sidecar of Node Exporter.

2. Prometheus will scrape from Node Exporter instead of directly from Node Expoerter.

3. Node Exporter Adapter will try to query the Kube-API-Server periodically to get the Kubernetes Node Object it is located on.

4. When being scraped, Node Exporter Adapter will scrape metrics from Node Exporter and inject the labels of Kubernetes Node Object into the metrcis, then expose the modified metrics to prometheus.

Finally, if the node has label `{k1="v1", k2="v2"}`, the metrics that Node Exporter exposed is:

`node_memory_MemAvailable_bytes 1.0042949632e+10`

But the metrics prometheus scraped from Node Exporter Adapter will be

`node_memory_MemAvailable_bytes{k1="v1", k2="v2"} 1.0042949632e+10`

If the labels relevant with the Node Object changed, Node Exporter Adapter will dynamically change the labels it injected into the metrics.

The alert rule above can be simply defined as:

```
expr: node_memory_MemAvailable_bytes{k1="v1"} < 1024*1024*1024
```

instead of finding all nodes with label `{k1="v1"}` and define a rule for each of them.
