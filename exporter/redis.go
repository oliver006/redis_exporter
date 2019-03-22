package exporter

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	prom_strutil "github.com/prometheus/prometheus/util/strutil"
	log "github.com/sirupsen/logrus"
)

// RedisHost represents a set of Redis Hosts to health check.
type RedisHost struct {
	Addrs     []string
	Passwords []string
	Aliases   []string
}

type dbKeyPair struct {
	db, key string
}

type keyInfo struct {
	size    float64
	keyType string
}

// Exporter implements the prometheus.Exporter interface, and exports Redis metrics.
type Exporter struct {
	redis        RedisHost
	namespace    string
	keys         []dbKeyPair
	singleKeys   []dbKeyPair
	keyValues    *prometheus.GaugeVec
	keySizes     *prometheus.GaugeVec
	scriptValues *prometheus.GaugeVec
	duration     prometheus.Gauge
	scrapeErrors prometheus.Gauge
	totalScrapes prometheus.Counter
	metrics      map[string]*prometheus.GaugeVec

	LuaScript []byte

	metricsMtx sync.RWMutex
	sync.RWMutex
}

type scrapeResult struct {
	Name  string
	Value float64
	Addr  string
	Alias string
	DB    string
}

var (
	metricMap = map[string]string{
		// # Server
		"uptime_in_seconds": "uptime_in_seconds",
		"process_id":        "process_id",

		// # Clients
		"connected_clients":          "connected_clients",
		"client_longest_output_list": "client_longest_output_list",
		"client_biggest_input_buf":   "client_biggest_input_buf",
		"blocked_clients":            "blocked_clients",

		// # Memory
		"allocator_active":    "allocator_active_bytes",
		"allocator_allocated": "allocator_allocated_bytes",
		"allocator_resident":  "allocator_resident_bytes",
		"used_memory":         "memory_used_bytes",
		"used_memory_rss":     "memory_used_rss_bytes",
		"used_memory_peak":    "memory_used_peak_bytes",
		"used_memory_lua":     "memory_used_lua_bytes",
		"total_system_memory": "total_system_memory_bytes",
		"maxmemory":           "memory_max_bytes",

		// # Persistence
		"rdb_changes_since_last_save":  "rdb_changes_since_last_save",
		"rdb_bgsave_in_progress":       "rdb_bgsave_in_progress",
		"rdb_last_save_time":           "rdb_last_save_timestamp_seconds",
		"rdb_last_bgsave_status":       "rdb_last_bgsave_status",
		"rdb_last_bgsave_time_sec":     "rdb_last_bgsave_duration_sec",
		"rdb_current_bgsave_time_sec":  "rdb_current_bgsave_duration_sec",
		"rdb_last_cow_size":            "rdb_last_cow_size_bytes",
		"aof_enabled":                  "aof_enabled",
		"aof_rewrite_in_progress":      "aof_rewrite_in_progress",
		"aof_rewrite_scheduled":        "aof_rewrite_scheduled",
		"aof_last_rewrite_time_sec":    "aof_last_rewrite_duration_sec",
		"aof_current_rewrite_time_sec": "aof_current_rewrite_duration_sec",
		"aof_last_cow_size":            "aof_last_cow_size_bytes",
		"aof_current_size":             "aof_current_size_bytes",
		"aof_base_size":                "aof_base_size_bytes",
		"aof_pending_rewrite":          "aof_pending_rewrite",
		"aof_buffer_length":            "aof_buffer_length",
		"aof_rewrite_buffer_length":    "aof_rewrite_buffer_length",
		"aof_pending_bio_fsync":        "aof_pending_bio_fsync",
		"aof_delayed_fsync":            "aof_delayed_fsync",
		"aof_last_bgrewrite_status":    "aof_last_bgrewrite_status",
		"aof_last_write_status":        "aof_last_write_status",

		// # Stats
		"total_connections_received": "connections_received_total",
		"total_commands_processed":   "commands_processed_total",
		"instantaneous_ops_per_sec":  "instantaneous_ops_per_sec",
		"total_net_input_bytes":      "net_input_bytes_total",
		"total_net_output_bytes":     "net_output_bytes_total",
		"instantaneous_input_kbps":   "instantaneous_input_kbps",
		"instantaneous_output_kbps":  "instantaneous_output_kbps",
		"rejected_connections":       "rejected_connections_total",
		"expired_keys":               "expired_keys_total",
		"evicted_keys":               "evicted_keys_total",
		"keyspace_hits":              "keyspace_hits_total",
		"keyspace_misses":            "keyspace_misses_total",
		"pubsub_channels":            "pubsub_channels",
		"pubsub_patterns":            "pubsub_patterns",
		"latest_fork_usec":           "latest_fork_usec",

		// # Replication
		"loading":                    "loading_dump_file",
		"connected_slaves":           "connected_slaves",
		"repl_backlog_size":          "replication_backlog_bytes",
		"master_last_io_seconds_ago": "master_last_io_seconds",
		"master_repl_offset":         "master_repl_offset",

		// # CPU
		"used_cpu_sys":           "used_cpu_sys",
		"used_cpu_user":          "used_cpu_user",
		"used_cpu_sys_children":  "used_cpu_sys_children",
		"used_cpu_user_children": "used_cpu_user_children",

		// # Cluster
		"cluster_stats_messages_sent":     "cluster_messages_sent_total",
		"cluster_stats_messages_received": "cluster_messages_received_total",

		// # Tile38
		// based on https://tile38.com/commands/server/
		"aof_size":        "aof_size_bytes",
		"avg_item_size":   "avg_item_size_bytes",
		"cpus":            "cpus_total",
		"heap_released":   "heap_released_bytes",
		"heap_size":       "heap_size_bytes",
		"http_transport":  "http_transport",
		"in_memory_size":  "in_memory_size_bytes",
		"max_heap_size":   "max_heap_size_bytes",
		"mem_alloc":       "mem_alloc_bytes",
		"num_collections": "num_collections_total",
		"num_hooks":       "num_hooks_total",
		"num_objects":     "num_objects_total",
		"num_points":      "num_points_total",
		"pointer_size":    "pointer_size_bytes",
		"read_only":       "read_only",
		"threads":         "threads_total",
		"version":         "version", // since tile38 version 1.14.1
	}

	instanceInfoFields = map[string]bool{"role": true, "redis_version": true, "redis_build_id": true, "redis_mode": true, "os": true}
	slaveInfoFields    = map[string]bool{"master_host": true, "master_port": true, "slave_read_only": true}
)

