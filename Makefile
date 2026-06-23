.DEFAULT_GOAL := build
HAS_UPX := $(shell command -v upx 2> /dev/null)
VERSION := v2-$(shell git rev-parse --short HEAD)

.PHONY: build
build:
	go build -ldflags="-X main.version=$(VERSION)" -o ./larkdown cmd/*.go
ifneq ($(and $(COMPRESS),$(HAS_UPX)),)
	upx -9 ./larkdown
endif

.PHONY: test
test:
	go test ./...

.PHONY: clean
clean:  ## Clean build bundles
	rm -f ./larkdown

.PHONY: format
format:
	gofmt -l -w .
