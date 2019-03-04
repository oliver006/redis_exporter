#
# build container
#
FROM golang:1-alpine as builder
WORKDIR /go/src/github.com/oliver006/redis_exporter/

ADD main.go /go/src/github.com/oliver006/redis_exporter/
ADD exporter /go/src/github.com/oliver006/redis_exporter/exporter
ADD vendor /go/src/github.com/oliver006/redis_exporter/vendor

ARG SHA1
ENV SHA1=$SHA1
ARG TAG
ENV TAG=$TAG

RUN apk --no-cache add ca-certificates
RUN BUILD_DATE=$(date +%F-%T) && CGO_ENABLED=0 GOOS=linux go build -o /redis_exporter \
    -ldflags  "-s -w -extldflags \"-static\" -X main.VERSION=$TAG -X main.COMMIT_SHA1=$SHA1 -X main.BUILD_DATE=$BUILD_DATE" .

RUN /redis_exporter -version

#
# Alpine release container
#
FROM alpine as alpine

COPY --from=builder /redis_exporter /redis_exporter
COPY --from=builder /etc/ssl/certs /etc/ssl/certs

EXPOSE     9121
ENTRYPOINT [ "/redis_exporter" ]


#
# release container
#
FROM scratch as scratch

COPY --from=builder /redis_exporter /redis_exporter
COPY --from=builder /etc/ssl/certs /etc/ssl/certs

# Run as non-root user for secure environments
USER 59000:59000

EXPOSE     9121
ENTRYPOINT [ "/redis_exporter" ]
