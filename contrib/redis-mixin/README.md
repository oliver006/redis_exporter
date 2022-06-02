# Redis Mixin

_This is a work in progress. We aim for it to become a good role model for alerts
and dashboards eventually, but it is not quite there yet._

The Redis Mixin is a set of configurable, reusable, and extensible alerts and
dashboards based on the metrics exported by the Redis Exporter. The mixin creates
recording and alerting rules for Prometheus and suitable dashboard descriptions
for Grafana.

To use them, you need to have `mixtool` and `jsonnetfmt` installed. If you
have a working Go development environment, it's easiest to run the following:
```bash
# go >= 1.17
# Using `go get` to install binaries is deprecated.
$ go install github.com/monitoring-mixins/mixtool/cmd/mixtool@latest
$ go install github.com/google/go-jsonnet/cmd/jsonnet@latest

# go < 1.17
$ go get github.com/monitoring-mixins/mixtool/cmd/mixtool
$ go get github.com/google/go-jsonnet/cmd/jsonnetfmt
```

You can then build the Prometheus rules files `alerts.yaml` and
`rules.yaml` and a directory `dashboard_out` with the JSON dashboard files
for Grafana:
```bash
$ make build
```

The mixin currently treats each redis instance independently - it has no notion of replication or clustering. We aim to support these concepts in future versions. The mixin dashboard is a fork of the one in the [contrib](contrib/) directory.

For more advanced uses of mixins, see
https://github.com/monitoring-mixins/docs.
