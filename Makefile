GO      ?= go
BIN     := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test vet fmt check-fmt lint run demo docker clean ci

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/blackbox ./cmd/blackbox
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/demoagent ./cmd/demoagent

test:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -l -w .

check-fmt:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run; else echo "golangci-lint not installed; falling back to go vet"; $(GO) vet ./...; fi

run:
	$(GO) run ./cmd/blackbox serve

demo:
	$(GO) run ./cmd/demoagent

docker:
	docker build -t blackbox:$(VERSION) .

clean:
	rm -rf $(BIN)

ci: check-fmt vet test build
