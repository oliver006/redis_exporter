FROM gliderlabs/alpine
MAINTAINER Oliver <oliver@21zoo.com>

RUN apk add --no-cache ca-certificates

COPY dist/redis_exporter /bin/redis_exporter

EXPOSE     9121
ENTRYPOINT [ "/bin/redis_exporter" ]
