.PHONY: build check fmt vet test integration deps-head tidy clean help install indi-drivers install-indi-drivers

build: ## Build bin/indihurd (default; Linux)
	go build -o bin/indihurd ./cmd/indihurd

indi-drivers: ## Fetch and build INDI core and bundled drivers locally (no sudo)
	./build/indi-drivers

install-indi-drivers: ## Install built INDI core drivers into /usr/local (sudo)
	@test "$$(id -u)" -eq 0 || { echo "Run: sudo make install-indi-drivers" >&2; exit 1; }
	@test -f "$(if $(INDI_BUILD_DIR),$(INDI_BUILD_DIR),build)/core/cmake_install.cmake" || { echo "Build first: make indi-drivers" >&2; exit 1; }
	cmake --install "$(if $(INDI_BUILD_DIR),$(INDI_BUILD_DIR),build)/core" --prefix /usr/local
	python3 deploy/install-indi-aliases.py "$(if $(INDI_BUILD_DIR),$(INDI_BUILD_DIR),build)"
	ldconfig

install: ## Install built binary, default config, and systemd unit (sudo; preserves config)
	./deploy/install.sh bin/indihurd

check: fmt vet test ## Check formatting, vet, and run unit tests

fmt: ## Check Go formatting without changing files
	@out="$$(gofmt -l cmd internal)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet: ## Run go vet
	go vet ./...

test: ## Run the Go and driver-build orchestration tests
	go test ./...
	python3 build/indi-drivers_test.py
	python3 build/indi-thirdparty_test.py

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

.PHONY: indi-thirdparty install-indi-thirdparty
indi-thirdparty: ## Build Astroasis Oasis, ZWO ASI, Player One and QHY families locally
	./build/indi-thirdparty

install-indi-thirdparty: ## Install selected third-party drivers, SDKs and USB rules (sudo)
	./build/indi-thirdparty --install

.PHONY: deb
deb: ## Build static Debian packages for amd64 and arm64 in dist/
	./build/build-deb $(DEB_ARGS)
