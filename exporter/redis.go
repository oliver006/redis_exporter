package exporter

import (
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/garyburd/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
)

// RedisHost represents a set of Redis Hosts to health check.
type RedisHost struct {
	Addrs     []string
	Passwords []string
}

type dbKeyPair struct {
	db, key string
}

// Exporter implementes the prometheus.Exporter interface, and exports Redis metrics.
type Exporter struct {
	redis        RedisHost
	namespace    string
	keys         []dbKeyPair
	keyValues    *prometheus.GaugeVec
	keySizes     *prometheus.GaugeVec
	duration     prometheus.Gauge
	scrapeErrors prometheus.Gauge
	totalScrapes prometheus.Counter
	metrics      map[string]*prometheus.GaugeVec
	sync.RWMutex
}

type scrapeResult struct {
	Name  string
	Value float64
	Addr  string
	DB    string
}

var (
	renameMap = map[string]string{
		"loading": "repl_loading",
	}

	inclMap = map[string]bool{
		// # Server
		"uptime_in_seconds": true,

		// # Clients
		"connected_clients": true,
		"blocked_clients":   true,

		// # Memory
		"used_memory":             true,
		"used_memory_rss":         true,
		"used_memory_peak":        true,
		"used_memory_lua":         true,
		"total_system_memory":     true,
		"max_memory":              true,
		"mem_fragmentation_ratio": true,

		// # Persistence
		"rdb_changes_since_last_save":  true,
		"rdb_last_bgsave_time_sec":     true,
		"rdb_current_bgsave_time_sec":  true,
		"aof_enabled":                  true,
		"aof_rewrite_in_progress":      true,
		"aof_rewrite_scheduled":        true,
		"aof_last_rewrite_time_sec":    true,
		"aof_current_rewrite_time_sec": true,

		// # Stats
		"total_connections_received": true,
		"total_commands_processed":   true,
		"instantaneous_ops_per_sec":  true,
		"total_net_input_bytes":      true,
		"total_net_output_bytes":     true,
		"rejected_connections":       true,
		"expired_keys":               true,
		"evicted_keys":               true,
		"keyspace_hits":              true,
		"keyspace_misses":            true,
		"pubsub_channels":            true,
		"pubsub_patterns":            true,

		// # Replication
		"loading":           true,
		"connected_slaves":  true,
		"repl_backlog_size": true,

		// # CPU
		"used_cpu_sys":           true,
		"used_cpu_user":          true,
		"used_cpu_sys_children":  true,
		"used_cpu_user_children": true,
	}
)

func (e *Exporter) initGauges() {

	e.metrics = map[string]*prometheus.GaugeVec{}
	e.metrics["db_keys_total"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "db_keys_total",
		Help:      "Total number of keys by DB",
	}, []string{"addr", "db"})
	e.metrics["db_expiring_keys_total"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "db_expiring_keys_total",
		Help:      "Total number of expiring keys by DB",
	}, []string{"addr", "db"})
	e.metrics["db_avg_ttl_seconds"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "db_avg_ttl_seconds",
		Help:      "Avg TTL in seconds",
	}, []string{"addr", "db"})
}

