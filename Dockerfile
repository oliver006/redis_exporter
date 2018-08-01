#
# build container
#
FROM golang:1.10
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

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags  " -X main.VERSION=$TAG -X main.COMMIT_SHA1=$SHA1 -X main.BUILD_DATE=$DATE " -a -installsuffix cgo -o redis_exporter .




#
# release container
#
FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /bin/
COPY --from=0 /go/src/github.com/oliver006/redis_exporter/ .

EXPOSE     9121
ENTRYPOINT [ "/bin/redis_exporter" ]
