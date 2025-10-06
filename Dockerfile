ARG TARGETPLATFORM

#
# build container
#
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
WORKDIR /go/src/github.com/oliver006/redis_exporter/

ADD . /go/src/github.com/oliver006/redis_exporter/

ARG SHA1="[no-sha]"
ARG TAG="[no-tag]"
ARG TARGETOS
ARG TARGETARCH

#RUN printf "nameserver 1.1.1.1\nnameserver 8.8.8.8"> /etc/resolv.conf \ && apk --no-cache add ca-certificates git

RUN apk --no-cache add ca-certificates git
RUN BUILD_DATE=$(date +%F-%T) CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /redis_exporter \
    -ldflags  "-s -w -extldflags \"-static\" -X main.BuildVersion=$TAG -X main.BuildCommitSha=$SHA1 -X main.BuildDate=$BUILD_DATE" .

RUN [ "$TARGETARCH" = "amd64" ]  && /redis_exporter -version || ls -la /redis_exporter

#
# scratch release container
#
FROM scratch AS scratch-release

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
FROM alpine:3.22 AS alpine

COPY --from=builder /redis_exporter /redis_exporter
COPY --from=builder /etc/ssl/certs /etc/ssl/certs

# Run as non-root user for secure environments
USER 59000:59000

EXPOSE     9121
ENTRYPOINT [ "/redis_exporter" ]
