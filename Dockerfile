FROM golang:1.10.1-alpine AS build

COPY . $GOPATH/src/github.com/oliver006/redis_exporter

RUN apk --update add git
RUN cd $GOPATH/src/github.com/oliver006/redis_exporter \
  && go build && cp redis_exporter /bin/redis_exporter

FROM gliderlabs/alpine
MAINTAINER Oliver <oliver@21zoo.com>

RUN apk add --no-cache ca-certificates

COPY --from=build /bin/redis_exporter /bin/redis_exporter

EXPOSE     9121
ENTRYPOINT [ "/bin/redis_exporter" ]