// NewRedisExporter returns a new exporter of Redis metrics.
// note to self: next time we add an argument, instead add a RedisExporter struct
func NewRedisExporter(redis RedisHost, namespace, checkKeys string) (*Exporter, error) {
	for _, addr := range redis.Addrs {
		parts := strings.Split(addr, ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("Invalid parameter --redis.addr: %s", addr)
		}
	}

	e := Exporter{
		redis:     redis,
		namespace: namespace,
		keyValues: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "key_value",
			Help:      "The value of \"key\"",
		}, []string{"db", "key"}),
		keySizes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "key_size",
			Help:      "The length or size of \"key\"",
		}, []string{"db", "key"}),
		duration: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "exporter_last_scrape_duration_seconds",
			Help:      "The last scrape duration.",
		}),
		totalScrapes: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "exporter_scrapes_total",
			Help:      "Current total redis scrapes.",
		}),
		scrapeErrors: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "exporter_last_scrape_error",
			Help:      "The last scrape error status.",
		}),
	}
	var err error
	for _, k := range strings.Split(checkKeys, ",") {
		db := "0"
		key := ""
		frags := strings.Split(k, "=")
		switch len(frags) {
		case 1:
			db = "0"
			key, err = url.QueryUnescape(strings.TrimSpace(frags[0]))
		case 2:
			db = strings.Replace(strings.TrimSpace(frags[0]), "db", "", -1)
			key, err = url.QueryUnescape(strings.TrimSpace(frags[1]))
		default:
			err = fmt.Errorf("")
		}
		if err != nil {
			log.Printf("Couldn't parse db/key string: %s", k)
			continue
		}
		e.keys = append(e.keys, dbKeyPair{db, key})
	}
	e.initGauges()
	return &e, nil
}

// Describe outputs Redis metric descriptions.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {

	for _, m := range e.metrics {
		m.Describe(ch)
	}
	e.keySizes.Describe(ch)
	e.keyValues.Describe(ch)

	ch <- e.duration.Desc()
	ch <- e.totalScrapes.Desc()
	ch <- e.scrapeErrors.Desc()
}

// Collect fetches new metrics from the RedisHost and updates the appropriate metrics.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	scrapes := make(chan scrapeResult)

	e.Lock()
	defer e.Unlock()

	e.initGauges()
	go e.scrape(scrapes)
	e.setMetrics(scrapes)

	e.keySizes.Collect(ch)
	e.keyValues.Collect(ch)

	ch <- e.duration
	ch <- e.totalScrapes
	ch <- e.scrapeErrors
	e.collectMetrics(ch)
}

func includeMetric(name string) bool {

	if strings.HasPrefix(name, "db") {
		return true
	}

	_, ok := inclMap[name]

	return ok
}

/*
	valid example: db0:keys=1,expires=0,avg_ttl=0
*/
func parseDBKeyspaceString(db string, stats string) (keysTotal float64, keysExpiringTotal float64, avgTTL float64, ok bool) {
	ok = false
	if !strings.HasPrefix(db, "db") {
		return
	}

	split := strings.Split(stats, ",")
	if len(split) != 3 && len(split) != 2 {
		return
	}

	extract := func(s string) (val float64, err error) {
		split := strings.Split(s, "=")
		if len(split) != 2 {
			return 0, fmt.Errorf("nope")
		}
		val, err = strconv.ParseFloat(split[1], 64)
		if err != nil {
			return 0, fmt.Errorf("nope")
		}
		return
	}

	var err error
	ok = true
	if keysTotal, err = extract(split[0]); err != nil {
		ok = false
		return
	}
	if keysExpiringTotal, err = extract(split[1]); err != nil {
		ok = false
		return
	}

	avgTTL = -1
	if len(split) > 2 {
		if avgTTL, err = extract(split[2]); err != nil {
			ok = false
			return
		}
		avgTTL /= 1000
	}
	return
}

func extractInfoMetrics(info, addr string, scrapes chan<- scrapeResult) error {
	lines := strings.Split(info, "\r\n")
	for _, line := range lines {

		if (len(line) < 2) || line[0] == '#' || (!strings.Contains(line, ":")) {
			continue
		}
		split := strings.Split(line, ":")
		if len(split) != 2 || !includeMetric(split[0]) {
			continue
		}
		if keysTotal, keysEx, avgTTL, ok := parseDBKeyspaceString(split[0], split[1]); ok {
			scrapes <- scrapeResult{Name: "db_keys_total", Addr: addr, DB: split[0], Value: keysTotal}
			scrapes <- scrapeResult{Name: "db_expiring_keys_total", Addr: addr, DB: split[0], Value: keysEx}
			if avgTTL > -1 {
				scrapes <- scrapeResult{Name: "db_avg_ttl_seconds", Addr: addr, DB: split[0], Value: avgTTL}
			}
			continue
		}

		metricName := split[0]
		if newName, ok := renameMap[metricName]; ok {
			metricName = newName
		}

		val, err := strconv.ParseFloat(split[1], 64)
		if err != nil {
			log.Printf("couldn't parse %s, err: %s", split[1], err)
			continue
		}
		scrapes <- scrapeResult{Name: metricName, Addr: addr, Value: val}
	}
	return nil
}

