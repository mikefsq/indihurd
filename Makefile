.PHONY: build check fmt vet test integration deps-head tidy clean help

build: ## Build bin/indihurd (default; Linux)
	go build -o bin/indihurd ./cmd/indihurd

check: fmt vet test ## Check formatting, vet, and run unit tests

fmt: ## Check Go formatting without changing files
	@out="$$(gofmt -l cmd internal)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet: ## Run go vet
	go vet ./...

test: ## Run the Go test suite
	go test ./...

integration: ## Run simulator tests (see DRIVERS.md for setup)
	go test -tags integration ./internal/e2e/...

deps-head: ## Update mikefsq dependencies to their latest main commits
	@export GOWORK=off; \
	self="$$(go list -m)" || exit $$?; \
	mods="$$(grep -oE 'github.com/mikefsq/[a-zA-Z0-9./-]+' go.mod | sort -u | grep -vxF "$$self")"; \
	[ -n "$$mods" ] || { echo "deps-head: no github.com/mikefsq dependencies in go.mod"; exit 0; }; \
	echo "$$mods" | sed 's/^/  /'; \
	go get $$(echo "$$mods" | sed 's/$$/@main/' | tr '\n' ' ')

tidy: deps-head ## Update dependencies to main and tidy modules
	GOWORK=off go mod tidy

clean: ## Remove bin/
	rm -rf bin

help: ## Show available targets
	@echo "Usage: make <target>"
	@echo
	@grep -hE '^[a-z-]+:.*## ' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN{FS=":.*## "}{printf "  %-12s %s\n", $$1, $$2}'
