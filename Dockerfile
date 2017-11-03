FROM golang:1.8.3 as builder

ARG VERSION=0.0.1
WORKDIR /go/src/github.com/oliver006/redis_exporter

COPY . .

RUN go get -d -v .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo  -ldflags "-X main.VERSION=${VERSION} -X main.BUILD_DATE=$(date +%F-%T) -X main.COMMIT_SHA1=`git rev-parse HEAD`" -o redis_exporter .


FROM bash:latest
MAINTAINER Oliver <oliver@21zoo.com>

COPY --from=builder /go/src/github.com/oliver006/redis_exporter/redis_exporter /bin/redis_exporter
COPY entrypoint.sh /entrypoint.sh

RUN chmod +x /entrypoint.sh

ENTRYPOINT [ "/entrypoint.sh" ]
