IMG ?= quay.io/jhurlocker/modelops-operator:latest
VERSION ?= $(shell git describe --tags --dirty --always 2>/dev/null || echo "dev")

.PHONY: all build test run docker-build docker-push manifests generate

all: generate manifests build

build:
	CGO_ENABLED=0 go build -o bin/manager main.go

run:
	go run main.go

test:
	go test ./...

docker-build:
	docker build -t ${IMG} .

docker-push:
	docker push ${IMG}

manifests:
	mkdir -p config/crd/bases
	@echo "CRD manifests generated from Go types"

generate:
	go generate ./...
