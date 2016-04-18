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
	redisAddr     = flag.String("redis.addr", "localhost:6379", "Address of one or more redis nodes, separated by separator")
	redisPassword = flag.String("redis.password", os.Getenv("REDIS_PASSWORD"), "Password for one or more redis nodes, separated by separator")
	namespace     = flag.String("namespace", "redis", "Namespace for metrics")
	separator     = flag.String("separator", ",", "separator used to split redis.addr and redis.password into several elements.")
	listenAddress = flag.String("web.listen-address", ":9121", "Address to listen on for web interface and telemetry.")
	metricPath    = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
	showVersion   = flag.Bool("version", false, "Show version information")

	// VERSION of Redis Exporter
	VERSION = "0.4"
)

func main() {
	flag.Parse()

	if *showVersion {
		fmt.Printf("Redis Metrics Exporter v%s\n", VERSION)
		return
	}

	addrs := strings.Split(*redisAddr, *separator)
	if len(addrs) == 0 || len(addrs[0]) == 0 {
		log.Fatal("Invalid parameter --redis.addr")
	}
	passwords := strings.Split(*redisPassword, *separator)
	for len(passwords) < len(addrs) {
		passwords = append(passwords, passwords[0])
	}

	e := exporter.NewRedisExporter(exporter.RedisHost{addrs, passwords}, *namespace)
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
