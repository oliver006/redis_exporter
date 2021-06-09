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

	"github.com/oliver006/redis_exporter/exporter"
)

var (
	/*
		BuildVersion, BuildDate, BuildCommitSha are filled in by the build script
	*/
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

func getEnvInt64(key string, defaultVal int64) int64 {
	if envVal, ok := os.LookupEnv(key); ok {
		envInt64, err := strconv.ParseInt(envVal, 10, 64)
		if err == nil {
			return envInt64
		}
	}
	return defaultVal
}

func main() {
	var (
		redisAddr            = flag.String("redis.addr", getEnv("REDIS_ADDR", "redis://localhost:6379"), "Address of the Redis instance to scrape")
		redisUser            = flag.String("redis.user", getEnv("REDIS_USER", ""), "User name to use for authentication (Redis ACL for Redis 6.0 and newer)")
		redisPwd             = flag.String("redis.password", getEnv("REDIS_PASSWORD", ""), "Password of the Redis instance to scrape")
		redisPwdFile         = flag.String("redis.password-file", getEnv("REDIS_PASSWORD_FILE", ""), "Password file of the Redis instance to scrape")
		namespace            = flag.String("namespace", getEnv("REDIS_EXPORTER_NAMESPACE", "redis"), "Namespace for metrics")
		checkKeys            = flag.String("check-keys", getEnv("REDIS_EXPORTER_CHECK_KEYS", ""), "Comma separated list of key-patterns to export value and length/size, searched for with SCAN")
		checkSingleKeys      = flag.String("check-single-keys", getEnv("REDIS_EXPORTER_CHECK_SINGLE_KEYS", ""), "Comma separated list of single keys to export value and length/size")
		checkKeyGroups       = flag.String("check-key-groups", getEnv("REDIS_EXPORTER_CHECK_KEY_GROUPS", ""), "Comma separated list of lua regex for grouping keys")
		checkStreams         = flag.String("check-streams", getEnv("REDIS_EXPORTER_CHECK_STREAMS", ""), "Comma separated list of stream-patterns to export info about streams, groups and consumers, searched for with SCAN")
		checkSingleStreams   = flag.String("check-single-streams", getEnv("REDIS_EXPORTER_CHECK_SINGLE_STREAMS", ""), "Comma separated list of single streams to export info about streams, groups and consumers")
		countKeys            = flag.String("count-keys", getEnv("REDIS_EXPORTER_COUNT_KEYS", ""), "Comma separated list of patterns to count (eg: 'db0=production_*,db3=sessions:*'), searched for with SCAN")
		checkKeysBatchSize   = flag.Int64("check-keys-batch-size", getEnvInt64("REDIS_EXPORTER_CHECK_KEYS_BATCH_SIZE", 1000), "Approximate number of keys to process in each execution, larger value speeds up scanning.\nWARNING: Still Redis is a single-threaded app, huge COUNT can affect production environment.")
		scriptPath           = flag.String("script", getEnv("REDIS_EXPORTER_SCRIPT", ""), "Path to Lua Redis script for collecting extra metrics")
		listenAddress        = flag.String("web.listen-address", getEnv("REDIS_EXPORTER_WEB_LISTEN_ADDRESS", ":9121"), "Address to listen on for web interface and telemetry.")
		metricPath           = flag.String("web.telemetry-path", getEnv("REDIS_EXPORTER_WEB_TELEMETRY_PATH", "/metrics"), "Path under which to expose metrics.")
		logFormat            = flag.String("log-format", getEnv("REDIS_EXPORTER_LOG_FORMAT", "txt"), "Log format, valid options are txt and json")
		configCommand        = flag.String("config-command", getEnv("REDIS_EXPORTER_CONFIG_COMMAND", "CONFIG"), "What to use for the CONFIG command")
		connectionTimeout    = flag.String("connection-timeout", getEnv("REDIS_EXPORTER_CONNECTION_TIMEOUT", "15s"), "Timeout for connection to Redis instance")
		tlsClientKeyFile     = flag.String("tls-client-key-file", getEnv("REDIS_EXPORTER_TLS_CLIENT_KEY_FILE", ""), "Name of the client key file (including full path) if the server requires TLS client authentication")
		tlsClientCertFile    = flag.String("tls-client-cert-file", getEnv("REDIS_EXPORTER_TLS_CLIENT_CERT_FILE", ""), "Name of the client certificate file (including full path) if the server requires TLS client authentication")
		tlsCaCertFile        = flag.String("tls-ca-cert-file", getEnv("REDIS_EXPORTER_TLS_CA_CERT_FILE", ""), "Name of the CA certificate file (including full path) if the server requires TLS client authentication")
		tlsServerKeyFile     = flag.String("tls-server-key-file", getEnv("REDIS_EXPORTER_TLS_SERVER_KEY_FILE", ""), "Name of the server key file (including full path) if the web interface and telemetry should use TLS")
		tlsServerCertFile    = flag.String("tls-server-cert-file", getEnv("REDIS_EXPORTER_TLS_SERVER_CERT_FILE", ""), "Name of the server certificate file (including full path) if the web interface and telemetry should use TLS")
		maxDistinctKeyGroups = flag.Int64("max-distinct-key-groups", getEnvInt64("REDIS_EXPORTER_MAX_DISTINCT_KEY_GROUPS", 100), "The maximum number of distinct key groups with the most memory utilization to present as distinct metrics per database, the leftover key groups will be aggregated in the 'overflow' bucket")
		isDebug              = flag.Bool("debug", getEnvBool("REDIS_EXPORTER_DEBUG", false), "Output verbose debug information")
		setClientName        = flag.Bool("set-client-name", getEnvBool("REDIS_EXPORTER_SET_CLIENT_NAME", true), "Whether to set client name to redis_exporter")
		isTile38             = flag.Bool("is-tile38", getEnvBool("REDIS_EXPORTER_IS_TILE38", false), "Whether to scrape Tile38 specific metrics")
		exportClientList     = flag.Bool("export-client-list", getEnvBool("REDIS_EXPORTER_EXPORT_CLIENT_LIST", false), "Whether to scrape Client List specific metrics")
		exportClientPort     = flag.Bool("export-client-port", getEnvBool("REDIS_EXPORTER_EXPORT_CLIENT_PORT", false), "Whether to include the client's port when exporting the client list. Warning: including the port increases the number of metrics generated and will make your Prometheus server take up more memory")
		showVersion          = flag.Bool("version", false, "Show version information and exit")
		redisMetricsOnly     = flag.Bool("redis-only-metrics", getEnvBool("REDIS_EXPORTER_REDIS_ONLY_METRICS", false), "Whether to also export go runtime metrics")
		pingOnConnect        = flag.Bool("ping-on-connect", getEnvBool("REDIS_EXPORTER_PING_ON_CONNECT", false), "Whether to ping the redis instance after connecting")
		inclSystemMetrics    = flag.Bool("include-system-metrics", getEnvBool("REDIS_EXPORTER_INCL_SYSTEM_METRICS", false), "Whether to include system metrics like e.g. redis_total_system_memory_bytes")
		skipTLSVerification  = flag.Bool("skip-tls-verification", getEnvBool("REDIS_EXPORTER_SKIP_TLS_VERIFICATION", false), "Whether to to skip TLS verification")
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

	passwordMap := make(map[string]string)
	if *redisPwd == "" && *redisPwdFile != "" {
		passwordMap, err = exporter.LoadPwdFile(*redisPwdFile)
		if err != nil {
			log.Fatalf("Error loading redis passwords from file %s, err: %s", *redisPwdFile, err)
		}
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

	exp, err := exporter.NewRedisExporter(
		*redisAddr,
		exporter.Options{
			User:                  *redisUser,
			Password:              *redisPwd,
			PasswordMap:           passwordMap,
			Namespace:             *namespace,
			ConfigCommandName:     *configCommand,
			CheckKeys:             *checkKeys,
			CheckSingleKeys:       *checkSingleKeys,
			CheckKeysBatchSize:    *checkKeysBatchSize,
			CheckKeyGroups:        *checkKeyGroups,
			MaxDistinctKeyGroups:  *maxDistinctKeyGroups,
			CheckStreams:          *checkStreams,
			CheckSingleStreams:    *checkSingleStreams,
			CountKeys:             *countKeys,
			LuaScript:             ls,
			InclSystemMetrics:     *inclSystemMetrics,
			SetClientName:         *setClientName,
			IsTile38:              *isTile38,
			ExportClientList:      *exportClientList,
			ExportClientsInclPort: *exportClientPort,
			SkipTLSVerification:   *skipTLSVerification,
			ClientCertFile:        *tlsClientCertFile,
			ClientKeyFile:         *tlsClientKeyFile,
			CaCertFile:            *tlsCaCertFile,
			ConnectionTimeouts:    to,
			MetricsPath:           *metricPath,
			RedisMetricsOnly:      *redisMetricsOnly,
			PingOnConnect:         *pingOnConnect,
			Registry:              registry,
			BuildInfo: exporter.BuildInfo{
				Version:   BuildVersion,
				CommitSha: BuildCommitSha,
				Date:      BuildDate,
			},
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	// Verify that initial client keypair and CA are accepted
	if (*tlsClientCertFile != "") != (*tlsClientKeyFile != "") {
		log.Fatal("TLS client key file and cert file should both be present")
	}
	_, err = exp.CreateClientTLSConfig()
	if err != nil {
		log.Fatal(err)
	}

	log.Infof("Providing metrics at %s%s", *listenAddress, *metricPath)
	log.Debugf("Configured redis addr: %#v", *redisAddr)
	if *tlsServerCertFile != "" && *tlsServerKeyFile != "" {
		log.Debugf("Bind as TLS using cert %s and key %s", *tlsServerCertFile, *tlsServerKeyFile)

		// Verify that the initial key pair is accepted
		_, err := exporter.LoadKeyPair(*tlsServerCertFile, *tlsServerKeyFile)
		if err != nil {
			log.Fatalf("Couldn't load TLS server key pair, err: %s", err)
		}
		server := &http.Server{
			Addr:      *listenAddress,
			TLSConfig: &tls.Config{GetCertificate: exporter.GetServerCertificateFunc(*tlsServerCertFile, *tlsServerKeyFile)},
			Handler:   exp}
		log.Fatal(server.ListenAndServeTLS("", ""))
	} else {
		log.Fatal(http.ListenAndServe(*listenAddress, exp))
	}
}
