VERSION := $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: test build release demo

test:
	go vet ./... && go test -race ./...

build:
	go build -ldflags "$(LDFLAGS)" -o bin/sweep .

release:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/sweep-darwin-arm64 .

# The README recording (demo/sweep.gif). Needs vhs: brew install vhs
demo:
	go build -o demo/bin/sweep .
	demo/setup.sh
	vhs demo/sweep.tape
	rm -rf demo/bin demo/projects demo/home
