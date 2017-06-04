FROM gliderlabs/alpine
MAINTAINER Oliver <oliver@21zoo.com>

COPY redis_exporter /bin/redis_exporter

EXPOSE     9121
ENTRYPOINT [ "/bin/redis_exporter" ]
