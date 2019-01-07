# Redis Metrics Exporter

[![Circle CI](https://circleci.com/gh/oliver006/redis_exporter.svg?style=shield)](https://circleci.com/gh/oliver006/redis_exporter) [![Coverage Status](https://coveralls.io/repos/github/oliver006/redis_exporter/badge.svg?branch=master)](https://coveralls.io/github/oliver006/redis_exporter?branch=master) [![codecov](https://codecov.io/gh/oliver006/redis_exporter/branch/master/graph/badge.svg)](https://codecov.io/gh/oliver006/redis_exporter)

Prometheus exporter for Redis metrics.\
Supports Redis 2.x, 3.x, 4.x, and 5.x

## Building, configuring, and running

Locally build and run it:

```sh
    $ go get github.com/oliver006/redis_exporter
    $ cd $GOPATH/src/github.com/oliver006/redis_exporter
    $ go build
    $ ./redis_exporter <flags>
```

You can also run it via docker:

```sh
    $ docker pull oliver006/redis_exporter
    $ docker run -d --name redis_exporter -p 9121:9121 oliver006/redis_exporter
```

Add a block to the `scrape_configs` of your prometheus.yml config file:

```yaml
scrape_configs:

...

- job_name: redis_exporter
  static_configs:
  - targets: ['localhost:9121']

...
```

and adjust the host name accordingly.

To run on Cloud Foundry, use:

```sh
cf push -f contrib/manifest.yml
```

### Run on Kubernetes

[Here](contrib/k8s-redis-and-exporter-deployment.yaml) is an example Kubernetes deployment configuration for how to deploy the redis_exporter as a sidecar with a Redis instance.

### Run on Openshift

In order to deploy the exporter on Openshift environment.

```sh
oc project <myproject>

oc process -f https://raw.githubusercontent.com/ivanovaleksandar/redis_exporter/master/contrib/openshift-template.yaml \
    -p REDIS_ADDR=<redis-service>:<redis-port> \
    -p REDIS_PASSWORD=<redis-pass> \
    -p REDIS_ALIAS=<redis-alias> \
    -p REDIS_FILE=<redis-file> \
    | oc create -f -
```

*NOTE*: Some of the parameters can be ommited if no authentication is used or the default redis config is applied.

```sh
oc process -f https://raw.githubusercontent.com/ivanovaleksandar/redis_exporter/contrib/openshift-template.yaml \
    -p REDIS_ADDR=<redis-service>:<redis-port> \
    | oc create -f -
```

If you are running Prometheus on Openshift on the same cluster, **target** in `prometheus.yml` should point to the correct service name of the exporter

```yaml
scrape_configs:

...

- job_name: redis_exporter
  static_configs:
  - targets: ['<redis-exporter.myproject.svc>:9121']

...
```

### Flags

Name               | Description
-------------------|------------
debug              | Verbose debug output
log-format         | Log format, valid options are `txt` (default) and `json`.
check-keys         | Comma separated list of key patterns to export value and length/size, eg: `db3=user_count` will export key `user_count` from db `3`. db defaults to `0` if omitted. The key patterns specified with this flag will be found using [SCAN](https://redis.io/commands/scan).  Use this option if you need glob pattern matching; `check-single-keys` is faster for non-pattern keys.
check-single-keys  | Comma separated list of keys to export value and length/size, eg: `db3=user_count` will export key `user_count` from db `3`. db defaults to `0` if omitted.  The keys specified with this flag will be looked up directly without any glob pattern matching.  Use this option if you don't need glob pattern matching;  it is faster than `check-keys`.
script             | Path to Redis Lua script for gathering extra metrics.
redis.addr         | Address of one or more redis nodes, comma separated, defaults to `redis://localhost:6379`.
redis.password     | Password to use when authenticating to Redis
redis.alias        | Alias for redis node addr, comma separated.
redis.file         | Path to file containing one or more redis nodes, separated by newline. This option is mutually exclusive with redis.addr. Each line can optionally be comma-separated with the fields `<addr>,<password>,<alias>`.
namespace          | Namespace for the metrics, defaults to `redis`.
web.listen-address | Address to listen on for web interface and telemetry, defaults to `0.0.0.0:9121`.
web.telemetry-path | Path under which to expose metrics, defaults to `metrics`.
use-cf-bindings    | Enable usage of Cloud Foundry service bindings. Defaults to `false`
separator          | Separator used to split redis.addr, redis.password and redis.alias into several elements. Defaults to `,`

Redis node addresses can be tcp addresses like `redis://localhost:6379`, `redis.example.com:6379` or unix socket addresses like `unix:///tmp/redis.sock`.\
SSL is supported by using the `rediss://` schema, for example: `rediss://azure-ssl-enabled-host.redis.cache.windows.net:6380` (note that the port is required when connecting to a non-standard 6379 port, e.g. with Azure Redis instances).

These settings take precedence over any configurations provided by [environment variables](#environment-variables).

### Environment Variables

Name               | Description
-------------------|------------
REDIS_ADDR         | Address of Redis node(s)
REDIS_PASSWORD     | Password to use when authenticating to Redis
REDIS_ALIAS        | Alias name of Redis node(s)
REDIS_FILE         | Path to file containing Redis node(s)

### What's exported?

Most items from the INFO command are exported,
see https://redis.io/commands/info for details.\
In addition, for every database there are metrics for total keys, expiring keys and the average TTL for keys in the database.\
You can also export values of keys if they're in numeric format by using the `-check-keys` flag. The exporter will also export the size (or, depending on the data type, the length) of the key. This can be used to export the number of elements in (sorted) sets, hashes, lists, etc.

If you require custom metric collection, you can provide a [Redis Lua script](https://redis.io/commands/eval) using the `-script` flag. An example can be found [in the contrib folder](./contrib/sample_collect_script.lua).

### What does it look like?

Example [Grafana](http://grafana.org/) screenshots:\
<img width="800" alt="redis_exporter_screen" src="https://cloud.githubusercontent.com/assets/1222339/19412031/897549c6-92da-11e6-84a0-b091f9deb81d.png">\
<img width="800" alt="redis_exporter_screen_02" src="https://cloud.githubusercontent.com/assets/1222339/19412041/dee6d7bc-92da-11e6-84f8-610c025d6182.png">

Grafana dashboard is available on [grafana.net](https://grafana.net/dashboards/763) and/or [github.com](contrib/grafana_prometheus_redis_dashboard.json).

Grafana dashboard with host & alias selector is available on [github.com](contrib/grafana_prometheus_redis_dashboard_alias.json).

### What else?

Open an issue or PR if you have more suggestions or ideas about what to add.