func (e *Exporter) initGauges() {

	e.metrics = map[string]*prometheus.GaugeVec{}
	e.metrics["instance_info"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "instance_info",
		Help:      "Information about the Redis instance",
	}, []string{"addr", "alias", "role", "redis_version", "redis_build_id", "redis_mode", "os"})
	e.metrics["slave_info"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "slave_info",
		Help:      "Information about the Redis slave",
	}, []string{"addr", "alias", "master_host", "master_port", "read_only"})
	e.metrics["start_time_seconds"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "start_time_seconds",
		Help:      "Start time of the Redis instance since unix epoch in seconds.",
	}, []string{"addr", "alias"})
	e.metrics["master_link_up"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "master_link_up",
		Help:      "Master link status on Redis slave",
	}, []string{"addr", "alias"})
	e.metrics["connected_slave_offset"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "connected_slave_offset",
		Help:      "Offset of connected slave",
	}, []string{"addr", "alias", "slave_ip", "slave_port", "slave_state"})
	e.metrics["connected_slave_lag_seconds"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "connected_slave_lag_seconds",
		Help:      "Lag of connected slave",
	}, []string{"addr", "alias", "slave_ip", "slave_port", "slave_state"})
	e.metrics["db_keys"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "db_keys",
		Help:      "Total number of keys by DB",
	}, []string{"addr", "alias", "db"})
	e.metrics["db_keys_expiring"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "db_keys_expiring",
		Help:      "Total number of expiring keys by DB",
	}, []string{"addr", "alias", "db"})
	e.metrics["db_avg_ttl_seconds"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "db_avg_ttl_seconds",
		Help:      "Avg TTL in seconds",
	}, []string{"addr", "alias", "db"})

	// Latency info
	e.metrics["latency_spike_last"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "latency_spike_last",
		Help:      "When the latency spike last occurred",
	}, []string{"addr", "alias", "event_name"})
	e.metrics["latency_spike_milliseconds"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "latency_spike_milliseconds",
		Help:      "Length of the last latency spike in milliseconds",
	}, []string{"addr", "alias", "event_name"})

	e.metrics["commands_total"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "commands_total",
		Help:      "Total number of calls per command",
	}, []string{"addr", "alias", "cmd"})
	e.metrics["commands_duration_seconds_total"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "commands_duration_seconds_total",
		Help:      "Total amount of time in seconds spent per command",
	}, []string{"addr", "alias", "cmd"})
	e.metrics["slowlog_length"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "slowlog_length",
		Help:      "Total slowlog",
	}, []string{"addr", "alias"})
	e.metrics["slowlog_last_id"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "slowlog_last_id",
		Help:      "Last id of slowlog",
	}, []string{"addr", "alias"})
	e.metrics["last_slow_execution_duration_seconds"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "last_slow_execution_duration_seconds",
		Help:      "The amount of time needed for last slow execution, in seconds",
	}, []string{"addr", "alias"})
}

