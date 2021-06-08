
.PHONY: docker-all
docker-all: docker-env-up docker-test docker-env-down


.PHONY: docker-env-up
docker-env-up:
	docker-compose -f contrib/docker-compose-for-tests.yml up -d


.PHONY: docker-env-down
docker-env-down:
	docker-compose -f contrib/docker-compose-for-tests.yml down


.PHONY: docker-test
docker-test:
	docker-compose -f contrib/docker-compose-for-tests.yml up -d
	docker-compose -f contrib/docker-compose-for-tests.yml run --rm tests bash -c 'make test'



.PHONY: test
test:
	contrib/tls/gen-test-certs.sh
	TEST_REDIS_URI="redis://redis6:6379" \
	TEST_REDIS5_URI="redis://redis5:6383" \
	TEST_REDIS6_URI="redis://redis6:6379" \
	TEST_REDIS_2_8_URI="redis://redis-2-8:6381" \
	TEST_KEYDB01_URI="redis://keydb-01:6401" \
	TEST_KEYDB02_URI="redis://keydb-02:6402" \
	TEST_PWD_REDIS_URI="redis://:redis-password@pwd-redis5:6380" \
	TEST_USER_PWD_REDIS_URI="redis://exporter:exporter-password@pwd-redis6:6390" \
	TEST_REDIS_CLUSTER_MASTER_URI="redis://redis-cluster:7000" \
	TEST_REDIS_CLUSTER_SLAVE_URI="redis://redis-cluster:7005" \
	TEST_TILE38_URI="redis://tile38:9851" \
	TEST_REDIS_SENTINEL_URI="redis://redis-sentinel:26379" \
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
	echo " ! gofmt -d *.go       2>&1 | read " | bash
	echo "checking gofmt - DONE"

.PHONY: mixin
mixin:
	cd contrib/redis-mixin && \
	$(MAKE) all && \
	cd ../../

.PHONY: upload-coverage
upload-coverage:
	go get github.com/mattn/goveralls
	/go/bin/goveralls -coverprofile=coverage.txt -service=drone.io



BUILD_DT:=$(shell date +%F-%T)
GO_LDFLAGS:="-s -w -extldflags \"-static\" -X main.BuildVersion=${DRONE_TAG} -X main.BuildCommitSha=${DRONE_COMMIT_SHA} -X main.BuildDate=$(BUILD_DT)" 

.PHONE: build-binaries
build-binaries:
	go get github.com/oliver006/gox@master

	rm -rf .build | true

	export CGO_ENABLED=0 ; \
	gox -os="linux windows freebsd netbsd openbsd"        -arch="amd64 386" -verbose -rebuild -ldflags $(GO_LDFLAGS) -output ".build/redis_exporter-${DRONE_TAG}.{{.OS}}-{{.Arch}}/{{.Dir}}" && \
	gox -os="darwin solaris illumos"                      -arch="amd64"     -verbose -rebuild -ldflags $(GO_LDFLAGS) -output ".build/redis_exporter-${DRONE_TAG}.{{.OS}}-{{.Arch}}/{{.Dir}}" && \
	gox -os="linux freebsd netbsd"                        -arch="arm"       -verbose -rebuild -ldflags $(GO_LDFLAGS) -output ".build/redis_exporter-${DRONE_TAG}.{{.OS}}-{{.Arch}}/{{.Dir}}" && \
	gox -os="linux" -arch="arm64 mips64 mips64le ppc64 ppc64le s390x"       -verbose -rebuild -ldflags $(GO_LDFLAGS) -output ".build/redis_exporter-${DRONE_TAG}.{{.OS}}-{{.Arch}}/{{.Dir}}" && \
	echo "done"
