package exporter

import (
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

// Exporter implements the prometheus.Exporter interface, and exports Redis metrics.
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
	metricsMtx   sync.RWMutex
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
		"used_memory":             "memory_used_bytes",
		"used_memory_rss":         "memory_used_rss_bytes",
		"used_memory_peak":        "memory_used_peak_bytes",
		"used_memory_lua":         "memory_used_lua_bytes",
		"maxmemory":               "memory_max_bytes",
		"mem_fragmentation_ratio": "memory_fragmentation_ratio",

		// # Persistence
		"rdb_changes_since_last_save":  "rdb_changes_since_last_save",
		"rdb_last_bgsave_time_sec":     "rdb_last_bgsave_duration_sec",
		"rdb_current_bgsave_time_sec":  "rdb_current_bgsave_duration_sec",
		"aof_enabled":                  "aof_enabled",
		"aof_rewrite_in_progress":      "aof_rewrite_in_progress",
		"aof_rewrite_scheduled":        "aof_rewrite_scheduled",
		"aof_last_rewrite_time_sec":    "aof_last_rewrite_duration_sec",
		"aof_current_rewrite_time_sec": "aof_current_rewrite_duration_sec",

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
	e.metrics["master_link_up"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "master_link_up",
		Help:      "Master link status on Redis slave",
	}, []string{"addr", "alias"})
	e.metrics["connected_slave_offset"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "connected_slave_offset",
		Help:      "Offset of connected slave",
	}, []string{"addr", "alias", "slave_ip", "slave_state"})
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
		Help:      "When the latency spike last occured",
	}, []string{"addr", "alias", "event_name"})
	e.metrics["latency_spike_milliseconds"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "latency_spike_milliseconds",
		Help:      "Length of the last latency spike in milliseconds",
	}, []string{"addr", "alias", "event_name"})

	// Emulate a Summary.
	e.metrics["command_call_duration_seconds_count"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "command_call_duration_seconds_count",
		Help:      "Total number of calls per command",
	}, []string{"addr", "alias", "cmd"})
	e.metrics["command_call_duration_seconds_sum"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: e.namespace,
		Name:      "command_call_duration_seconds_sum",
		Help:      "Total amount of time in seconds spent per command",
	}, []string{"addr", "alias", "cmd"})
}

// NewRedisExporter returns a new exporter of Redis metrics.
// note to self: next time we add an argument, instead add a RedisExporter struct
func NewRedisExporter(host RedisHost, namespace, checkKeys string) (*Exporter, error) {

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
	for _, k := range strings.Split(checkKeys, ",") {
		var err error
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
			log.Debugf("Couldn't parse db/key string: %s", k)
			continue
		}
		if key != "" {
			e.keys = append(e.keys, dbKeyPair{db, key})
		}
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

	e.keySizes.Reset()
	e.keyValues.Reset()

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
func parseConnectedSlaveString(slaveName string, slaveInfo string) (offset float64, ip string, state string, ok bool) {
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
	ok = true
	ip = connectedSlaveInfo["ip"]
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
			"maxmemory": true,
		}[strKey] {
			continue
		}

		if val, err := strconv.ParseFloat(strVal, 64); err == nil {
			scrapes <- scrapeResult{Name: fmt.Sprintf("config_%s", config[pos*2]), Addr: addr, Alias: alias, Value: val}
		}
	}
	return
}

