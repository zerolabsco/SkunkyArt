# Build, test and package SkunkyArt. VERSION comes from the nearest tag so a
# local build reports the same version a release of that commit would.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')
LDFLAGS  = -s -w -X main.version=$(VERSION)
GOFLAGS  = -trimpath
LINT     = github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2

# os/arch pairs packaged by `make dist`.
PLATFORMS = linux/amd64 linux/arm64 darwin/arm64 freebsd/amd64

.PHONY: build test lint dist clean

build:
	CGO_ENABLED=0 go build $(GOFLAGS) -tags embed -ldflags "$(LDFLAGS)" -o skunkyart .

test:
	go test ./... -race -count=1

lint:
	go run $(LINT) run ./...

# One tar.gz per platform under dist/, each holding the embed-tag binary,
# the example config and the docs, with a checksum file beside them.
dist:
	rm -rf dist && mkdir -p dist
	set -e; for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; dir=dist/skunkyart-$(VERSION)-$$os-$$arch; \
	  mkdir -p $$dir; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build $(GOFLAGS) -tags embed -ldflags "$(LDFLAGS)" -o $$dir/skunkyart .; \
	  cp config.example.json README.org SETUP.md API.md LICENSE $$dir/; \
	  cp -r services $$dir/; \
	  tar -C dist -czf $$dir.tar.gz $$(basename $$dir); \
	  rm -r $$dir; \
	done
	cd dist && shasum -a 256 *.tar.gz > SHA256SUMS

clean:
	rm -rf dist skunkyart
