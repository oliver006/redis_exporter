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
ARG DATE
ENV DATE=$DATE

RUN apk --no-cache add ca-certificates
RUN CGO_ENABLED=0 GOOS=linux go build -o /redis_exporter \
    -ldflags  "-s -w -extldflags \"-static\" -X main.VERSION=$TAG -X main.COMMIT_SHA1=$SHA1 -X main.BUILD_DATE=$DATE" .


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

EXPOSE     9121
ENTRYPOINT [ "/redis_exporter" ]
