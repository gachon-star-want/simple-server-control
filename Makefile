BINARY := ssc
MODULE := github.com/gachon-star-want/simple-server-control
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo local)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.Date=$(DATE)

.PHONY: all build-ui build test doctor release-binaries clean

all: build

build-ui:
	cd web && npm ci && npm run build

build:
	cd web && npm run build
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY) ./cmd/ssc

test:
	cd web && npm run build
	go test -race -v ./...

doctor:
	go run ./cmd/ssc doctor

release-binaries:
	cd web && npm run build
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/simple-server-control-linux-amd64 ./cmd/ssc
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/simple-server-control-linux-arm64 ./cmd/ssc
	cd dist && sha256sum simple-server-control-linux-amd64 simple-server-control-linux-arm64 > checksums.txt 2>/dev/null || shasum -a 256 simple-server-control-linux-amd64 simple-server-control-linux-arm64 > checksums.txt

clean:
	rm -rf dist web/dist
