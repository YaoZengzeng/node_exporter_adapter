VERSION=v0.1.0

all:
	CGO_ENABLED=0 go build -o node-exporter-adapter --installsuffix cgo main.go
	docker build -t node-exporter-adapter:$(VERSION) .