ARG GOARCH
#
# build container
#
FROM --platform=linux/amd64 golang:1.18-alpine as builder
WORKDIR /go/src/github.com/oliver006/redis_exporter/

ADD . /go/src/github.com/oliver006/redis_exporter/

ARG SHA1="[no-sha]"
ARG TAG="[no-tag]"
ARG GOARCH

RUN apk --no-cache add ca-certificates git
RUN BUILD_DATE=$(date +%F-%T) CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build -o /redis_exporter \
    -ldflags  "-s -w -extldflags \"-static\" -X main.BuildVersion=$TAG -X main.BuildCommitSha=$SHA1 -X main.BuildDate=$BUILD_DATE" .

RUN [ "$GOARCH" = "amd64" ]  && /redis_exporter -version || ls -la /redis_exporter

#
# scratch release container
#
FROM --platform=linux/$GOARCH scratch as scratch

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
FROM --platform=linux/$GOARCH alpine as alpine

COPY --from=builder /redis_exporter /redis_exporter
COPY --from=builder /etc/ssl/certs /etc/ssl/certs

# Run as non-root user for secure environments
USER 59000:59000

EXPOSE     9121
ENTRYPOINT [ "/redis_exporter" ]
