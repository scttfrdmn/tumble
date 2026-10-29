# SPDX-License-Identifier: Apache-2.0

# giri's build needs the arenas experiment; export it for every target so it is
# never a surprise (SETUP.md, "Identity").
export GOEXPERIMENT := arenas

BINARY  := tumble
CMD     := ./cmd/tumble
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

KERNELS := $(wildcard kernels/*)

.PHONY: all vet build test lint fuzz-seed tidy clean

all: vet build test ## vet, build, unit tests

vet: ## go vet
	go vet ./...

build: ## Build the tumble binary
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(CMD)

test: ## Unit tests with the race detector
	go test -race ./...

lint: ## go vet and golangci-lint with the repo config
	go vet ./...
	golangci-lint run

fuzz-seed: ## Regenerate kernels/*/testdata/corpus (60 s per kernel)
	@for k in $(KERNELS); do \
		echo "fuzz-seed $$k"; \
		go test -run '^$$' -fuzz '^Fuzz' -fuzztime 60s ./$$k/ || exit 1; \
	done

tidy: ## go mod tidy
	go mod tidy

clean: ## Remove build artifacts
	rm -rf bin coverage.out
