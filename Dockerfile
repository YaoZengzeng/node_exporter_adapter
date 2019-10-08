FROM busybox

ADD node-exporter-adapter /bin/node-exporter-adapter

ENTRYPOINT ["/bin/node-exporter-adapter"]
