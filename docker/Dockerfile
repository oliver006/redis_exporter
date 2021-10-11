#
# build container
#
FROM golang:1.17-alpine as builder
WORKDIR /go/src/github.com/oliver006/redis_exporter/

ADD . /go/src/github.com/oliver006/redis_exporter/

ARG TARGETPLATFORM
ARG SHA1="[no-sha]"
ARG TAG="[no-tag]"

RUN apk --no-cache add ca-certificates
RUN GOOS=$(echo $TARGETPLATFORM | cut -d/ -f 1) && \
    GOARCH=$(echo $TARGETPLATFORM | cut -d/ -f 2) && \
    BUILD_DATE=$(date +%F-%T) && CGO_ENABLED=0 GOOS=$GOOS GOARCH=$GOARCH go build -o /redis_exporter \
    -ldflags  "-s -w -extldflags \"-static\" -X main.BuildVersion=$TAG -X main.BuildCommitSha=$SHA1 -X main.BuildDate=$BUILD_DATE" .

RUN [ $TARGETPLATFORM = "linux/amd64" ]  && /redis_exporter -version || ls -la /redis_exporter

#
# scratch release container
#
FROM --platform=$TARGETPLATFORM scratch as scratch

COPY --from=builder /redis_exporter /redis_exporter
COPY --from=builder /etc/ssl/certs /etc/ssl/certs
COPY --from=builder /etc/nsswitch.conf /etc/nsswitch.conf

# Run as non-root user for secure environments
USER 59000:59000

EXPOSE     9121
ENTRYPOINT [ "/redis_exporter" ]


#
# Alpine release container
#
FROM --platform=$TARGETPLATFORM alpine as alpine

COPY --from=builder /redis_exporter /redis_exporter
COPY --from=builder /etc/ssl/certs /etc/ssl/certs

# Run as non-root user for secure environments
USER 59000:59000

EXPOSE     9121
ENTRYPOINT [ "/redis_exporter" ]