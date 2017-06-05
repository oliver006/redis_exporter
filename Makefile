GO              ?= GO15VENDOREXPERIMENT=1 go
GOPATH          := $(firstword $(subst :, ,$(shell $(GO) env GOPATH)))
PROMU           ?= $(GOPATH)/bin/promu
MEGACHECK       ?= $(GOPATH)/bin/megacheck
pkgs            = $(shell $(GO) list ./... | grep -v /vendor/)
TARGET          ?= redis_exporter

PREFIX          ?= $(shell pwd)
BIN_DIR         ?= $(shell pwd)
DOCKER_IMAGE_NAME       ?= oliver006/redis_exporter
DOCKER_IMAGE_TAG        ?= $(subst /,-,$(shell git rev-parse --abbrev-ref HEAD))

# Redis variables for test
REDIS_CLI_PATH          ?= $(shell which redis-cli)
REDIS_PORT              ?= 6379

all: format vet megacheck build test

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
	@CGO_ENABLED=0 $(PROMU) build --prefix $(PREFIX)

tarball: $(PROMU)
	@echo ">> building release tarball"
	@$(PROMU) tarball --prefix $(PREFIX) $(BIN_DIR)

docker:
	@echo ">> building docker image"
	@docker build -t "$(DOCKER_IMAGE_NAME):$(DOCKER_IMAGE_TAG)" .

test-docker:
	@echo ">> testing docker image"
	@./test_image.sh "$(DOCKER_IMAGE_NAME):$(DOCKER_IMAGE_TAG)" 9121

clean:
	@echo ">> Cleaning up"
	@$(RM) $(TARGET) *.tar.gz

$(GOPATH)/bin/promu promu:
	@GOOS=$(shell uname -s | tr A-Z a-z) \
		GOARCH=$(subst x86_64,amd64,$(patsubst i%86,386,$(shell uname -m))) \
		$(GO) get -u github.com/prometheus/promu

$(GOPATH)/bin/megacheck mega:
	@GOOS=$(shell uname -s | tr A-Z a-z) \
		GOARCH=$(subst x86_64,amd64,$(patsubst i%86,386,$(shell uname -m))) \
		$(GO) get -u honnef.co/go/tools/cmd/megacheck

.PHONY: all format vet build test promu clean $(GOPATH)/bin/promu $(GOPATH)/bin/megacheck tarball docker test-docker