// splitKeyArgs splits a command-line supplied argument into a slice of dbKeyPairs.
func parseKeyArg(keysArgString string) (keys []dbKeyPair, err error) {
	if keysArgString == "" {
		return keys, err
	}
	for _, k := range strings.Split(keysArgString, ",") {
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
			return keys, fmt.Errorf("invalid key list argument: %s", k)
		}
		if err != nil {
			return keys, fmt.Errorf("couldn't parse db/key string: %s", k)
		}

		keys = append(keys, dbKeyPair{db, key})
	}
	return keys, err
}

// NewRedisExporter returns a new exporter of Redis metrics.
// note to self: next time we add an argument, instead add a RedisExporter struct
func NewRedisExporter(host RedisHost, namespace, checkSingleKeys, checkKeys string) (*Exporter, error) {

	e := Exporter{
		redis:     host,
		namespace: namespace,
		keyValues: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "key_value",
			Help:      "The value of \"key\"",
		}, []string{"addr", "alias", "db", "key"}),
		keySizes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "key_size",
			Help:      "The length or size of \"key\"",
		}, []string{"addr", "alias", "db", "key"}),
		scriptValues: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "script_value",
			Help:      "Values returned by the collect script",
		}, []string{"addr", "alias", "key"}),
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

	if e.keys, err = parseKeyArg(checkKeys); err != nil {
		return &e, fmt.Errorf("Couldn't parse check-keys: %#v", err)
	}
	log.Debugf("keys: %#v", e.keys)

	if e.singleKeys, err = parseKeyArg(checkSingleKeys); err != nil {
		return &e, fmt.Errorf("Couldn't parse check-single-keys: %#v", err)
	}
	log.Debugf("singleKeys: %#v", e.singleKeys)

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

	e.keySizes.Reset()
	e.keyValues.Reset()

	e.initGauges()
	go e.scrape(scrapes)
	e.setMetrics(scrapes)

	e.keySizes.Collect(ch)
	e.keyValues.Collect(ch)
	e.scriptValues.Collect(ch)

	ch <- e.duration
	ch <- e.totalScrapes
	ch <- e.scrapeErrors
	e.collectMetrics(ch)
}

func includeMetric(s string) bool {
	if strings.HasPrefix(s, "db") || strings.HasPrefix(s, "cmdstat_") || strings.HasPrefix(s, "cluster_") {
		return true
	}
	_, ok := metricMap[s]
	return ok
}

func sanitizeMetricName(n string) string {
	return prom_strutil.SanitizeLabelName(n)
}

func extractVal(s string) (val float64, err error) {
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

	var err error
	ok = true
	if keysTotal, err = extractVal(split[0]); err != nil {
		ok = false
		return
	}
	if keysExpiringTotal, err = extractVal(split[1]); err != nil {
		ok = false
		return
	}

	avgTTL = -1
	if len(split) > 2 {
		if avgTTL, err = extractVal(split[2]); err != nil {
			ok = false
			return
		}
		avgTTL /= 1000
	}
	return
}

