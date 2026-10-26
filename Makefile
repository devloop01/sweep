VERSION := $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: test build release

test:
	go vet ./... && go test -race ./...

build:
	go build -ldflags "$(LDFLAGS)" -o bin/sweep .

release:
	for arch in arm64 amd64; do \
		CGO_ENABLED=0 GOOS=darwin GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o dist/sweep-darwin-$$arch . || exit 1; \
	done