func (e *Exporter) extractInfoMetrics(info, addr string, alias string, scrapes chan<- scrapeResult, dbCount int, padDBKeyCounts bool) error {
	cmdstats := false
	lines := strings.Split(info, "\r\n")

	instanceInfo := map[string]string{}
	slaveInfo := map[string]string{}
	handledDBs := map[string]bool{}
	for _, line := range lines {
		log.Debugf("info: %s", line)
		if len(line) > 0 && line[0] == '#' {
			if strings.Contains(line, "Commandstats") {
				cmdstats = true
			}
			continue
		}

		if (len(line) < 2) || (!strings.Contains(line, ":")) {
			cmdstats = false
			continue
		}

		split := strings.Split(line, ":")
		if len(split) != 2 {
			continue
		}
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

		if fieldKey == "master_link_status" {
			e.metricsMtx.RLock()
			if fieldValue == "up" {
				e.metrics["master_link_up"].WithLabelValues(addr, alias).Set(1)
			} else {
				e.metrics["master_link_up"].WithLabelValues(addr, alias).Set(0)
			}
			e.metricsMtx.RUnlock()
			continue
		}

		if slaveOffset, slaveIp, slaveState, ok := parseConnectedSlaveString(fieldKey, fieldValue); ok {
			e.metricsMtx.RLock()
			e.metrics["connected_slave_offset"].WithLabelValues(
				addr,
				alias,
				slaveIp,
				slaveState,
			).Set(slaveOffset)
			e.metricsMtx.RUnlock()
		}

		if !includeMetric(fieldKey) {
			continue
		}

		if cmdstats {
			/*
				Format:

				cmdstat_get:calls=21,usec=175,usec_per_call=8.33
				cmdstat_set:calls=61,usec=3139,usec_per_call=51.46
				cmdstat_setex:calls=75,usec=1260,usec_per_call=16.80
			*/
			frags := strings.Split(fieldKey, "_")
			if len(frags) != 2 {
				continue
			}

			cmd := frags[1]

			frags = strings.Split(fieldValue, ",")
			if len(frags) != 3 {
				continue
			}

			var calls float64
			var usecTotal float64
			var err error
			if calls, err = extractVal(frags[0]); err != nil {
				continue
			}
			if usecTotal, err = extractVal(frags[1]); err != nil {
				continue
			}

			e.metricsMtx.RLock()
			e.metrics["command_call_duration_seconds_count"].WithLabelValues(addr, alias, cmd).Set(calls)
			e.metrics["command_call_duration_seconds_sum"].WithLabelValues(addr, alias, cmd).Set(usecTotal / 1e6)
			e.metricsMtx.RUnlock()
			continue
		}

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

		metricName := sanitizeMetricName(fieldKey)
		if newName, ok := metricMap[metricName]; ok {
			metricName = newName
		}

		var err error
		var val float64

		switch fieldValue {

		case "ok":
			val = 1

		case "fail":
			val = 0

		default:
			val, err = strconv.ParseFloat(fieldValue, 64)

		}
		if err != nil {
			log.Debugf("couldn't parse %s, err: %s", fieldValue, err)
			continue
		}

		scrapes <- scrapeResult{Name: metricName, Addr: addr, Alias: alias, Value: val}
	}

	if padDBKeyCounts {
		for dbIndex := 0; dbIndex < dbCount; dbIndex++ {
			dbName := "db" + strconv.Itoa(dbIndex)
			if _, exists := handledDBs[dbName]; !exists {
				scrapes <- scrapeResult{Name: "db_keys", Addr: addr, Alias: alias, DB: dbName, Value: 0}
				scrapes <- scrapeResult{Name: "db_keys_expiring", Addr: addr, Alias: alias, DB: dbName, Value: 0}
			}
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

func doRedisCmd(c redis.Conn, cmd string, args ...interface{}) (reply interface{}, err error) {
	log.Debugf("c.Do() - running command: %s %s", cmd, args)
	defer log.Debugf("c.Do() - done")
	res, err := c.Do(cmd, args...)
	if err != nil {
		log.Debugf("c.Do() - err: %s", err)
	}
	return res, err
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
			e.extractInfoMetrics(clusterInfo, addr, e.redis.Aliases[idx], scrapes, dbCount, false)

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

	e.extractInfoMetrics(infoAll, addr, e.redis.Aliases[idx], scrapes, dbCount, true)

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

	log.Debugf("e.keys: %#v", e.keys)
	for _, k := range e.keys {
		if _, err := doRedisCmd(c, "SELECT", k.db); err != nil {
			continue
		}

		obtainedKeys := []string{}
		if tempVal, err := redis.Strings(doRedisCmd(c, "KEYS", k.key)); err == nil && tempVal != nil {
			for _, tempKey := range tempVal {
				log.Debugf("Append result: %s", tempKey)
				obtainedKeys = append(obtainedKeys, tempKey)
			}
		}

		for _, key := range obtainedKeys {
			dbLabel := "db" + k.db
			keyLabel := key
			if tempVal, err := doRedisCmd(c, "GET", key); err == nil && tempVal != nil {
				if val, err := strconv.ParseFloat(fmt.Sprintf("%s", tempVal), 64); err == nil {
					e.keyValues.WithLabelValues(addr, e.redis.Aliases[idx], dbLabel, keyLabel).Set(val)
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
				if tempVal, err := doRedisCmd(c, op, key); err == nil && tempVal != nil {
					e.keySizes.WithLabelValues(addr, e.redis.Aliases[idx], dbLabel, keyLabel).Set(float64(tempVal.(int64)))
					break
				}
			}
		}
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
			}, []string{"addr", "alias"})
			e.metricsMtx.Unlock()
		}
		var labels prometheus.Labels = map[string]string{"addr": scr.Addr, "alias": scr.Alias}
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