func extractConfigMetrics(config []string, addr string, scrapes chan<- scrapeResult) error {

	if len(config)%2 != 0 {
		return fmt.Errorf("invalid config: %#v", config)
	}

	for pos := 0; pos < len(config)/2; pos++ {
		val, err := strconv.ParseFloat(config[pos*2+1], 64)
		if err != nil {
			log.Printf("couldn't parse %s, err: %s", config[pos*2+1], err)
			continue
		}
		scrapes <- scrapeResult{Name: fmt.Sprintf("config_%s", config[pos*2]), Addr: addr, Value: val}
	}
	return nil
}

func (e *Exporter) scrape(scrapes chan<- scrapeResult) {

	defer close(scrapes)
	now := time.Now().UnixNano()
	e.totalScrapes.Inc()

	errorCount := 0
	for idx, addr := range e.redis.Addrs {
		c, err := redis.Dial("tcp", addr)
		if err != nil {
			log.Printf("redis err: %s", err)
			errorCount++
			continue
		}
		defer c.Close()

		if len(e.redis.Passwords) > idx && e.redis.Passwords[idx] != "" {
			if _, err := c.Do("AUTH", e.redis.Passwords[idx]); err != nil {
				log.Printf("redis err: %s", err)
				errorCount++
				continue
			}
		}
		info, err := redis.String(c.Do("INFO"))
		if err == nil {
			err = extractInfoMetrics(info, addr, scrapes)
		}
		if err != nil {
			log.Printf("redis err: %s", err)
			errorCount++
		}

		config, err := redis.Strings(c.Do("CONFIG", "GET", "maxmemory"))
		if err == nil {
			err = extractConfigMetrics(config, addr, scrapes)
		}
		if err != nil {
			log.Printf("redis err: %s", err)
			errorCount++
		}

		for _, k := range e.keys {
			if _, err := c.Do("SELECT", k.db); err != nil {
				continue
			}
			if tempVal, err := c.Do("GET", k.key); err == nil && tempVal != nil {
				if val, err := strconv.ParseFloat(fmt.Sprintf("%s", tempVal), 64); err == nil {
					e.keyValues.WithLabelValues("db"+k.db, k.key).Set(val)
				}
			}

			for _, op := range []string{
				"HLEN",
				"LLEN",
				"SCARD",
				"ZCARD",
				"PFCOUNT",
				"STRLEN",
			} {
				if tempVal, err := c.Do(op, k.key); err == nil && tempVal != nil {
					e.keySizes.WithLabelValues("db"+k.db, k.key).Set(float64(tempVal.(int64)))
					break
				}
			}
		}
	}

	e.scrapeErrors.Set(float64(errorCount))
	e.duration.Set(float64(time.Now().UnixNano()-now) / 1000000000)
}

func (e *Exporter) setMetrics(scrapes <-chan scrapeResult) {

	for scr := range scrapes {
		name := scr.Name
		if _, ok := e.metrics[name]; !ok {
			e.metrics[name] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Namespace: e.namespace,
				Name:      name,
			}, []string{"addr"})
		}
		var labels prometheus.Labels = map[string]string{"addr": scr.Addr}
		if len(scr.DB) > 0 {
			labels["db"] = scr.DB
		}
		e.metrics[name].With(labels).Set(float64(scr.Value))
	}
}

func (e *Exporter) collectMetrics(metrics chan<- prometheus.Metric) {
	for _, m := range e.metrics {
		m.Collect(metrics)
	}
}
