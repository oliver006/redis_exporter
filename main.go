package main

import (
	"encoding/csv"
	"flag"
	"net/http"
	"os"
	"runtime"
	"strings"

	log "github.com/Sirupsen/logrus"
	"github.com/cloudfoundry-community/go-cfenv"
	"github.com/oliver006/redis_exporter/exporter"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	redisAddr     = flag.String("redis.addr", getEnv("REDIS_ADDR", ""), "Address of one or more redis nodes, separated by separator")
	redisFile     = flag.String("redis.file", getEnv("REDIS_FILE", ""), "Path to file containing one or more redis nodes, separated by newline. NOTE: mutually exclusive with redis.addr")
	redisPassword = flag.String("redis.password", getEnv("REDIS_PASSWORD", ""), "Password for one or more redis nodes, separated by separator")
	redisAlias    = flag.String("redis.alias", getEnv("REDIS_ALIAS", ""), "Redis instance alias for one or more redis nodes, separated by separator")
	namespace     = flag.String("namespace", "redis", "Namespace for metrics")
	checkKeys     = flag.String("check-keys", "", "Comma separated list of keys to export value and length/size")
	separator     = flag.String("separator", ",", "separator used to split redis.addr, redis.password and redis.alias into several elements.")
	listenAddress = flag.String("web.listen-address", ":9121", "Address to listen on for web interface and telemetry.")
	metricPath    = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
	isDebug       = flag.Bool("debug", false, "Output verbose debug information")
	logFormat     = flag.String("log-format", "txt", "Log format, valid options are txt and json")
	showVersion   = flag.Bool("version", false, "Show version information and exit")
	useCfBindings = flag.Bool("use-cf-bindings", false, "Use Cloud Foundry service bindings")
	addrs         []string
	passwords     []string
	aliases       []string

	// VERSION, BUILD_DATE, GIT_COMMIT are filled in by the build script
	VERSION     = "<<< filled in by build >>>"
	BUILD_DATE  = "<<< filled in by build >>>"
	COMMIT_SHA1 = "<<< filled in by build >>>"
)

func main() {
	flag.Parse()

	switch *logFormat {
	case "json":
		log.SetFormatter(&log.JSONFormatter{})
	default:
		log.SetFormatter(&log.TextFormatter{})
	}
	log.Printf("Redis Metrics Exporter %s    build date: %s    sha1: %s    Go: %s\n",
		VERSION, BUILD_DATE, COMMIT_SHA1,
		runtime.Version(),
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

	if *redisFile != "" && *redisAddr != "" {
		log.Fatal("Cannot specify both redis.addr and redis.file")
	}

	switch {
	case *redisFile != "":
		var err error
		addrs, passwords, aliases, err = loadRedisFile(*redisFile)
		if err != nil {
			log.Fatal(err)
		}
	case *useCfBindings:
		addrs, passwords, aliases = getCloudFoundryRedisBindings()
	default:
		addrs, passwords, aliases = loadRedisArgs(*redisAddr, *redisPassword, *redisAlias, *separator)
	}

	exp, err := exporter.NewRedisExporter(
		exporter.RedisHost{Addrs: addrs, Passwords: passwords, Aliases: aliases},
		*namespace,
		*checkKeys)
	if err != nil {
		log.Fatal(err)
	}
	prometheus.MustRegister(exp)

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "redis_exporter_build_info",
		Help: "redis exporter build_info",
	}, []string{"version", "commit_sha", "build_date", "golang_version"})
	prometheus.MustRegister(buildInfo)
	buildInfo.WithLabelValues(VERSION, COMMIT_SHA1, BUILD_DATE, runtime.Version()).Set(1)

	http.Handle(*metricPath, prometheus.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`
<html>
<head><title>Redis Exporter v` + VERSION + `</title></head>
<body>
<h1>Redis Exporter v` + VERSION + `</h1>
<p><a href='` + *metricPath + `'>Metrics</a></p>
</body>
</html>
						`))
	})

	log.Printf("Providing metrics at %s%s", *listenAddress, *metricPath)
	log.Printf("Connecting to redis hosts: %#v", addrs)
	log.Printf("Using alias: %#v", aliases)
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}

// loadRedisArgs loads the configuration for which redis hosts to monitor from either
// the environment or as passed from program arguments. Returns the list of host addrs,
// passwords, and their aliases.
func loadRedisArgs(addr, password, alias, separator string) ([]string, []string, []string) {
	if addr == "" {
		addr = "redis://localhost:6379"
	}
	addrs = strings.Split(addr, separator)
	passwords = strings.Split(password, separator)
	for len(passwords) < len(addrs) {
		passwords = append(passwords, passwords[0])
	}
	aliases = strings.Split(alias, separator)
	for len(aliases) < len(addrs) {
		aliases = append(aliases, aliases[0])
	}
	return addrs, passwords, aliases
}

// loadRedisFile opens the specified file and loads the configuration for which redis
// hosts to monitor. Returns the list of hosts addrs, passwords, and their aliases.
func loadRedisFile(fileName string) ([]string, []string, []string, error) {
	var addrs []string
	var passwords []string
	var aliases []string
	file, err := os.Open(fileName)
	if err != nil {
		return nil, nil, nil, err
	}
	r := csv.NewReader(file)
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, nil, nil, err
	}
	file.Close()
	// For each line, test if it contains an optional password and alias and provide them,
	// else give them empty strings
	for _, record := range records {
		length := len(record)
		switch length {
		case 3:
			addrs = append(addrs, record[0])
			passwords = append(passwords, record[1])
			aliases = append(aliases, record[2])
		case 2:
			addrs = append(addrs, record[0])
			passwords = append(passwords, record[1])
			aliases = append(aliases, "")
		case 1:
			addrs = append(addrs, record[0])
			passwords = append(passwords, "")
			aliases = append(aliases, "")
		}
	}
	return addrs, passwords, aliases, nil
}

// getEnv gets an environment variable from a given key and if it doesn't exist,
// returns defaultVal given.
func getEnv(key string, defaultVal string) string {
	if envVal, ok := os.LookupEnv(key); ok {
		return envVal
	}
	return defaultVal
}

func getCloudFoundryRedisBindings() (addrs, passwords, aliases []string) {
	if !cfenv.IsRunningOnCF() {
		return
	}

	appEnv, err := cfenv.Current()
	if err != nil {
		log.Warnln("Unable to get current CF environment", err)
		return
	}

	redisServices, err := appEnv.Services.WithTag("redis")
	if err != nil {
		log.Warnln("Error while getting redis services", err)
		return
	}

	for _, redisService := range redisServices {
		credentials := redisService.Credentials
		addr := credentials["hostname"].(string) + ":" + credentials["port"].(string)
		password := credentials["password"].(string)
		alias := redisService.Name

		addrs = append(addrs, addr)
		passwords = append(passwords, password)
		aliases = append(aliases, alias)
	}

	return
}
