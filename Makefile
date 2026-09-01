.PHONY: build check fmt vet test integration clean help

build: ## build bin/indihurd
	go build -o bin/indihurd ./cmd/indihurd

check: fmt vet test ## fmt, vet and test

fmt: ## fail on any file gofmt would change
	@out="$$(gofmt -l cmd internal)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet: ## go vet ./...
	go vet ./...

test: ## go test ./...
	go test ./...

integration: ## e2e tests; needs INDI simulators built (see build/indi)
	go test -tags integration ./internal/e2e/...

clean: ## remove bin
	rm -rf bin

help: ## list targets
	@grep -E '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "%-12s %s\n", $$1, $$2}'
