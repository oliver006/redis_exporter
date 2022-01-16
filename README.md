# Prometheus Redis Metrics Exporter

[![Build Status](https://cloud.drone.io/api/badges/oliver006/redis_exporter/status.svg)](https://cloud.drone.io/oliver006/redis_exporter)
 [![Coverage Status](https://coveralls.io/repos/github/oliver006/redis_exporter/badge.svg?branch=master)](https://coveralls.io/github/oliver006/redis_exporter?branch=master) [![codecov](https://codecov.io/gh/oliver006/redis_exporter/branch/master/graph/badge.svg)](https://codecov.io/gh/oliver006/redis_exporter) [![docker_pulls](https://img.shields.io/docker/pulls/oliver006/redis_exporter.svg)](https://img.shields.io/docker/pulls/oliver006/redis_exporter.svg)

Prometheus exporter for Redis metrics.\
Supports Redis 2.x, 3.x, 4.x, 5.x, and 6.x


## Building and running the exporter

### Build and run locally

```sh
git clone https://github.com/oliver006/redis_exporter.git
cd redis_exporter
make build
./redis_exporter --version
```


### Pre-build binaries

For pre-built binaries please take a look at [the releases](https://github.com/oliver006/redis_exporter/releases).


### Basic Prometheus Configuration

Add a block to the `scrape_configs` of your prometheus.yml config file:

```yaml
scrape_configs:
  - job_name: redis_exporter
    static_configs:
    - targets: ['<<REDIS-EXPORTER-HOSTNAME>>:9121']
```

and adjust the host name accordingly.


### Kubernetes SD configurations

To have instances in the drop-down as human readable names rather than IPs, it is suggested to use [instance relabelling](https://www.robustperception.io/controlling-the-instance-label).

For example, if the metrics are being scraped via the pod role, one could add:

```yaml
          - source_labels: [__meta_kubernetes_pod_name]
            action: replace
            target_label: instance
            regex: (.*redis.*)
```

as a relabel config to the corresponding scrape config. As per the regex value, only pods with "redis" in their name will be relabelled as such.

Similar approaches can be taken with [other role types](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#kubernetes_sd_config) depending on how scrape targets are retrieved.


### Prometheus Configuration to Scrape Multiple Redis Hosts

Run the exporter with the command line flag `--redis.addr=` so it won't try to access the local instance every time the `/metrics` endpoint is scraped.

```yaml
scrape_configs:
  ## config for the multiple Redis targets that the exporter will scrape
  - job_name: 'redis_exporter_targets'
    static_configs:
      - targets:
        - redis://first-redis-host:6379
        - redis://second-redis-host:6379
        - redis://second-redis-host:6380
        - redis://second-redis-host:6381
    metrics_path: /scrape
    relabel_configs:
      - source_labels: [__address__]
        target_label: __param_target
      - source_labels: [__param_target]
        target_label: instance
      - target_label: __address__
        replacement: <<REDIS-EXPORTER-HOSTNAME>>:9121

  ## config for scraping the exporter itself
  - job_name: 'redis_exporter'
    static_configs:
      - targets:
        - <<REDIS-EXPORTER-HOSTNAME>>:9121
```

The Redis instances are listed under `targets`, the Redis exporter hostname is configured via the last relabel_config rule.\
If authentication is needed for the Redis instances then you can set the password via the `--redis.password` command line option of
the exporter (this means you can currently only use one password across the instances you try to scrape this way. Use several
exporters if this is a problem). \
You can also use a json file to supply multiple targets by using `file_sd_configs` like so:

```yaml

scrape_configs:
  - job_name: 'redis_exporter_targets'
    file_sd_configs:
      - files:
        - targets-redis-instances.json
    metrics_path: /scrape
    relabel_configs:
      - source_labels: [__address__]
        target_label: __param_target
      - source_labels: [__param_target]
        target_label: instance
      - target_label: __address__
        replacement: <<REDIS-EXPORTER-HOSTNAME>>:9121

  ## config for scraping the exporter itself
  - job_name: 'redis_exporter'
    static_configs:
      - targets:
        - <<REDIS-EXPORTER-HOSTNAME>>:9121
```

The `targets-redis-instances.json` should look something like this:

```json
[
  {
    "targets": [ "redis://redis-host-01:6379", "redis://redis-host-02:6379"],
    "labels": { }
  }
]
```

Prometheus uses file watches and all changes to the json file are applied immediately.


### Command line flags

Name                        | Environment Variable Name                  | Description
----------------------------|--------------------------------------------|-----------------
redis.addr                  | REDIS_ADDR                                 | Address of the Redis instance, defaults to `redis://localhost:6379`.
redis.user                  | REDIS_USER                                 | User name to use for authentication (Redis ACL for Redis 6.0 and newer).
redis.password              | REDIS_PASSWORD                             | Password of the Redis instance, defaults to `""` (no password).
redis.password-file         | REDIS_PASSWORD_FILE                        | Password file of the Redis instance to scrape, defaults to `""` (no password file).
check-keys                  | REDIS_EXPORTER_CHECK_KEYS                  | Comma separated list of key patterns to export value and length/size, eg: `db3=user_count` will export key `user_count` from db `3`. db defaults to `0` if omitted. The key patterns specified with this flag will be found using [SCAN](https://redis.io/commands/scan).  Use this option if you need glob pattern matching; `check-single-keys` is faster for non-pattern keys. Warning: using `--check-keys` to match a very large number of keys can slow down the exporter to the point where it doesn't finish scraping the redis instance.
check-single-keys           | REDIS_EXPORTER_CHECK_SINGLE_KEYS           | Comma separated list of keys to export value and length/size, eg: `db3=user_count` will export key `user_count` from db `3`. db defaults to `0` if omitted.  The keys specified with this flag will be looked up directly without any glob pattern matching.  Use this option if you don't need glob pattern matching;  it is faster than `check-keys`.
check-streams               | REDIS_EXPORTER_CHECK_STREAMS               | Comma separated list of stream-patterns to export info about streams, groups and consumers. Syntax is the same as `check-keys`.
check-single-streams        | REDIS_EXPORTER_CHECK_SINGLE_STREAMS        | Comma separated list of streams to export info about streams, groups and consumers. The streams specified with this flag will be looked up directly without any glob pattern matching.  Use this option if you don't need glob pattern matching;  it is faster than `check-streams`.
check-keys-batch-size        | REDIS_EXPORTER_CHECK_KEYS_BATCH_SIZE      | Approximate number of keys to process in each execution. This is basically the COUNT option that will be passed into the SCAN command as part of the execution of the key or key group metrics, see [COUNT option](https://redis.io/commands/scan#the-count-option). Larger value speeds up scanning. Still Redis is a single-threaded app, huge `COUNT` can affect production environment.
count-keys                  | REDIS_EXPORTER_COUNT_KEYS                  | Comma separated list of patterns to count, eg: `db3=sessions:*` will count all keys with prefix `sessions:` from db `3`. db defaults to `0` if omitted. Warning: The exporter runs SCAN to count the keys. This might not perform well on large databases.
script                      | REDIS_EXPORTER_SCRIPT                      | Path to Redis Lua script for gathering extra metrics.
debug                       | REDIS_EXPORTER_DEBUG                       | Verbose debug output
log-format                  | REDIS_EXPORTER_LOG_FORMAT                  | Log format, valid options are `txt` (default) and `json`.
namespace                   | REDIS_EXPORTER_NAMESPACE                   | Namespace for the metrics, defaults to `redis`.
connection-timeout          | REDIS_EXPORTER_CONNECTION_TIMEOUT          | Timeout for connection to Redis instance, defaults to "15s" (in Golang duration format)
web.listen-address          | REDIS_EXPORTER_WEB_LISTEN_ADDRESS          | Address to listen on for web interface and telemetry, defaults to `0.0.0.0:9121`.
web.telemetry-path          | REDIS_EXPORTER_WEB_TELEMETRY_PATH          | Path under which to expose metrics, defaults to `/metrics`.
redis-only-metrics          | REDIS_EXPORTER_REDIS_ONLY_METRICS          | Whether to also export go runtime metrics, defaults to false.
include-system-metrics      | REDIS_EXPORTER_INCL_SYSTEM_METRICS         | Whether to include system metrics like `total_system_memory_bytes`, defaults to false.
ping-on-connect             | REDIS_EXPORTER_PING_ON_CONNECT             | Whether to ping the redis instance after connecting and record the duration as a metric, defaults to false.
is-tile38                   | REDIS_EXPORTER_IS_TILE38                   | Whether to scrape Tile38 specific metrics, defaults to false.
is-cluster                  | REDIS_EXPORTER_IS_CLUSTER                  | Whether this is a redis cluster (Enable this if you need to fetch key level data on a Redis Cluster).
export-client-list          | REDIS_EXPORTER_EXPORT_CLIENT_LIST          | Whether to scrape Client List specific metrics, defaults to false.
export-client-port          | REDIS_EXPORTER_EXPORT_CLIENT_PORT          | Whether to include the client's port when exporting the client list. Warning: including the port increases the number of metrics generated and will make your Prometheus server take up more memory
skip-tls-verification       | REDIS_EXPORTER_SKIP_TLS_VERIFICATION       | Whether to to skip TLS verification
tls-client-key-file         | REDIS_EXPORTER_TLS_CLIENT_KEY_FILE         | Name of the client key file (including full path) if the server requires TLS client authentication
tls-client-cert-file        | REDIS_EXPORTER_TLS_CLIENT_CERT_FILE        | Name the client cert file (including full path) if the server requires TLS client authentication
tls-server-key-file         | REDIS_EXPORTER_TLS_SERVER_KEY_FILE         | Name of the server key file (including full path) if the web interface and telemetry should use TLS
tls-server-cert-file        | REDIS_EXPORTER_TLS_SERVER_CERT_FILE        | Name of the server certificate file (including full path) if the web interface and telemetry should use TLS
tls-server-ca-cert-file     | REDIS_EXPORTER_TLS_SERVER_CA_CERT_FILE     | Name of the CA certificate file (including full path) if the web interface and telemetry should require TLS client authentication
tls-ca-cert-file            | REDIS_EXPORTER_TLS_CA_CERT_FILE            | Name of the CA certificate file (including full path) if the server requires TLS client authentication
set-client-name             | REDIS_EXPORTER_SET_CLIENT_NAME             | Whether to set client name to redis_exporter, defaults to true.
check-key-groups            | REDIS_EXPORTER_CHECK_KEY_GROUPS            | Comma separated list of [LUA regexes](https://www.lua.org/pil/20.1.html) for classifying keys into groups. The regexes are applied in specified order to individual keys, and the group name is generated by concatenating all capture groups of the first regex that matches a key. A key will be tracked under the `unclassified` group if none of the specified regexes matches it.
max-distinct-key-groups     | MAX_DISTINCT_KEY_GROUPS                    | Maximum number of distinct key groups that can be tracked independently *per Redis database*. If exceeded, only key groups with the highest memory consumption within the limit will be tracked separately, all remaining key groups will be tracked under a single `overflow` key group.


Redis instance addresses can be tcp addresses: `redis://localhost:6379`, `redis.example.com:6379` or e.g. unix sockets: `unix:///tmp/redis.sock`.\
SSL is supported by using the `rediss://` schema, for example: `rediss://azure-ssl-enabled-host.redis.cache.windows.net:6380` (note that the port is required when connecting to a non-standard 6379 port, e.g. with Azure Redis instances).\

Command line settings take precedence over any configurations provided by the environment variables.


### Authenticating with Redis

If your Redis instance requires authentication then there are several ways how you can supply 
a username (new in Redis 6.x with ACLs) and a password.

You can provide the username and password as part of the address, see [here](https://www.iana.org/assignments/uri-schemes/prov/redis) for the official documentation of the `redis://` scheme.
You can set `-redis.password-file=sample-pwd-file.json` to specify a password file when using the `/scrape` endpoint, It only takes effect when `redis.password == ""`


An example for a URI including a password is: `redis://<<username (optional)>>:<<PASSWORD>>@<<HOSTNAME>>:<<PORT>>`

Alternatively, you can provide the username and/or password using the `--redis.user` and `--redis.password` directly to the redis_exporter.

If you want to use a dedicated Redis user for the redis_exporter (instead of the default user) then you need enable a list of commands for that user.
You can use the following Redis command to set up the user, just replace `<<<USERNAME>>>` and `<<<PASSWORD>>>` with your desired values.
```
ACL SETUSER <<<USERNAME>>> +client +ping +info +config|get +cluster|info +slowlog +latency +memory +select +get +scan +xinfo +type +pfcount +strlen +llen +scard +zcard +hlen +xlen +eval allkeys on > <<<PASSWORD>>>
```


### Run via Docker

The latest release is automatically published to the [Docker registry](https://hub.docker.com/r/oliver006/redis_exporter/).

You can run it like this:

```sh
docker run -d --name redis_exporter -p 9121:9121 oliver006/redis_exporter
```

Docker images are also published to the [quay.io docker repo](https://quay.io/oliver006/redis_exporter) so you can pull them from there if for instance you run into rate limiting issues with Docker hub.

```sh
docker run -d --name redis_exporter -p 9121:9121 quay.io/oliver006/redis_exporter
```

The `latest` docker image contains only the exporter binary.
If e.g. for debugging purposes, you need the exporter running
in an image that has a shell then you can run the `alpine` image:

```sh
docker run -d --name redis_exporter -p 9121:9121 oliver006/redis_exporter:alpine
```

If you try to access a Redis instance running on the host node, you'll need to add `--network host` so the
redis_exporter container can access it:

```sh
docker run -d --name redis_exporter --network host oliver006/redis_exporter
```

### Run on Kubernetes

[Here](contrib/k8s-redis-and-exporter-deployment.yaml) is an example Kubernetes deployment configuration for how to deploy the redis_exporter as a sidecar to a Redis instance.


### Tile38

[Tile38](https://tile38.com) now has native Prometheus support for exporting server metrics and basic stats about number of objects, strings, etc.
You can also use redis_exporter to export Tile38 metrics, especially more advanced metrics by using Lua scripts or the `-check-keys` flag.\
To enable Tile38 support, run the exporter with `--is-tile38=true`.


## What's exported

Most items from the INFO command are exported,
see [Redis documentation](https://redis.io/commands/info) for details.\
In addition, for every database there are metrics for total keys, expiring keys and the average TTL for keys in the database.\
You can also export values of keys if they're in numeric format by using the `-check-keys` flag. The exporter will also export the size (or, depending on the data type, the length) of the key. This can be used to export the number of elements in (sorted) sets, hashes, lists, streams, etc.

If you require custom metric collection, you can provide a [Redis Lua script](https://redis.io/commands/eval) using the `-script` flag. An example can be found [in the contrib folder](./contrib/sample_collect_script.lua).


### The redis_memory_max_bytes metric

The metric `redis_memory_max_bytes`  will show the maximum number of bytes Redis can use.\
It is zero if no memory limit is set for the Redis instance you're scraping (this is the default setting for Redis).\
You can confirm that's the case by checking if the metric `redis_config_maxmemory` is zero or by connecting to the Redis instance via redis-cli and running the command `CONFIG GET MAXMEMORY`.


## What it looks like

Example [Grafana](http://grafana.org/) screenshots:
![redis_exporter_screen_01](https://cloud.githubusercontent.com/assets/1222339/19412031/897549c6-92da-11e6-84a0-b091f9deb81d.png)

![redis_exporter_screen_02](https://cloud.githubusercontent.com/assets/1222339/19412041/dee6d7bc-92da-11e6-84f8-610c025d6182.png)

Grafana dashboard is available on [grafana.com](https://grafana.com/dashboards/763) and/or [github.com](contrib/grafana_prometheus_redis_dashboard.json).

### Viewing multiple Redis simultaneously

If running [Redis Sentinel](https://redis.io/topics/sentinel), it may be desirable to view the metrics of the various cluster members simultaneously. For this reason the dashboard's drop down is of the multi-value type, allowing for the selection of multiple Redis. Please note that there is a  caveat; the single stat panels up top namely `uptime`, `total memory use` and `clients` do not function upon viewing multiple Redis.


## Using the mixin
There is a set of sample rules, alerts and dashboards available in [redis-mixin](contrib/redis-mixin/)

## Upgrading from 0.x to 1.x

[PR #256](https://github.com/oliver006/redis_exporter/pull/256) introduced breaking changes which were released as version v1.0.0.

If you only scrape one Redis instance and use command line flags `--redis.address`
and `--redis.password` then you're most probably not affected.
Otherwise, please see [PR #256](https://github.com/oliver006/redis_exporter/pull/256) and [this README](https://github.com/oliver006/redis_exporter#prometheus-configuration-to-scrape-multiple-redis-hosts) for more information.

## Memory Usage Aggregation by Key Groups

When a single Redis instance is used for multiple purposes, it is useful to be able to see how Redis memory is consumed among the different usage scenarios. This is particularly important when a Redis instance with no eviction policy is running low on memory as we want to identify whether certain applications are misbehaving (e.g. not deleting keys that are no longer in use) or the Redis instance needs to be scaled up to handle the increased resource demand. Fortunately, most applications using Redis will employ some sort of naming conventions for keys tied to their specific purpose such as (hierarchical) namespace prefixes which can be exploited by the check-keys, check-single-keys, and count-keys parameters of redis_exporter to surface the memory usage metrics of specific scenarios. *Memory usage aggregation by key groups* takes this one step further by harnessing the flexibility of Redis LUA scripting support to classify all keys on a Redis instance into groups through a list of user-defined [LUA regular expressions](https://www.lua.org/pil/20.1.html) so memory usage metrics can be aggregated into readily identifiable groups.

To enable memory usage aggregation by key groups, simply specify a non-empty comma-separated list of LUA regular expressions through the `check-key-groups` redis_exporter parameter. On each aggregation of memory metrics by key groups, redis_exporter will set up a `SCAN` cursor through all keys for each Redis database to be processed in batches via a LUA script. Each key batch is then processed by the same LUA script on a key-by-key basis as follows:

  1. The `MEMORY USAGE` command is called to gather memory usage for each key
  2. The specified LUA regexes are applied to each key in the specified order, and the group name that a given key belongs to will be derived from concatenating the capture groups of the first regex that matches the key. For example, applying the regex `^(.*)_[^_]+$` to the key `key_exp_Nick` would yield a group name of `key_exp`. If none of the specified regexes matches a key, the key will be assigned to the `unclassified` group

Once a key has been classified, the memory usage and key counter for the corresponding group will be incremented in a local LUA table. This aggregated metrics table will then be returned alongside the next `SCAN` cursor position to redis_exporter when all keys in a batch have been processed, and redis_exporter can aggregate the data from all batches into a single table of grouped memory usage metrics for the Prometheus metrics scrapper.

Besides making the full flexibility of LUA regex available for classifying keys into groups, the LUA script also has the benefit of reducing network traffic by executing all `MEMORY USAGE` commands on the Redis server and returning aggregated data to redis_exporter in a far more compact format than key-level data. The use of `SCAN` cursor over batches of keys processed by a server-side LUA script also helps prevent unbounded latency bubble in Redis's single processing thread, and the batch size can be tailored to specific environments via the `check-keys-batch-size` parameter.

Scanning the entire key space of a Redis instance may sound a lttle extravagant, but it takes only a single scan to classify all keys into groups, and on a moderately sized system with ~780K keys and a rather complex list of 17 regexes, it takes an average of ~5s to perform a full aggregation of memory usage by key groups. Of course, the actual performance for specific systems will vary widely depending on the total number of keys, the number and complexity of regexes used for classification, and the configured batch size.

To protect Prometheus from being overwhelmed by a large number of time series resulting from misconfigured group classification regular expression (e.g. applying the regular expression `^(.*)$` where each key will be classified into its own distinct group), a limit on the number of distinct key groups *per Redis database* can be configured via the `max-distinct-key-groups` parameter. If the `max-distinct-key-groups` limit is exceeded, only the key groups with the highest memory usage within the limit will be tracked separately, remaining key groups will be reported under a single `overflow` key group.

Here is a list of additional metrics that will be exposed when memory usage aggregation by key groups is enabled:

|Name                                              |Labels      |Description
|--------------------------------------------------|------------|-----------
|redis_key_group_count                             |db,key_group|Number of keys in a key group
|redis_key_group_memory_usage_bytes                |db,key_group|Memory usage by key group
|redis_number_of_distinct_key_groups               |db          |Number of distinct key groups in a Redis database when the `overflow` group is fully expanded
|redis_last_key_groups_scrape_duration_milliseconds|            |Duration of the last memory usage aggregation by key groups in milliseconds

### Script to collect Redis lists and respective sizes.
If using Redis version < 4.0, most of the helpful metrics which we need to gather based on length or memory is not possible via default redis_exporter.
With the help of LUA scripts, we can gather these metrics.
One of these scripts [contrib/collect_lists_length_growing.lua](./contrib/collect_lists_length_growing.lua) will help to collect the length of redis lists.
With this count, we can take following actions such as Create alerts or dashboards in Grafana or any similar tools with these Prometheus metrics.

## Development

The tests require a variety of real Redis instances to not only verify correctness of the exporter but also
compatibility with older versions of Redis and with Redis-like systems like KeyDB or Tile38.\
The [contrib/docker-compose-for-tests.yml](./contrib/docker-compose-for-tests.yml) file has service definitions for
everything that's needed.\
You can bring up the Redis test instances first by running `make docker-env-up` and then, every time you want to run the tests, you can run `make docker-test`. This will mount the current directory (with the .go source files) into a docker container and kick off the tests.\
Once you're done testing you can bring down the stack by running `make docker-env-down`.\
Or you can bring up the stack, run the tests, and then tear down the stack, all in one shot, by running `make docker-all`.

***Note.** Tests initialization can lead to unexpected results when using a persistent testing environment. When `make docker-env-up` is executed once and `make docker-test` is constantly run or stopped during execution, the number of keys in the database changes, which can lead to unexpected failures of tests. Use `make docker-env-down` periodacally to clean up as a workaround.*

## Communal effort

Open an issue or PR if you have more suggestions, questions or ideas about what to add.