/*
	slave0:ip=10.254.11.1,port=6379,state=online,offset=1751844676,lag=0
	slave1:ip=10.254.11.2,port=6379,state=online,offset=1751844222,lag=0
*/
func parseConnectedSlaveString(slaveName string, slaveInfo string) (offset float64, ip string, port string, state string, lag float64, ok bool) {
	ok = false
	if matched, _ := regexp.MatchString(`^slave\d+`, slaveName); !matched {
		return
	}
	connectedSlaveInfo := make(map[string]string)
	for _, kvPart := range strings.Split(slaveInfo, ",") {
		x := strings.Split(kvPart, "=")
		if len(x) != 2 {
			log.Debugf("Invalid format for connected slave string, got: %s", kvPart)
			return
		}
		connectedSlaveInfo[x[0]] = x[1]
	}
	offset, err := strconv.ParseFloat(connectedSlaveInfo["offset"], 64)
	if err != nil {
		log.Debugf("Can not parse connected slave offset, got: %s", connectedSlaveInfo["offset"])
		return
	}

	if lagStr, exists := connectedSlaveInfo["lag"]; exists == false {
		// Prior to 3.0, "lag" property does not exist
		lag = -1
	} else {
		lag, err = strconv.ParseFloat(lagStr, 64)
		if err != nil {
			log.Debugf("Can not parse connected slave lag, got: %s", lagStr)
			return
		}
	}

	ok = true
	ip = connectedSlaveInfo["ip"]
	port = connectedSlaveInfo["port"]
	state = connectedSlaveInfo["state"]

	return
}

func extractConfigMetrics(config []string, addr string, alias string, scrapes chan<- scrapeResult) (dbCount int, err error) {
	if len(config)%2 != 0 {
		return 0, fmt.Errorf("invalid config: %#v", config)
	}

	for pos := 0; pos < len(config)/2; pos++ {
		strKey := config[pos*2]
		strVal := config[pos*2+1]

		if strKey == "databases" {
			if dbCount, err = strconv.Atoi(strVal); err != nil {
				return 0, fmt.Errorf("invalid config value for key databases: %#v", strVal)
			}
		}

		// todo: we can add more configs to this map if there's interest
		if !map[string]bool{
			"maxmemory":  true,
			"maxclients": true,
		}[strKey] {
			continue
		}

		if val, err := strconv.ParseFloat(strVal, 64); err == nil {
			scrapes <- scrapeResult{Name: fmt.Sprintf("config_%s", config[pos*2]), Addr: addr, Alias: alias, Value: val}
		}
	}
	return
}

func (e *Exporter) extractTile38Metrics(info []string, addr string, alias string, scrapes chan<- scrapeResult) error {
	for i := 0; i < len(info); i += 2 {
		log.Debugf("tile38: %s:%s", info[i], info[i+1])

		fieldKey := info[i]
		fieldValue := info[i+1]

		if !includeMetric(fieldKey) {
			continue
		}

		registerMetric(addr, alias, fieldKey, fieldValue, scrapes)
	}

	return nil
}

func (e *Exporter) handleMetricsCommandStats(addr string, alias string, fieldKey string, fieldValue string) {
	/*
		Format:
		cmdstat_get:calls=21,usec=175,usec_per_call=8.33
		cmdstat_set:calls=61,usec=3139,usec_per_call=51.46
		cmdstat_setex:calls=75,usec=1260,usec_per_call=16.80
	*/
	splitKey := strings.Split(fieldKey, "_")
	if len(splitKey) != 2 {
		return
	}

	splitValue := strings.Split(fieldValue, ",")
	if len(splitValue) != 3 {
		return
	}

	var calls float64
	var usecTotal float64
	var err error
	if calls, err = extractVal(splitValue[0]); err != nil {
		return
	}
	if usecTotal, err = extractVal(splitValue[1]); err != nil {
		return
	}

	e.metricsMtx.RLock()
	defer e.metricsMtx.RUnlock()

	cmd := splitKey[1]
	e.metrics["commands_total"].WithLabelValues(addr, alias, cmd).Set(calls)
	e.metrics["commands_duration_seconds_total"].WithLabelValues(addr, alias, cmd).Set(usecTotal / 1e6)
}

func (e *Exporter) handleMetricsReplication(addr string, alias string, fieldKey string, fieldValue string) bool {
	e.metricsMtx.RLock()
	defer e.metricsMtx.RUnlock()

	// only slaves have this field
	if fieldKey == "master_link_status" {
		if fieldValue == "up" {
			e.metrics["master_link_up"].WithLabelValues(addr, alias).Set(1)
		} else {
			e.metrics["master_link_up"].WithLabelValues(addr, alias).Set(0)
		}
		return true
	}

	// not a slave, try extracting master metrics
	if slaveOffset, slaveIp, slavePort, slaveState, lag, ok := parseConnectedSlaveString(fieldKey, fieldValue); ok {
		e.metrics["connected_slave_offset"].WithLabelValues(
			addr,
			alias,
			slaveIp,
			slavePort,
			slaveState,
		).Set(slaveOffset)

		if lag > -1 {
			e.metrics["connected_slave_lag_seconds"].WithLabelValues(
				addr,
				alias,
				slaveIp,
				slavePort,
				slaveState,
			).Set(lag)
		}
		return true
	}

	return false
}

