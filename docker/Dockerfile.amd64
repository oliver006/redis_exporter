#
# build container
#
FROM golang:1.15-alpine as builder
WORKDIR /go/src/github.com/oliver006/redis_exporter/

ADD go.mod go.sum main.go exporter.go /go/src/github.com/oliver006/redis_exporter/

ARG GOARCH="amd64"
ARG SHA1="[no-sha]"
ARG TAG="[no-tag]"

RUN apk --no-cache add ca-certificates
RUN BUILD_DATE=$(date +%F-%T) && CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build -o /redis_exporter \
    -ldflags  "-s -w -extldflags \"-static\" -X main.BuildVersion=$TAG -X main.BuildCommitSha=$SHA1 -X main.BuildDate=$BUILD_DATE" .

RUN [ $GOARCH = "amd64" ]  && /redis_exporter -version || ls -la /redis_exporter

#
# scratch release container
#
FROM scratch as scratch

COPY --from=builder /redis_exporter /redis_exporter
COPY --from=builder /etc/ssl/certs /etc/ssl/certs

# Run as non-root user for secure environments
USER 59000:59000

EXPOSE     9121
ENTRYPOINT [ "/redis_exporter" ]


#
# Alpine release container
#
FROM alpine as alpine

COPY --from=builder /redis_exporter /redis_exporter
COPY --from=builder /etc/ssl/certs /etc/ssl/certs

EXPOSE     9121
ENTRYPOINT [ "/redis_exporter" ]
