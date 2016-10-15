package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/oliver006/redis_exporter/exporter"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	redisAddr     = flag.String("redis.addr", "localhost:6379", "Address of one or more redis nodes, separated by separator")
	redisPassword = flag.String("redis.password", os.Getenv("REDIS_PASSWORD"), "Password for one or more redis nodes, separated by separator")
	namespace     = flag.String("namespace", "redis", "Namespace for metrics")
	checkKeys     = flag.String("check-keys", "", "Comma separated list of keys to export value and length/size")
	separator     = flag.String("separator", ",", "separator used to split redis.addr and redis.password into several elements.")
	listenAddress = flag.String("web.listen-address", ":9121", "Address to listen on for web interface and telemetry.")
	metricPath    = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
	showVersion   = flag.Bool("version", false, "Show version information and exit")

	// VERSION, BUILD_DATE, GIT_COMMIT are filled in by the CircleCI build
	VERSION     = "<<< filled in by build >>>"
	BUILD_DATE  = "<<< filled in by build >>>"
	COMMIT_SHA1 = "<<< filled in by build >>>"
)

func main() {
	flag.Parse()

	log.Printf("Redis Metrics Exporter %s    build date: %s    sha1: %s\n", VERSION, BUILD_DATE, COMMIT_SHA1)
	if *showVersion {
		return
	}

	addrs := strings.Split(*redisAddr, *separator)
	passwords := strings.Split(*redisPassword, *separator)
	for len(passwords) < len(addrs) {
		passwords = append(passwords, passwords[0])
	}

	e, err := exporter.NewRedisExporter(exporter.RedisHost{Addrs: addrs, Passwords: passwords}, *namespace, *checkKeys)
	if err != nil {
		log.Fatal(err)
	}
	prometheus.MustRegister(e)

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "redis_exporter_build_info",
		Help: "redis exporter build_info",
	}, []string{"version", "commit_sha", "build_date", "golang_version"})
	prometheus.MustRegister(buildInfo)
	buildInfo.WithLabelValues(VERSION, COMMIT_SHA1, BUILD_DATE, runtime.Version()).Set(1)

	http.Handle(*metricPath, prometheus.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
<head><title>Redis Exporter v` + VERSION + `</title></head>
<body>
<h1>Redis Exporter v` + VERSION + `</h1>
<p><a href='` + *metricPath + `'>Metrics</a></p>
</body>
</html>
						`))
	})

	log.Printf("Providing metrics at %s%s", *listenAddress, *metricPath)
	log.Printf("Connecting to: %#v", addrs)
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}
