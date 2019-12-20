package main

import (
	"crypto/tls"
	"flag"
	"io/ioutil"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

var (
	// BuildVersion, BuildDate, BuildCommitSha are filled in by the build script
	BuildVersion   = "<<< filled in by build >>>"
	BuildDate      = "<<< filled in by build >>>"
	BuildCommitSha = "<<< filled in by build >>>"
)

func getEnv(key string, defaultVal string) string {
	if envVal, ok := os.LookupEnv(key); ok {
		return envVal
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	if envVal, ok := os.LookupEnv(key); ok {
		envBool, err := strconv.ParseBool(envVal)
		if err == nil {
			return envBool
		}
	}
	return defaultVal
}

func main() {
	var (
		redisAddr           = flag.String("redis.addr", getEnv("REDIS_ADDR", "redis://localhost:6379"), "Address of the Redis instance to scrape")
		redisPwd            = flag.String("redis.password", getEnv("REDIS_PASSWORD", ""), "Password of the Redis instance to scrape")
		namespace           = flag.String("namespace", getEnv("REDIS_EXPORTER_NAMESPACE", "redis"), "Namespace for metrics")
		checkKeys           = flag.String("check-keys", getEnv("REDIS_EXPORTER_CHECK_KEYS", ""), "Comma separated list of key-patterns to export value and length/size, searched for with SCAN")
		checkSingleKeys     = flag.String("check-single-keys", getEnv("REDIS_EXPORTER_CHECK_SINGLE_KEYS", ""), "Comma separated list of single keys to export value and length/size")
		scriptPath          = flag.String("script", getEnv("REDIS_EXPORTER_SCRIPT", ""), "Path to Lua Redis script for collecting extra metrics")
		listenAddress       = flag.String("web.listen-address", getEnv("REDIS_EXPORTER_WEB_LISTEN_ADDRESS", ":9121"), "Address to listen on for web interface and telemetry.")
		metricPath          = flag.String("web.telemetry-path", getEnv("REDIS_EXPORTER_WEB_TELEMETRY_PATH", "/metrics"), "Path under which to expose metrics.")
		logFormat           = flag.String("log-format", getEnv("REDIS_EXPORTER_LOG_FORMAT", "txt"), "Log format, valid options are txt and json")
		configCommand       = flag.String("config-command", getEnv("REDIS_EXPORTER_CONFIG_COMMAND", "CONFIG"), "What to use for the CONFIG command")
		connectionTimeout   = flag.String("connection-timeout", getEnv("REDIS_EXPORTER_CONNECTION_TIMEOUT", "15s"), "Timeout for connection to Redis instance")
		tlsClientKeyFile    = flag.String("tls-client-key-file", getEnv("REDIS_EXPORTER_TLS_CLIENT_KEY_FILE", ""), "Name of the client key file (including full path) if the server requires TLS client authentication")
		tlsClientCertFile   = flag.String("tls-client-cert-file", getEnv("REDIS_EXPORTER_TLS_CLIENT_CERT_FILE", ""), "Name of the client certificate file (including full path) if the server requires TLS client authentication")
		isDebug             = flag.Bool("debug", getEnvBool("REDIS_EXPORTER_DEBUG", false), "Output verbose debug information")
		setClientName       = flag.Bool("set-client-name", getEnvBool("REDIS_EXPORTER_SET_CLIENT_NAME", true), "Whether to set client name to redis_exporter")
		isTile38            = flag.Bool("is-tile38", getEnvBool("REDIS_EXPORTER_IS_TILE38", false), "Whether to scrape Tile38 specific metrics")
		exportClientList    = flag.Bool("export-client-list", getEnvBool("REDIS_EXPORTER_EXPORT_CLIENT_LIST", false), "Whether to scrape Client List specific metrics")
		showVersion         = flag.Bool("version", false, "Show version information and exit")
		redisMetricsOnly    = flag.Bool("redis-only-metrics", getEnvBool("REDIS_EXPORTER_REDIS_ONLY_METRICS", false), "Whether to also export go runtime metrics")
		pingOnConnect       = flag.Bool("ping-on-connect", getEnvBool("REDIS_EXPORTER_PING_ON_CONNECT", false), "Whether to ping the redis instance after connecting")
		inclSystemMetrics   = flag.Bool("include-system-metrics", getEnvBool("REDIS_EXPORTER_INCL_SYSTEM_METRICS", false), "Whether to include system metrics like e.g. redis_total_system_memory_bytes")
		skipTLSVerification = flag.Bool("skip-tls-verification", getEnvBool("REDIS_EXPORTER_SKIP_TLS_VERIFICATION", false), "Whether to to skip TLS verification")
	)
	flag.Parse()

	switch *logFormat {
	case "json":
		log.SetFormatter(&log.JSONFormatter{})
	default:
		log.SetFormatter(&log.TextFormatter{})
	}
	log.Printf("Redis Metrics Exporter %s    build date: %s    sha1: %s    Go: %s    GOOS: %s    GOARCH: %s",
		BuildVersion, BuildDate, BuildCommitSha,
		runtime.Version(),
		runtime.GOOS,
		runtime.GOARCH,
	)
	if *isDebug {
		log.SetLevel(log.DebugLevel)
		log.Debugln("Enabling debug output")
	} else {
		log.SetLevel(log.InfoLevel)
	}

	if *showVersion {
		return
	}

	to, err := time.ParseDuration(*connectionTimeout)
	if err != nil {
		log.Fatalf("Couldn't parse connection timeout duration, err: %s", err)
	}

	var tlsClientCertificates []tls.Certificate
	if (*tlsClientKeyFile != "") != (*tlsClientCertFile != "") {
		log.Fatal("TLS client key file and cert file should both be present")
	}
	if *tlsClientKeyFile != "" && *tlsClientCertFile != "" {
		cert, err := tls.LoadX509KeyPair(*tlsClientCertFile, *tlsClientKeyFile)
		if err != nil {
			log.Fatalf("Couldn't load TLS client key pair, err: %s", err)
		}
		tlsClientCertificates = append(tlsClientCertificates, cert)
	}

	var ls []byte
	if *scriptPath != "" {
		if ls, err = ioutil.ReadFile(*scriptPath); err != nil {
			log.Fatalf("Error loading script file %s    err: %s", *scriptPath, err)
		}
	}

	registry := prometheus.NewRegistry()
	if !*redisMetricsOnly {
		registry = prometheus.DefaultRegisterer.(*prometheus.Registry)
	}

	exp, err := NewRedisExporter(
		*redisAddr,
		ExporterOptions{
			Password:            *redisPwd,
			Namespace:           *namespace,
			ConfigCommandName:   *configCommand,
			CheckKeys:           *checkKeys,
			CheckSingleKeys:     *checkSingleKeys,
			LuaScript:           ls,
			InclSystemMetrics:   *inclSystemMetrics,
			SetClientName:       *setClientName,
			IsTile38:            *isTile38,
			ExportClientList:    *exportClientList,
			SkipTLSVerification: *skipTLSVerification,
			ClientCertificates:  tlsClientCertificates,
			ConnectionTimeouts:  to,
			MetricsPath:         *metricPath,
			RedisMetricsOnly:    *redisMetricsOnly,
			PingOnConnect:       *pingOnConnect,
			Registry:            registry,
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	log.Infof("Providing metrics at %s%s", *listenAddress, *metricPath)
	log.Debugf("Configured redis addr: %#v", *redisAddr)
	log.Fatal(http.ListenAndServe(*listenAddress, exp))
}