func (e *Exporter) handleMetricsServer(addr string, alias string, fieldKey string, fieldValue string) {
	if fieldKey == "uptime_in_seconds" {
		if uptime, err := strconv.ParseFloat(fieldValue, 64); err == nil {
			e.metricsMtx.RLock()
			e.metrics["start_time_seconds"].WithLabelValues(addr, alias).Set(float64(time.Now().Unix()) - uptime)
			e.metricsMtx.RUnlock()
		}
	}
}

func (e *Exporter) extractInfoMetrics(info, addr string, alias string, scrapes chan<- scrapeResult, dbCount int) error {
	instanceInfo := map[string]string{}
	slaveInfo := map[string]string{}
	handledDBs := map[string]bool{}

	fieldClass := ""
	lines := strings.Split(info, "\r\n")
	for _, line := range lines {
		log.Debugf("info: %s", line)
		if len(line) > 0 && line[0] == '#' {
			fieldClass = line[2:]
			continue
		}

		if (len(line) < 2) || (!strings.Contains(line, ":")) {
			continue
		}

		split := strings.SplitN(line, ":", 2)
		fieldKey := split[0]
		fieldValue := split[1]

		if _, ok := instanceInfoFields[fieldKey]; ok {
			instanceInfo[fieldKey] = fieldValue
			continue
		}

		if _, ok := slaveInfoFields[fieldKey]; ok {
			slaveInfo[fieldKey] = fieldValue
			continue
		}

		switch fieldClass {

		case "Replication":
			if ok := e.handleMetricsReplication(addr, alias, fieldKey, fieldValue); ok {
				continue
			}

		case "Server":
			e.handleMetricsServer(addr, alias, fieldKey, fieldValue)

		case "Commandstats":
			e.handleMetricsCommandStats(addr, alias, fieldKey, fieldValue)
			continue

		case "Keyspace":
			if keysTotal, keysEx, avgTTL, ok := parseDBKeyspaceString(fieldKey, fieldValue); ok {
				dbName := fieldKey
				scrapes <- scrapeResult{Name: "db_keys", Addr: addr, Alias: alias, DB: dbName, Value: keysTotal}
				scrapes <- scrapeResult{Name: "db_keys_expiring", Addr: addr, Alias: alias, DB: dbName, Value: keysEx}
				if avgTTL > -1 {
					scrapes <- scrapeResult{Name: "db_avg_ttl_seconds", Addr: addr, Alias: alias, DB: dbName, Value: avgTTL}
				}
				handledDBs[dbName] = true
				continue
			}
		}

		if !includeMetric(fieldKey) {
			continue
		}

		registerMetric(addr, alias, fieldKey, fieldValue, scrapes)
	}

	for dbIndex := 0; dbIndex < dbCount; dbIndex++ {
		dbName := "db" + strconv.Itoa(dbIndex)
		if _, exists := handledDBs[dbName]; !exists {
			scrapes <- scrapeResult{Name: "db_keys", Addr: addr, Alias: alias, DB: dbName, Value: 0}
			scrapes <- scrapeResult{Name: "db_keys_expiring", Addr: addr, Alias: alias, DB: dbName, Value: 0}
		}
	}

	e.metricsMtx.RLock()
	e.metrics["instance_info"].WithLabelValues(
		addr, alias,
		instanceInfo["role"],
		instanceInfo["redis_version"],
		instanceInfo["redis_build_id"],
		instanceInfo["redis_mode"],
		instanceInfo["os"],
	).Set(1)
	if instanceInfo["role"] == "slave" {
		e.metrics["slave_info"].WithLabelValues(
			addr, alias,
			slaveInfo["master_host"],
			slaveInfo["master_port"],
			slaveInfo["slave_read_only"],
		).Set(1)
	}
	e.metricsMtx.RUnlock()

	return nil
}

func (e *Exporter) extractClusterInfoMetrics(info, addr, alias string, scrapes chan<- scrapeResult) error {
	lines := strings.Split(info, "\r\n")

	for _, line := range lines {
		log.Debugf("info: %s", line)

		split := strings.Split(line, ":")
		if len(split) != 2 {
			continue
		}
		fieldKey := split[0]
		fieldValue := split[1]

		if !includeMetric(fieldKey) {
			continue
		}

		registerMetric(addr, alias, fieldKey, fieldValue, scrapes)
	}

	return nil
}

