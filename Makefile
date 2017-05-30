GO              ?= GO15VENDOREXPERIMENT=1 go
GOPATH          := $(firstword $(subst :, ,$(shell $(GO) env GOPATH)))
PROMU           ?= $(GOPATH)/bin/promu
MEGACHECK       ?= $(GOPATH)/bin/megacheck
pkgs            = $(shell $(GO) list ./... | grep -v /vendor/)
TARGET          ?= redis_exporter

PREFIX          ?= $(shell pwd)
BIN_DIR         ?= $(shell pwd)

# Redis variables for test
REDIS_CLI_PATH          ?= $(shell which redis-cli)
REDIS_PORT              ?= 6379

all: get-tools dependencies format vet megacheck build test

get-tools:
	@echo ">> getting glide"
	@$(GO) get -u "github.com/Masterminds/glide"
	@$(GO) install "github.com/Masterminds/glide"

dependencies:
	@echo ">> glide update dependencies"
	@glide --quiet update

test:
	@echo ">> check for a redis-server on port $(REDIS_PORT)"
ifeq ($(shell $(REDIS_CLI_PATH) -p $(REDIS_PORT) ping 2>/dev/null),PONG)
	@echo " > running tests"
	@$(GO) test -short $(pkgs)
else
	@echo " > redis-server not running, skipping test.."
endif

format:
	@echo ">> formatting code"
	@$(GO) fmt $(pkgs)

vet:
	@echo ">> vetting code"
	@$(GO) vet $(pkgs)

megacheck: $(MEGACHECK)
	@echo ">> megacheck code"
	@$(MEGACHECK) $(pkgs)

build: $(PROMU)
	@echo ">> building binaries"
	@CGO_ENABLED=0; $(PROMU) build --prefix $(PREFIX)

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
