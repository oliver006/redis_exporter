# Redis Metrics Exporter
[![Circle CI](https://circleci.com/gh/oliver006/redis_exporter.svg?style=shield)](https://circleci.com/gh/oliver006/redis_exporter) [![Coverage Status](https://coveralls.io/repos/github/oliver006/redis_exporter/badge.svg?branch=master)](https://coveralls.io/github/oliver006/redis_exporter?branch=master)

Prometheus exporter for Redis metrics.<br>
Supports Redis 2.x and 3.x

## Building, configuring, and running

Locally build and run it:

```
    $ go get
    $ go build
    $ ./redis_exporter <flags>
```

You can also run it via docker: 

```
    $ docker pull oliver006/redis_exporter
    $ docker run -d --name redis_exporter -p 9121:9121 oliver006/redis_exporter
```

Add a block to the `scrape_configs` of your prometheus.yml config file:

```
scrape_configs:

...

- job_name: redis_exporter
  static_configs:
  - targets: ['localhost:9121']

...
```
and adjust the host name accordingly.


### Flags

Name               | Description
-------------------|------------
debug              | Verbose debug output
log-format         | Log format, valid options are `txt` (default) and `json`.
check-keys         | Comma separated list of keys to export value and length/size, eg: `db3=user_count` will export key `user_count` from db `3`. db defaults to `0` if omitted. 
redis.addr         | Address of one or more redis nodes, comma separated, defaults to `redis://localhost:6379`.
redis.password     | Password to use when authenticating to Redis
redis.alias        | Alias for redis node addr, comma separated.
namespace          | Namespace for the metrics, defaults to `redis`.
web.listen-address | Address to listen on for web interface and telemetry, defaults to `0.0.0.0:9121`.
web.telemetry-path | Path under which to expose metrics, defaults to `metrics`.

Redis node addresses can be tcp addresses like `redis://localhost:6379`, `redis.example.com:6379` or unix socket addresses like `unix:///tmp/redis.sock`. <br>
SSL is supported by using the `rediss://` schema, for example: `rediss://azure-ssl-enabled-host.redis.cache.windows.net:6380` (note that the port is required when connecting to a non-standard 6379 port, e.g. with Azure Redis instances).

These settings take precedence over any configurations provided by [environment variables](#environment-variables).


### Environment Variables

Name               | Description
-------------------|------------
REDIS_ADDR         | Address of Redis node(s)
REDIS_PASSWORD     | Password to use when authenticating to Redis
REDIS_ALIAS        | Alias name of Redis node(s)

### What's exported?

Most items from the INFO command are exported,
see http://redis.io/commands/info for details.<br>
In addition, for every database there are metrics for total keys, expiring keys and the average TTL for keys in the database.<br> 
You can also export values of keys if they're in numeric format by using the `-check-keys` flag. The exporter will also export the size (or, depending on the data type, the length) of the key. This can be used to export the number of elements in (sorted) sets, hashes, lists, etc. <br>


### What does it look like?
Example [Grafana](http://grafana.org/) screenshots:<br>
<img width="800" alt="redis_exporter_screen" src="https://cloud.githubusercontent.com/assets/1222339/19412031/897549c6-92da-11e6-84a0-b091f9deb81d.png"><br>
<img width="800" alt="redis_exporter_screen_02" src="https://cloud.githubusercontent.com/assets/1222339/19412041/dee6d7bc-92da-11e6-84f8-610c025d6182.png">

Grafana dashboard is available on [grafana.net](https://grafana.net/dashboards/763) and/or [github.com](https://github.com/oliver006/redis_exporter/blob/master/grafana_prometheus_redis_dashboard.json).

Grafana dashboard with host & alias selector is available on [github.com](https://github.com/oliver006/redis_exporter/blob/master/grafana_prometheus_redis_dashboard_alias.json).

### What else?

Open an issue or PR if you have more suggestions or ideas about what to add.