func registerMetric(addr, alias, fieldKey, fieldValue string, scrapes chan<- scrapeResult) error {
	metricName := sanitizeMetricName(fieldKey)
	if newName, ok := metricMap[metricName]; ok {
		metricName = newName
	}

	var err error
	var val float64

	switch fieldValue {

	case "ok":
		val = 1

	case "err", "fail":
		val = 0

	default:
		val, err = strconv.ParseFloat(fieldValue, 64)

	}
	if err != nil {
		log.Debugf("couldn't parse %s, err: %s", fieldValue, err)
	}

	scrapes <- scrapeResult{Name: metricName, Addr: addr, Alias: alias, Value: val}

	return nil
}

func doRedisCmd(c redis.Conn, cmd string, args ...interface{}) (reply interface{}, err error) {
	log.Debugf("c.Do() - running command: %s %s", cmd, args)
	defer log.Debugf("c.Do() - done")
	res, err := c.Do(cmd, args...)
	if err != nil {
		log.Debugf("c.Do() - err: %s", err)
	}
	return res, err
}

var errNotFound = errors.New("key not found")

// getKeyInfo takes a key and returns the type, and the size or length of the value stored at that key.
func getKeyInfo(c redis.Conn, key string) (info keyInfo, err error) {

	if info.keyType, err = redis.String(c.Do("TYPE", key)); err != nil {
		return info, err
	}

	switch info.keyType {
	case "none":
		return info, errNotFound
	case "string":
		if size, err := redis.Int64(c.Do("PFCOUNT", key)); err == nil {
			info.keyType = "hyperloglog"
			info.size = float64(size)
		} else if size, err := redis.Int64(c.Do("STRLEN", key)); err == nil {
			info.size = float64(size)
		}
	case "list":
		if size, err := redis.Int64(c.Do("LLEN", key)); err == nil {
			info.size = float64(size)
		}
	case "set":
		if size, err := redis.Int64(c.Do("SCARD", key)); err == nil {
			info.size = float64(size)
		}
	case "zset":
		if size, err := redis.Int64(c.Do("ZCARD", key)); err == nil {
			info.size = float64(size)
		}
	case "hash":
		if size, err := redis.Int64(c.Do("HLEN", key)); err == nil {
			info.size = float64(size)
		}
	case "stream":
		if size, err := redis.Int64(c.Do("XLEN", key)); err == nil {
			info.size = float64(size)
		}
	default:
		err = fmt.Errorf("Unknown type: %v for key: %v", info.keyType, key)
	}

	return info, err
}

// scanForKeys returns a list of keys matching `pattern` by using `SCAN`, which is safer for production systems than using `KEYS`.
// This function was adapted from: https://github.com/reisinger/examples-redigo
func scanForKeys(c redis.Conn, pattern string) ([]string, error) {
	iter := 0
	keys := []string{}

	for {
		arr, err := redis.Values(c.Do("SCAN", iter, "MATCH", pattern))
		if err != nil {
			return keys, fmt.Errorf("error retrieving '%s' keys err: %s", pattern, err)
		}
		if len(arr) != 2 {
			return keys, fmt.Errorf("invalid response from SCAN for pattern: %s", pattern)
		}

		k, _ := redis.Strings(arr[1], nil)
		keys = append(keys, k...)

		if iter, _ = redis.Int(arr[0], nil); iter == 0 {
			break
		}
	}

	return keys, nil
}

// getKeysFromPatterns does a SCAN for a key if the key contains pattern characters
func getKeysFromPatterns(c redis.Conn, keys []dbKeyPair) (expandedKeys []dbKeyPair, err error) {
	expandedKeys = []dbKeyPair{}
	for _, k := range keys {
		if regexp.MustCompile(`[\?\*\[\]\^]+`).MatchString(k.key) {
			_, err := c.Do("SELECT", k.db)
			if err != nil {
				return expandedKeys, err
			}
			keyNames, err := scanForKeys(c, k.key)
			if err != nil {
				log.Errorf("error with SCAN for pattern: %#v err: %s", k.key, err)
				continue
			}
			for _, keyName := range keyNames {
				expandedKeys = append(expandedKeys, dbKeyPair{db: k.db, key: keyName})
			}
		} else {
			expandedKeys = append(expandedKeys, k)
		}
	}
	return expandedKeys, err
}

