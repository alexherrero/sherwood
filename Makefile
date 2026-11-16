# Targets act on the root module only. v1/ has its own go.mod, so ./... here
# never reaches it.

GO ?= go
GOLANGCI_LINT ?= golangci-lint

.PHONY: all build test lint vet

all: vet lint test build

build:
	$(GO) build ./...

test:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

lint:
	@command -v $(GOLANGCI_LINT) >/dev/null 2>&1 || { \
		echo "golangci-lint not found; install v2: https://golangci-lint.run/welcome/install/" >&2; \
		exit 1; }
	$(GOLANGCI_LINT) run ./...
