GO              ?= GO15VENDOREXPERIMENT=1 go
GOPATH          := $(firstword $(subst :, ,$(shell $(GO) env GOPATH)))
PROMU           ?= $(GOPATH)/bin/promu
MEGACHECK       ?= $(GOPATH)/bin/megacheck
pkgs            = $(shell $(GO) list ./... | grep -v /vendor/)
TARGET          ?= redis_exporter

PREFIX          ?= $(shell pwd)
BIN_DIR         ?= $(shell pwd)

all: get-tools dependencies format vet megacheck build

get-tools:
	@echo ">> getting glide"
	@$(GO) get -u "github.com/Masterminds/glide"
	@$(GO) install "github.com/Masterminds/glide"

dependencies:
	@echo ">> glide update dependencies"
	@glide --quiet update

test:
	@echo ">> running tests"
	@$(GO) test -short $(pkgs)

format:
	@echo ">> formatting code"
	@$(GO) fmt $(pkgs)

vet:
	@echo ">> vetting code"
	@$(GO) vet $(pkgs)

megacheck:
	@echo ">> megacheck code"
	@$(MEGACHECK) $(pkgs)

build: $(PROMU)
	@echo ">> building binaries"
	@$(PROMU) build --prefix $(PREFIX)

clean:
	@echo ">> Cleaning up"
	@$(RM) $(TARGET)

$(GOPATH)/bin/promu promu:
	@GOOS=$(shell uname -s | tr A-Z a-z) \
		GOARCH=$(subst x86_64,amd64,$(patsubst i%86,386,$(shell uname -m))) \
		$(GO) get -u github.com/prometheus/promu

$(GOPATH)/bin/megacheck mega:
	@GOOS=$(shell uname -s | tr A-Z a-z) \
		GOARCH=$(subst x86_64,amd64,$(patsubst i%86,386,$(shell uname -m))) \
		$(GO) get -u honnef.co/go/tools/cmd/megacheck

.PHONY: all format vet build test promu clean $(GOPATH)/bin/promu $(GOPATH)/bin/megacheck
