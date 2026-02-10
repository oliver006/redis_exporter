.DEFAULT_GOAL := build

DOCKER_COMPOSE := $(if $(shell which docker-compose),docker-compose,docker compose)

.PHONY: build
build:
	go build .


.PHONY: docker-all
docker-all: docker-env-up docker-test docker-env-down


.PHONY: docker-env-up
docker-env-up:
	$(DOCKER_COMPOSE) -f docker-compose.yml up -d


.PHONY: docker-env-down
docker-env-down:
	$(DOCKER_COMPOSE) -f docker-compose.yml down


.PHONY: docker-test
docker-test:
	$(DOCKER_COMPOSE) -f docker-compose.yml up -d
	$(DOCKER_COMPOSE) -f docker-compose.yml run --rm tests bash -c 'make test'


.PHONY: test-certs
test-certs:
	contrib/tls/gen-test-certs.sh


.PHONY: test
test:
	TEST_REDIS_URI="redis://localhost:6379" \
	TEST_VALKEY7_URI="valkey://localhost:16384" \
	TEST_VALKEY8_URI="valkey://localhost:16382" \
	TEST_VALKEY8_BUNDLE_URI="valkey://localhost:16389" \
	TEST_VALKEY8_TLS_URI="valkeys://localhost:16386" \
	TEST_REDIS7_TLS_URI="rediss://localhost:16387" \
	TEST_REDIS8_URI="redis://localhost:16388" \
	TEST_REDIS7_URI="redis://localhost:16385" \
	TEST_REDIS5_URI="redis://localhost:16383" \
	TEST_REDIS6_URI="redis://localhost:16379" \
	TEST_REDIS_2_8_URI="redis://localhost:16381" \
	TEST_KEYDB01_URI="redis://localhost:16401" \
	TEST_KEYDB02_URI="redis://localhost:16402" \
	TEST_PWD_REDIS_URI="redis://:redis-password@localhost:16380" \
	TEST_USER_PWD_REDIS_URI="redis://exporter:exporter-password@localhost:16390" \
	TEST_REDIS_CLUSTER_MASTER_URI="redis://localhost:17000" \
	TEST_REDIS_CLUSTER_SLAVE_URI="redis://localhost:17005" \
	TEST_VALKEY_CLUSTER_PASSWORD_URI="redis://localhost:17006" \
	TEST_TILE38_URI="redis://localhost:19851" \
	TEST_VALKEY_SENTINEL_URI="redis://localhost:26379" \
	go test -v -covermode=atomic -cover -race -coverprofile=coverage.txt -p 1 ./...

.PHONY: lint
lint:
	#
	# this will run the default linters on non-test files
	# and then all but the "errcheck" linters on the tests
	golangci-lint run --tests=false --exclude-use-default
	golangci-lint run -D=errcheck   --exclude-use-default

.PHONY: checks
checks:
	go vet ./...
	echo "checking gofmt"
	@if [ "$(shell gofmt -e -l . | wc -l)" -ne 0 ]; then exit 1; fi
	echo "checking gofmt - DONE"

.PHONY: mixin
mixin:
	cd contrib/redis-mixin && \
	$(MAKE) all && \
	cd ../../


BUILD_DT:=$(shell date +%F-%T)
GO_LDFLAGS:="-s -w -extldflags \"-static\" -X main.BuildVersion=${GITHUB_REF_NAME} -X main.BuildCommitSha=${GITHUB_SHA} -X main.BuildDate=$(BUILD_DT)"


.PHONY: build-some-amd64-binaries
build-some-amd64-binaries:
	go install github.com/oliver006/gox@master

	rm -rf .build | true

	export CGO_ENABLED=0 ; \
	gox -os="linux windows" -arch="amd64" -verbose -rebuild -ldflags $(GO_LDFLAGS) -output ".build/redis_exporter-${GITHUB_REF_NAME}.{{.OS}}-{{.Arch}}/{{.Dir}}" && echo "done"


.PHONY: build-all-binaries
build-all-binaries:
	go install github.com/oliver006/gox@master

	rm -rf .build | true

	export CGO_ENABLED=0 ; \
	gox -os="linux windows freebsd netbsd openbsd"        -arch="amd64 386" -verbose -rebuild -ldflags $(GO_LDFLAGS) -output ".build/redis_exporter-${GITHUB_REF_NAME}.{{.OS}}-{{.Arch}}/{{.Dir}}" && \
	gox -os="darwin solaris illumos"                      -arch="amd64"     -verbose -rebuild -ldflags $(GO_LDFLAGS) -output ".build/redis_exporter-${GITHUB_REF_NAME}.{{.OS}}-{{.Arch}}/{{.Dir}}" && \
	gox -os="darwin"                                      -arch="arm64"     -verbose -rebuild -ldflags $(GO_LDFLAGS) -output ".build/redis_exporter-${GITHUB_REF_NAME}.{{.OS}}-{{.Arch}}/{{.Dir}}" && \
	gox -os="linux freebsd netbsd"                        -arch="arm"       -verbose -rebuild -ldflags $(GO_LDFLAGS) -output ".build/redis_exporter-${GITHUB_REF_NAME}.{{.OS}}-{{.Arch}}/{{.Dir}}" && \
	gox -os="linux" -arch="arm64 mips64 mips64le ppc64 ppc64le s390x"       -verbose -rebuild -ldflags $(GO_LDFLAGS) -output ".build/redis_exporter-${GITHUB_REF_NAME}.{{.OS}}-{{.Arch}}/{{.Dir}}" && \
	echo "done"
