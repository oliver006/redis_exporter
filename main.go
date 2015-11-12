package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/oliver006/redis_exporter/exporter"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	redisAddr     = flag.String("redis.addr", "localhost:6379", "Address of one or more redis nodes, comma separated")
	redisPassword = flag.String("redis.password", os.Getenv("REDIS_PASSWORD"), "Password for Redis server")
	namespace     = flag.String("namespace", "redis", "Namespace for metrics")
	listenAddress = flag.String("web.listen-address", ":9121", "Address to listen on for web interface and telemetry.")
	metricPath    = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
	showVersion   = flag.Bool("version", false, "Show version information")
	// VERSION of Redis Exporter
	VERSION = "0.3"
)

func main() {
	flag.Parse()

	if *showVersion {
		fmt.Printf("Redis Metrics Exporter v%s\n", VERSION)
		return
	}

	addrs := strings.Split(*redisAddr, ",")
	if len(addrs) == 0 || len(addrs[0]) == 0 {
		log.Fatal("Invalid parameter --redis.addr")
	}

	e := exporter.NewRedisExporter(exporter.RedisHost{addrs, *redisPassword}, *namespace)
	prometheus.MustRegister(e)

	http.Handle(*metricPath, prometheus.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
<head><title>Redis exporter</title></head>
<body>
<h1>Redis exporter</h1>
<p><a href='` + *metricPath + `'>Metrics</a></p>
</body>
</html>
						`))
	})

	log.Printf("providing metrics at %s%s", *listenAddress, *metricPath)
	log.Printf("Connecting to: %#v", addrs)
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}
