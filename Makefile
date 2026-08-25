.PHONY: build test lint clean install update

BINARY := remail
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/dreikanter/remail/internal/cli.Version=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/remail

test:
	go test -coverprofile=coverage.out ./...

lint:
	go fix -diff ./...
	go tool golangci-lint run

clean:
	rm -f $(BINARY) coverage.out

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/remail

update:
	git checkout main
	git pull --tags
	$(MAKE) install
	@echo "Installed: $$(remail --version)"
