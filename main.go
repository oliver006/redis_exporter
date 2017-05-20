package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/oliver006/redis_exporter/exporter"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/log"
	"github.com/prometheus/common/version"
)

func init() {
	prometheus.MustRegister(version.NewCollector("redis_exporter"))
}

func main() {
	var (
		checkKeys     = flag.String("check-keys", "", "Comma separated list of keys to export value and length/size")
		listenAddress = flag.String("web.listen-address", ":9121", "Address to listen on for web interface and telemetry.")
		namespace     = flag.String("namespace", "redis", "Namespace for metrics")
		metricsPath   = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
		redisAddr     = flag.String("redis.addr", getEnv("REDIS_ADDR", "redis://localhost:6379"), "Address of one or more redis nodes, separated by separator")
		redisPassword = flag.String("redis.password", getEnv("REDIS_PASSWORD", ""), "Password for one or more redis nodes, separated by separator")
		redisAlias    = flag.String("redis.alias", getEnv("REDIS_ALIAS", ""), "Redis instance alias for one or more redis nodes, separated by separator")
		separator     = flag.String("separator", ",", "separator used to split redis.addr, redis.password and redis.alias into several elements.")
		showVersion   = flag.Bool("version", false, "Show version information and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Fprintln(os.Stdout, version.Print("redis_exporter"))
		os.Exit(0)
	}

	log.Infoln("Starting Redis Metrics Exporter", version.Info())
	log.Infoln("Build context", version.BuildContext())

	addrs := strings.Split(*redisAddr, *separator)
	passwords := strings.Split(*redisPassword, *separator)
	for len(passwords) < len(addrs) {
		passwords = append(passwords, passwords[0])
	}
	aliases := strings.Split(*redisAlias, *separator)
	for len(aliases) < len(addrs) {
		aliases = append(aliases, aliases[0])
	}

	exp, err := exporter.NewRedisExporter(
		exporter.RedisHost{Addrs: addrs, Passwords: passwords, Aliases: aliases},
		*namespace,
		*checkKeys)
	if err != nil {
		log.Fatal(err)
	}
	prometheus.MustRegister(exp)

	handler := promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{ErrorLog: log.NewErrorLogger()})

	http.Handle(*metricsPath, prometheus.InstrumentHandler("prometheus", handler))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
			<head><title>Redis Exporter</title></head>
			<body>
			<h1>Redis Exporter</h1>
			<p><a href="` + *metricsPath + `">Metrics</a></p>
			</body>
			</html>`))
	})

	log.Infoln("Listening on", *listenAddress, *metricsPath)
	log.Infoln("Connecting to redis hosts:", addrs)
	log.Infoln("Using alias:", aliases)
	err = http.ListenAndServe(*listenAddress, nil)
	if err != nil {
		log.Fatal(err)
	}
}

// getEnv gets an environment variable from a given key and if it doesn't exist,
// returns defaultVal given.
func getEnv(key string, defaultVal string) string {
	if envVal, ok := os.LookupEnv(key); ok {
		return envVal
	}
	return defaultVal
}