func (e *Exporter) scrapeRedisHost(scrapes chan<- scrapeResult, addr string, idx int) error {
	options := []redis.DialOption{
		redis.DialConnectTimeout(5 * time.Second),
		redis.DialReadTimeout(5 * time.Second),
		redis.DialWriteTimeout(5 * time.Second),
	}

	if len(e.redis.Passwords) > idx && e.redis.Passwords[idx] != "" {
		options = append(options, redis.DialPassword(e.redis.Passwords[idx]))
	}

	log.Debugf("Trying DialURL(): %s", addr)
	c, err := redis.DialURL(addr, options...)

	if err != nil {
		log.Debugf("DialURL() failed, err: %s", err)
		if frags := strings.Split(addr, "://"); len(frags) == 2 {
			log.Debugf("Trying: Dial(): %s %s", frags[0], frags[1])
			c, err = redis.Dial(frags[0], frags[1], options...)
		} else {
			log.Debugf("Trying: Dial(): tcp %s", addr)
			c, err = redis.Dial("tcp", addr, options...)
		}
	}

	if err != nil {
		log.Debugf("aborting for addr: %s - redis err: %s", addr, err)
		return err
	}

	defer c.Close()
	log.Debugf("connected to: %s", addr)

	dbCount := 0

	if config, err := redis.Strings(c.Do("CONFIG", "GET", "*")); err == nil {
		dbCount, err = extractConfigMetrics(config, addr, e.redis.Aliases[idx], scrapes)
		if err != nil {
			log.Errorf("Redis CONFIG err: %s", err)
			return err
		}
	} else {
		log.Debugf("Redis CONFIG err: %s", err)
	}

	infoAll, err := redis.String(doRedisCmd(c, "INFO", "ALL"))
	if err != nil {
		log.Errorf("Redis INFO err: %s", err)
		return err
	}
	isClusterEnabled := strings.Contains(infoAll, "cluster_enabled:1")

	if isClusterEnabled {
		if clusterInfo, err := redis.String(doRedisCmd(c, "CLUSTER", "INFO")); err == nil {
			e.extractClusterInfoMetrics(clusterInfo, addr, e.redis.Aliases[idx], scrapes)

			// in cluster mode Redis only supports one database so no extra padding beyond that needed
			dbCount = 1
		} else {
			log.Errorf("Redis CLUSTER INFO err: %s", err)
		}
	} else {
		// in non-cluster mode, if dbCount is zero then "CONFIG" failed to retrieve a valid
		// number of databases and we use the Redis config default which is 16
		if dbCount == 0 {
			dbCount = 16
		}
	}

	e.extractInfoMetrics(infoAll, addr, e.redis.Aliases[idx], scrapes, dbCount)

	// SERVER command only works on tile38 database. check the following link to
	// find out more: https://tile38.com/
	if serverInfo, err := redis.Strings(doRedisCmd(c, "SERVER")); err == nil {
		e.extractTile38Metrics(serverInfo, addr, e.redis.Aliases[idx], scrapes)
	} else {
		log.Debugf("Tile38 SERVER err: %s", err)
	}

	if reply, err := doRedisCmd(c, "LATENCY", "LATEST"); err == nil {
		var eventName string
		var spikeLast, milliseconds, max int64
		if tempVal, _ := reply.([]interface{}); len(tempVal) > 0 {
			latencyResult := tempVal[0].([]interface{})
			if _, err := redis.Scan(latencyResult, &eventName, &spikeLast, &milliseconds, &max); err == nil {
				e.metricsMtx.RLock()
				e.metrics["latency_spike_last"].WithLabelValues(addr, e.redis.Aliases[idx], eventName).Set(float64(spikeLast))
				e.metrics["latency_spike_milliseconds"].WithLabelValues(addr, e.redis.Aliases[idx], eventName).Set(float64(milliseconds))
				e.metricsMtx.RUnlock()
			}
		}
	}

	log.Debugf("e.singleKeys: %#v", e.singleKeys)
	allKeys := append([]dbKeyPair{}, e.singleKeys...)

	log.Debugf("e.keys: %#v", e.keys)
	scannedKeys, err := getKeysFromPatterns(c, e.keys)
	if err != nil {
		log.Errorf("Error expanding key patterns: %#v", err)
	} else {
		allKeys = append(allKeys, scannedKeys...)
	}

	log.Debugf("allKeys: %#v", allKeys)
	for _, k := range allKeys {
		if _, err := doRedisCmd(c, "SELECT", k.db); err != nil {
			log.Debugf("Couldn't select database %#v when getting key info.", k.db)
			continue
		}

		info, err := getKeyInfo(c, k.key)
		if err != nil {
			switch err {
			case errNotFound:
				log.Debugf("Key '%s' not found when trying to get type and size.", k.key)
			default:
				log.Error(err)
			}
			continue
		}
		dbLabel := "db" + k.db
		e.keySizes.WithLabelValues(addr, e.redis.Aliases[idx], dbLabel, k.key).Set(info.size)

		// Only record value metric if value is float-y
		if value, err := redis.Float64(c.Do("GET", k.key)); err == nil {
			e.keyValues.WithLabelValues(addr, e.redis.Aliases[idx], dbLabel, k.key).Set(value)
		}
	}

	if e.LuaScript != nil && len(e.LuaScript) > 0 {
		log.Debug("e.script")
		kv, err := redis.StringMap(doRedisCmd(c, "EVAL", e.LuaScript, 0, 0))
		if err != nil {
			log.Errorf("Collect script error: %v", err)
		} else if kv != nil {
			for key, stringVal := range kv {
				if val, err := strconv.ParseFloat(stringVal, 64); err == nil {
					e.scriptValues.WithLabelValues(addr, e.redis.Aliases[idx], key).Set(val)
				}
			}
		}
	}

	if reply, err := c.Do("SLOWLOG", "LEN"); err == nil {
		e.metricsMtx.RLock()
		e.metrics["slowlog_length"].WithLabelValues(addr, e.redis.Aliases[idx]).Set(float64(reply.(int64)))
		e.metricsMtx.RUnlock()
	}

	if values, err := redis.Values(c.Do("SLOWLOG", "GET", "1")); err == nil {
		var slowlogLastId int64 = 0
		var lastSlowExecutionDurationSeconds float64 = 0

		if len(values) > 0 {
			if values, err = redis.Values(values[0], err); err == nil && len(values) > 0 {
				slowlogLastId = values[0].(int64)
				if len(values) > 2 {
					lastSlowExecutionDurationSeconds = float64(values[2].(int64)) / 1e6
				}
			}
		}

		e.metricsMtx.RLock()
		e.metrics["slowlog_last_id"].WithLabelValues(addr, e.redis.Aliases[idx]).Set(float64(slowlogLastId))
		e.metrics["last_slow_execution_duration_seconds"].WithLabelValues(addr, e.redis.Aliases[idx]).Set(lastSlowExecutionDurationSeconds)
		e.metricsMtx.RUnlock()
	}

	log.Debugf("scrapeRedisHost() done")
	return nil
}

