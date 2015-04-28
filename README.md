# Redis Metrics Exporter

Prometheus exporter for Redis metrics.<br>
Supports Redis 2.x and 3.x

## Building and running

    go build
    ./redis_exporter <flags>

### Flags

Name               | Description
-------------------|------------
redis.addr         | Address of one or more redis nodes, comma separated.
namespace          | Namespace for the metrics, defaults to "redis".
web.listen-address | Address to listen on for web interface and telemetry.
web.telemetry-path | Path under which to expose metrics.


### What's exported?

Most items from the INFO command are exported,
see http://redis.io/commands/info for details.<br>
In addition, for every database there are metrics for total keys, expiring keys and the average TTL for keys in the database.<br> 


### What does it look like?
Example [PromDash](https://github.com/prometheus/promdash) screenshots:<br>
![screen1](https://cloud.githubusercontent.com/assets/1222339/7362443/c3cbc2f8-ed36-11e4-8955-cf88e5383e8b.png)<br>
![screen2](https://cloud.githubusercontent.com/assets/1222339/7362444/c3f5b324-ed36-11e4-9c95-ec84e8217fa8.png)


### What else?

Open an issue or PR if you have more suggestions or ideas about what to add.
