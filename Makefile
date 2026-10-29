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

# The fuzzer writes the inputs it finds to $GOCACHE/fuzz, not the repository;
# fuzz-seed starts each kernel's cache entry empty, fuzzes for 60 s, and copies
# what was found into testdata/corpus/, which is committed.
fuzz-seed: ## Regenerate kernels/*/testdata/corpus (60 s per kernel)
	@for k in $(KERNELS); do \
		[ -f $$k/go.mod ] || [ -n "$$(ls $$k/*_test.go 2>/dev/null)" ] || continue; \
		pkg=$$(go list ./$$k/) || exit 1; \
		for fz in $$(grep -ho '^func Fuzz[A-Za-z0-9_]*' $$k/*_test.go | cut -c6-); do \
			echo "fuzz-seed $$k $$fz"; \
			cache="$$(go env GOCACHE)/fuzz/$$pkg/$$fz"; \
			rm -rf "$$cache"; \
			go test -run '^$$' -fuzz "^$$fz\$$" -fuzztime 60s ./$$k/ || exit 1; \
			rm -rf $$k/testdata/corpus/$$fz; \
			mkdir -p $$k/testdata/corpus/$$fz; \
			cp "$$cache"/* $$k/testdata/corpus/$$fz/; \
		done; \
	done

tidy: ## go mod tidy
	go mod tidy

clean: ## Remove build artifacts
	rm -rf bin coverage.out
