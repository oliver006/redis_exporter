OM r.fds.so:5000/golang1.5.3

ENV GOPATH /go
COPY . /go/src/redis_exporter

RUN apt-get update && apt-get install -y   git mercurial make \
    && cd /go/src/redis_exporter \
    && go get ./... && go build && cp redis_exporter /bin/redis_exporter \
    && rm -rf /go

EXPOSE     9121
ENTRYPOINT [ "/bin/redis_exporter" ]
