.PHONY: build test fmt vet clean install-dev

GO ?= go
BIN_DIR := bin
BIN := $(BIN_DIR)/hera

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN) ./cmd/hera

test:
	$(GO) test ./... -race -count=1

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

clean:
	rm -rf $(BIN_DIR)

install-dev: build
	cp $(BIN) $$HOME/bin/hera