func (e *Exporter) scrape(scrapes chan<- scrapeResult) {
	defer close(scrapes)

	now := time.Now().UnixNano()
	e.totalScrapes.Inc()

	errorCount := 0
	for idx, addr := range e.redis.Addrs {
		var up float64 = 1
		if err := e.scrapeRedisHost(scrapes, addr, idx); err != nil {
			errorCount++
			up = 0
		}
		scrapes <- scrapeResult{Name: "up", Addr: addr, Alias: e.redis.Aliases[idx], Value: up}
	}

	e.scrapeErrors.Set(float64(errorCount))
	e.duration.Set(float64(time.Now().UnixNano()-now) / 1000000000)
}

func (e *Exporter) setMetrics(scrapes <-chan scrapeResult) {
	for scr := range scrapes {
		name := scr.Name
		if _, ok := e.metrics[name]; !ok {
			e.metricsMtx.Lock()
			e.metrics[name] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Namespace: e.namespace,
				Name:      name,
				Help:      name + "metric", // needs to be set for prometheus >= 2.3.1
			}, []string{"addr", "alias"})
			e.metricsMtx.Unlock()
		}
		var labels prometheus.Labels = map[string]string{"addr": scr.Addr, "alias": scr.Alias}
		if len(scr.DB) > 0 {
			labels["db"] = scr.DB
		}
		e.metrics[name].With(labels).Set(scr.Value)
	}
}

func (e *Exporter) collectMetrics(metrics chan<- prometheus.Metric) {
	for _, m := range e.metrics {
		m.Collect(metrics)
	}
}
