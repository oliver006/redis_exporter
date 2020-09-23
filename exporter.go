package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

type dbKeyPair struct {
	db, key string
}

type keyInfo struct {
	size    float64
	keyType string
}

// All fields of the streamInfo struct must be exported
// because of redis.ScanStruct (reflect) limitations
type streamInfo struct {
	Length           int64 `redis:"length"`
	RadixTreeKeys    int64 `redis:"radix-tree-keys"`
	RadixTreeNodes   int64 `redis:"radix-tree-nodes"`
	Groups           int64 `redis:"groups"`
	StreamGroupsInfo []streamGroupsInfo
}

type streamGroupsInfo struct {
	Name                     string `redis:"name"`
	Consumers                int64  `redis:"consumers"`
	Pending                  int64  `redis:"pending"`
	StreamGroupConsumersInfo []streamGroupConsumersInfo
}

type streamGroupConsumersInfo struct {
	Name    string `redis:"name"`
	Pending int64  `redis:"pending"`
	Idle    int64  `redis:"idle"`
}

// Exporter implements the prometheus.Exporter interface, and exports Redis metrics.
type Exporter struct {
	sync.Mutex

	redisAddr string
	namespace string

	totalScrapes              prometheus.Counter
	scrapeDuration            prometheus.Summary
	targetScrapeRequestErrors prometheus.Counter

	metricDescriptions map[string]*prometheus.Desc

	options Options

	metricMapCounters map[string]string
	metricMapGauges   map[string]string

	mux *http.ServeMux
}

type Options struct {
	User                string
	Password            string
	Namespace           string
	ConfigCommandName   string
	CheckSingleKeys     string
	CheckStreams        string
	CheckSingleStreams  string
	CheckKeys           string
	LuaScript           []byte
	ClientCertificates  []tls.Certificate
	CaCertificates      *x509.CertPool
	InclSystemMetrics   bool
	SkipTLSVerification bool
	SetClientName       bool
	IsTile38            bool
	ExportClientList    bool
	ConnectionTimeouts  time.Duration
	MetricsPath         string
	RedisMetricsOnly    bool
	PingOnConnect       bool
	Registry            *prometheus.Registry
}

func (e *Exporter) scrapeHandler(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("target")
	if target == "" {
		http.Error(w, "'target' parameter must be specified", 400)
		e.targetScrapeRequestErrors.Inc()
		return
	}

	if !strings.Contains(target, "://") {
		target = "redis://" + target
	}

	u, err := url.Parse(target)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid 'target' parameter, parse err: %ck ", err), 400)
		e.targetScrapeRequestErrors.Inc()
		return
	}

	// get rid of username/password info in "target" so users don't send them in plain text via http
	u.User = nil
	target = u.String()

	opts := e.options

	if ck := r.URL.Query().Get("check-keys"); ck != "" {
		opts.CheckKeys = ck
	}

	if csk := r.URL.Query().Get("check-single-keys"); csk != "" {
		opts.CheckSingleKeys = csk
	}

	if cs := r.URL.Query().Get("check-streams"); cs != "" {
		opts.CheckStreams = cs
	}

	if css := r.URL.Query().Get("check-single-streams"); css != "" {
		opts.CheckSingleStreams = css
	}

	registry := prometheus.NewRegistry()
	opts.Registry = registry

	_, err = NewRedisExporter(target, opts)
	if err != nil {
		http.Error(w, "NewRedisExporter() err: err", 400)
		e.targetScrapeRequestErrors.Inc()
		return
	}

	promhttp.HandlerFor(
		registry, promhttp.HandlerOpts{ErrorHandling: promhttp.ContinueOnError},
	).ServeHTTP(w, r)
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

func newMetricDescr(namespace string, metricName string, docString string, labels []string) *prometheus.Desc {
	return prometheus.NewDesc(prometheus.BuildFQName(namespace, "", metricName), docString, labels, nil)
}

// NewRedisExporter returns a new exporter of Redis metrics.
func NewRedisExporter(redisURI string, opts Options) (*Exporter, error) {
	log.Debugf("NewRedisExporter options: %#v", opts)

	e := &Exporter{
		redisAddr: redisURI,
		options:   opts,
		namespace: opts.Namespace,

		totalScrapes: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: opts.Namespace,
			Name:      "exporter_scrapes_total",
			Help:      "Current total redis scrapes.",
		}),

		scrapeDuration: prometheus.NewSummary(prometheus.SummaryOpts{
			Namespace: opts.Namespace,
			Name:      "exporter_scrape_duration_seconds",
			Help:      "Durations of scrapes by the exporter",
		}),

		targetScrapeRequestErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: opts.Namespace,
			Name:      "target_scrape_request_errors_total",
			Help:      "Errors in requests to the exporter",
		}),

		metricMapGauges: map[string]string{
			// # Server
			"uptime_in_seconds": "uptime_in_seconds",
			"process_id":        "process_id",

			// # Clients
			"connected_clients": "connected_clients",
			"blocked_clients":   "blocked_clients",
			"tracking_clients":  "tracking_clients",

			// redis 2,3,4.x
			"client_longest_output_list": "client_longest_output_list",
			"client_biggest_input_buf":   "client_biggest_input_buf",

			// the above two metrics were renamed in redis 5.x
			"client_recent_max_output_buffer": "client_recent_max_output_buffer_bytes",
			"client_recent_max_input_buffer":  "client_recent_max_input_buffer_bytes",

			// # Memory
			"allocator_active":     "allocator_active_bytes",
			"allocator_allocated":  "allocator_allocated_bytes",
			"allocator_resident":   "allocator_resident_bytes",
			"allocator_frag_ratio": "allocator_frag_ratio",
			"allocator_frag_bytes": "allocator_frag_bytes",
			"allocator_rss_ratio":  "allocator_rss_ratio",
			"allocator_rss_bytes":  "allocator_rss_bytes",

			"used_memory":          "memory_used_bytes",
			"used_memory_rss":      "memory_used_rss_bytes",
			"used_memory_peak":     "memory_used_peak_bytes",
			"used_memory_lua":      "memory_used_lua_bytes",
			"used_memory_overhead": "memory_used_overhead_bytes",
			"used_memory_startup":  "memory_used_startup_bytes",
			"used_memory_dataset":  "memory_used_dataset_bytes",
			"used_memory_scripts":  "memory_used_scripts_bytes",
			"maxmemory":            "memory_max_bytes",

			"maxmemory_reservation":         "memory_max_reservation_bytes",
			"maxmemory_desired_reservation": "memory_max_reservation_desired_bytes",

			"maxfragmentationmemory_reservation":         "memory_max_fragmentation_reservation_bytes",
			"maxfragmentationmemory_desired_reservation": "memory_max_fragmentation_reservation_desired_bytes",

			"mem_fragmentation_ratio": "mem_fragmentation_ratio",
			"mem_fragmentation_bytes": "mem_fragmentation_bytes",
			"mem_clients_slaves":      "mem_clients_slaves",
			"mem_clients_normal":      "mem_clients_normal",

			// https://github.com/antirez/redis/blob/17bf0b25c1171486e3a1b089f3181fff2bc0d4f0/src/evict.c#L349-L352
			// ... the sum of AOF and slaves buffer ....
			"mem_not_counted_for_evict": "mem_not_counted_for_eviction_bytes",

			"lazyfree_pending_objects": "lazyfree_pending_objects",
			"active_defrag_running":    "active_defrag_running",

			"migrate_cached_sockets": "migrate_cached_sockets_total",

			"active_defrag_hits":       "defrag_hits",
			"active_defrag_misses":     "defrag_misses",
			"active_defrag_key_hits":   "defrag_key_hits",
			"active_defrag_key_misses": "defrag_key_misses",

			// https://github.com/antirez/redis/blob/0af467d18f9d12b137af3b709c0af579c29d8414/src/expire.c#L297-L299
			"expired_time_cap_reached_count": "expired_time_cap_reached_total",

			// # Persistence
			"loading":                      "loading_dump_file",
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
			"module_fork_in_progress":      "module_fork_in_progress",
			"module_fork_last_cow_size":    "module_fork_last_cow_size",

			// # Stats
			"pubsub_channels":  "pubsub_channels",
			"pubsub_patterns":  "pubsub_patterns",
			"latest_fork_usec": "latest_fork_usec",

			// # Replication
			"connected_slaves":               "connected_slaves",
			"repl_backlog_size":              "replication_backlog_bytes",
			"repl_backlog_active":            "repl_backlog_is_active",
			"repl_backlog_first_byte_offset": "repl_backlog_first_byte_offset",
			"repl_backlog_histlen":           "repl_backlog_history_bytes",
			"master_repl_offset":             "master_repl_offset",
			"second_repl_offset":             "second_repl_offset",
			"slave_expires_tracked_keys":     "slave_expires_tracked_keys",
			"slave_priority":                 "slave_priority",
			"sync_full":                      "replica_resyncs_full",
			"sync_partial_ok":                "replica_partial_resync_accepted",
			"sync_partial_err":               "replica_partial_resync_denied",

			// # Cluster
			"cluster_stats_messages_sent":     "cluster_messages_sent_total",
			"cluster_stats_messages_received": "cluster_messages_received_total",

			// # Tile38
			// based on https://tile38.com/commands/server/
			"tile38_aof_size":        "tile38_aof_size_bytes",
			"tile38_avg_item_size":   "tile38_avg_item_size_bytes",
			"tile38_cpus":            "tile38_cpus_total",
			"tile38_heap_released":   "tile38_heap_released_bytes",
			"tile38_heap_size":       "tile38_heap_size_bytes",
			"tile38_http_transport":  "tile38_http_transport",
			"tile38_in_memory_size":  "tile38_in_memory_size_bytes",
			"tile38_max_heap_size":   "tile38_max_heap_size_bytes",
			"tile38_mem_alloc":       "tile38_mem_alloc_bytes",
			"tile38_num_collections": "tile38_num_collections_total",
			"tile38_num_hooks":       "tile38_num_hooks_total",
			"tile38_num_objects":     "tile38_num_objects_total",
			"tile38_num_points":      "tile38_num_points_total",
			"tile38_pointer_size":    "tile38_pointer_size_bytes",
			"tile38_read_only":       "tile38_read_only",
			"tile38_threads":         "tile38_threads_total",

			// addtl. KeyDB metrics
			"server_threads":        "server_threads_total",
			"long_lock_waits":       "long_lock_waits_total",
			"current_client_thread": "current_client_thread",
		},

		metricMapCounters: map[string]string{
			"total_connections_received": "connections_received_total",
			"total_commands_processed":   "commands_processed_total",

			"rejected_connections":   "rejected_connections_total",
			"total_net_input_bytes":  "net_input_bytes_total",
			"total_net_output_bytes": "net_output_bytes_total",

			"expired_keys":    "expired_keys_total",
			"evicted_keys":    "evicted_keys_total",
			"keyspace_hits":   "keyspace_hits_total",
			"keyspace_misses": "keyspace_misses_total",

			"used_cpu_sys":           "cpu_sys_seconds_total",
			"used_cpu_user":          "cpu_user_seconds_total",
			"used_cpu_sys_children":  "cpu_sys_children_seconds_total",
			"used_cpu_user_children": "cpu_user_children_seconds_total",
		},
	}

	if e.options.ConfigCommandName == "" {
		e.options.ConfigCommandName = "CONFIG"
	}

	if keys, err := parseKeyArg(opts.CheckKeys); err != nil {
		return nil, fmt.Errorf("couldn't parse check-keys: %s", err)
	} else {
		log.Debugf("keys: %#v", keys)
	}

	if singleKeys, err := parseKeyArg(opts.CheckSingleKeys); err != nil {
		return nil, fmt.Errorf("couldn't parse check-single-keys: %s", err)
	} else {
		log.Debugf("singleKeys: %#v", singleKeys)
	}

	if streams, err := parseKeyArg(opts.CheckStreams); err != nil {
		return nil, fmt.Errorf("couldn't parse check-streams: %s", err)
	} else {
		log.Debugf("streams: %#v", streams)
	}

	if singleStreams, err := parseKeyArg(opts.CheckSingleStreams); err != nil {
		return nil, fmt.Errorf("couldn't parse check-single-streams: %s", err)
	} else {
		log.Debugf("singleStreams: %#v", singleStreams)
	}

	if opts.InclSystemMetrics {
		e.metricMapGauges["total_system_memory"] = "total_system_memory_bytes"
	}

	e.metricDescriptions = map[string]*prometheus.Desc{}

	for k, desc := range map[string]struct {
		txt  string
		lbls []string
	}{
		"commands_duration_seconds_total":        {txt: `Total amount of time in seconds spent per command`, lbls: []string{"cmd"}},
		"commands_total":                         {txt: `Total number of calls per command`, lbls: []string{"cmd"}},
		"connected_slave_lag_seconds":            {txt: "Lag of connected slave", lbls: []string{"slave_ip", "slave_port", "slave_state"}},
		"connected_slave_offset_bytes":           {txt: "Offset of connected slave", lbls: []string{"slave_ip", "slave_port", "slave_state"}},
		"db_avg_ttl_seconds":                     {txt: "Avg TTL in seconds", lbls: []string{"db"}},
		"db_keys":                                {txt: "Total number of keys by DB", lbls: []string{"db"}},
		"db_keys_expiring":                       {txt: "Total number of expiring keys by DB", lbls: []string{"db"}},
		"exporter_last_scrape_error":             {txt: "The last scrape error status.", lbls: []string{"err"}},
		"instance_info":                          {txt: "Information about the Redis instance", lbls: []string{"role", "redis_version", "redis_build_id", "redis_mode", "os", "maxmemory_policy"}},
		"key_size":                               {txt: `The length or size of "key"`, lbls: []string{"db", "key"}},
		"key_value":                              {txt: `The value of "key"`, lbls: []string{"db", "key"}},
		"last_slow_execution_duration_seconds":   {txt: `The amount of time needed for last slow execution, in seconds`},
		"latency_spike_last":                     {txt: `When the latency spike last occurred`, lbls: []string{"event_name"}},
		"latency_spike_duration_seconds":         {txt: `Length of the last latency spike in seconds`, lbls: []string{"event_name"}},
		"master_link_up":                         {txt: "Master link status on Redis slave", lbls: []string{"master_host", "master_port"}},
		"master_sync_in_progress":                {txt: "Master sync in progress", lbls: []string{"master_host", "master_port"}},
		"master_last_io_seconds_ago":             {txt: "Master last io seconds ago", lbls: []string{"master_host", "master_port"}},
		"script_values":                          {txt: "Values returned by the collect script", lbls: []string{"key"}},
		"sentinel_tilt":                          {txt: "Sentinel is in TILT mode"},
		"sentinel_masters":                       {txt: "The number of masters this sentinel is watching"},
		"sentinel_running_scripts":               {txt: "Number of scripts in execution right now"},
		"sentinel_scripts_queue_length":          {txt: "Queue of user scripts to execute"},
		"sentinel_simulate_failure_flags":        {txt: "Failures simulations"},
		"sentinel_master_status":                 {txt: "Master status on Sentinel", lbls: []string{"master_name", "master_address", "master_status"}},
		"sentinel_master_slaves":                 {txt: "The number of slaves of the master", lbls: []string{"master_name", "master_address"}},
		"sentinel_master_sentinels":              {txt: "The number of sentinels monitoring this master", lbls: []string{"master_name", "master_address"}},
		"slave_repl_offset":                      {txt: "Slave replication offset", lbls: []string{"master_host", "master_port"}},
		"slave_info":                             {txt: "Information about the Redis slave", lbls: []string{"master_host", "master_port", "read_only"}},
		"slowlog_last_id":                        {txt: `Last id of slowlog`},
		"slowlog_length":                         {txt: `Total slowlog`},
		"start_time_seconds":                     {txt: "Start time of the Redis instance since unix epoch in seconds."},
		"stream_length":                          {txt: `The number of elements of the stream`, lbls: []string{"db", "stream"}},
		"stream_radix_tree_keys":                 {txt: `Radix tree keys count"`, lbls: []string{"db", "stream"}},
		"stream_radix_tree_nodes":                {txt: `Radix tree nodes count`, lbls: []string{"db", "stream"}},
		"stream_groups":                          {txt: `Groups count of stream`, lbls: []string{"db", "stream"}},
		"stream_group_consumers":                 {txt: `Consumers count of stream group`, lbls: []string{"db", "stream", "group"}},
		"stream_group_messages_pending":          {txt: `Pending number of messages in that stream group`, lbls: []string{"db", "stream", "group"}},
		"stream_group_consumer_messages_pending": {txt: `Pending number of messages for this specific consumer`, lbls: []string{"db", "stream", "group", "consumer"}},
		"stream_group_consumer_idle_seconds":     {txt: `Consumer idle time in seconds`, lbls: []string{"db", "stream", "group", "consumer"}},
		"up":                                     {txt: "Information about the Redis instance"},
		"connected_clients_details":              {txt: "Details about connected clients", lbls: []string{"host", "port", "name", "age", "idle", "flags", "db", "omem", "cmd"}},
	} {
		e.metricDescriptions[k] = newMetricDescr(opts.Namespace, k, desc.txt, desc.lbls)
	}

	if e.options.MetricsPath == "" {
		e.options.MetricsPath = "/metrics"
	}

	e.mux = http.NewServeMux()

	if e.options.Registry != nil {
		e.options.Registry.MustRegister(e)
		e.mux.Handle(e.options.MetricsPath, promhttp.HandlerFor(
			e.options.Registry, promhttp.HandlerOpts{ErrorHandling: promhttp.ContinueOnError},
		))

		if !e.options.RedisMetricsOnly {
			buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Namespace: opts.Namespace,
				Name:      "exporter_build_info",
				Help:      "redis exporter build_info",
			}, []string{"version", "commit_sha", "build_date", "golang_version"})
			buildInfo.WithLabelValues(BuildVersion, BuildCommitSha, BuildDate, runtime.Version()).Set(1)
			e.options.Registry.MustRegister(buildInfo)
		}
	}

	e.mux.HandleFunc("/scrape", e.scrapeHandler)
	e.mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`ok`))
	})
	e.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
<head><title>Redis Exporter ` + BuildVersion + `</title></head>
<body>
<h1>Redis Exporter ` + BuildVersion + `</h1>
<p><a href='` + opts.MetricsPath + `'>Metrics</a></p>
</body>
</html>
`))
	})

	return e, nil
}

func (e *Exporter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.mux.ServeHTTP(w, r)
}

// Describe outputs Redis metric descriptions.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range e.metricDescriptions {
		ch <- desc
	}

	for _, v := range e.metricMapGauges {
		ch <- newMetricDescr(e.options.Namespace, v, v+" metric", nil)
	}

	for _, v := range e.metricMapCounters {
		ch <- newMetricDescr(e.options.Namespace, v, v+" metric", nil)
	}

	ch <- e.totalScrapes.Desc()
	ch <- e.scrapeDuration.Desc()
	ch <- e.targetScrapeRequestErrors.Desc()
}

// Collect fetches new metrics from the RedisHost and updates the appropriate metrics.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	e.Lock()
	defer e.Unlock()
	e.totalScrapes.Inc()

	if e.redisAddr != "" {
		startTime := time.Now()
		var up float64 = 1
		if err := e.scrapeRedisHost(ch); err != nil {
			up = 0
			e.registerConstMetricGauge(ch, "exporter_last_scrape_error", 1.0, fmt.Sprintf("%s", err))
		} else {
			e.registerConstMetricGauge(ch, "exporter_last_scrape_error", 0, "")
		}

		e.registerConstMetricGauge(ch, "up", up)

		took := time.Since(startTime).Seconds()
		e.scrapeDuration.Observe(took)
		e.registerConstMetricGauge(ch, "exporter_last_scrape_duration_seconds", took)
	}

	ch <- e.totalScrapes
	ch <- e.scrapeDuration
	ch <- e.targetScrapeRequestErrors
}

func (e *Exporter) includeMetric(s string) bool {
	if strings.HasPrefix(s, "db") || strings.HasPrefix(s, "cmdstat_") || strings.HasPrefix(s, "cluster_") {
		return true
	}
	if _, ok := e.metricMapGauges[s]; ok {
		return true
	}

	_, ok := e.metricMapCounters[s]
	return ok
}

var (
	metricNameRE = regexp.MustCompile(`[^a-zA-Z0-9_]`)
)

func sanitizeMetricName(n string) string {
	return metricNameRE.ReplaceAllString(n, "_")
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
	Valid Examples
	id=11 addr=127.0.0.1:63508 fd=8 name= age=6321 idle=6320 flags=N db=0 sub=0 psub=0 multi=-1 qbuf=0 qbuf-free=0 obl=0 oll=0 omem=0 events=r cmd=setex
	id=14 addr=127.0.0.1:64958 fd=9 name= age=5 idle=0 flags=N db=0 sub=0 psub=0 multi=-1 qbuf=26 qbuf-free=32742 obl=0 oll=0 omem=0 events=r cmd=client
*/
func parseClientListString(clientInfo string) (host string, port string, name string, age string, idle string, flags string, db string, omem string, cmd string, ok bool) {
	ok = false
	if matched, _ := regexp.MatchString(`^id=\d+ addr=\d+`, clientInfo); !matched {
		return
	}
	connectedClient := map[string]string{}
	for _, kvPart := range strings.Split(clientInfo, " ") {
		vPart := strings.Split(kvPart, "=")
		if len(vPart) != 2 {
			log.Debugf("Invalid format for client list string, got: %s", kvPart)
			return
		}
		connectedClient[vPart[0]] = vPart[1]
	}

	hostPortString := strings.Split(connectedClient["addr"], ":")
	if len(hostPortString) != 2 {
		return
	}
	host = hostPortString[0]
	port = hostPortString[1]

	name = connectedClient["name"]
	age = connectedClient["age"]
	idle = connectedClient["idle"]
	flags = connectedClient["flags"]
	db = connectedClient["db"]
	omem = connectedClient["omem"]
	cmd = connectedClient["cmd"]

	ok = true
	return
}

/*
	valid example: db0:keys=1,expires=0,avg_ttl=0
*/
func parseDBKeyspaceString(inputKey string, inputVal string) (keysTotal float64, keysExpiringTotal float64, avgTTL float64, ok bool) {
	log.Debugf("parseDBKeyspaceString inputKey: [%s] inputVal: [%s]", inputKey, inputVal)

	if !strings.HasPrefix(inputKey, "db") {
		log.Debugf("parseDBKeyspaceString inputKey not starting with 'db': [%s]", inputKey)
		return
	}

	split := strings.Split(inputVal, ",")
	if len(split) != 3 && len(split) != 2 {
		log.Debugf("parseDBKeyspaceString strings.Split(inputVal) invalid: %#v", split)
		return
	}

	var err error
	if keysTotal, err = extractVal(split[0]); err != nil {
		log.Debugf("parseDBKeyspaceString extractVal(split[0]) invalid, err: %s", err)
		return
	}
	if keysExpiringTotal, err = extractVal(split[1]); err != nil {
		log.Debugf("parseDBKeyspaceString extractVal(split[1]) invalid, err: %s", err)
		return
	}

	avgTTL = -1
	if len(split) > 2 {
		if avgTTL, err = extractVal(split[2]); err != nil {
			log.Debugf("parseDBKeyspaceString extractVal(split[2]) invalid, err: %s", err)
			return
		}
		avgTTL /= 1000
	}

	ok = true
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

	if lagStr, exists := connectedSlaveInfo["lag"]; !exists {
		// Prior to Redis 3.0, "lag" property does not exist
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

/*
	valid examples:
		master0:name=user03,status=sdown,address=192.169.2.52:6381,slaves=1,sentinels=5
		master1:name=user02,status=ok,address=192.169.2.54:6380,slaves=1,sentinels=5
*/
func parseSentinelMasterString(master string, masterInfo string) (masterName string, masterStatus string, masterAddr string, masterSlaves float64, masterSentinels float64, ok bool) {
	ok = false
	if matched, _ := regexp.MatchString(`^master\d+`, master); !matched {
		return
	}
	matchedMasterInfo := make(map[string]string)
	for _, kvPart := range strings.Split(masterInfo, ",") {
		x := strings.Split(kvPart, "=")
		if len(x) != 2 {
			log.Errorf("Invalid format for sentinel's master string, got: %s", kvPart)
			continue
		}
		matchedMasterInfo[x[0]] = x[1]
	}

	masterName = matchedMasterInfo["name"]
	masterStatus = matchedMasterInfo["status"]
	masterAddr = matchedMasterInfo["address"]
	masterSlaves, err := strconv.ParseFloat(matchedMasterInfo["slaves"], 64)
	if err != nil {
		log.Debugf("parseSentinelMasterString(): couldn't parse slaves value, got: %s, err: %s", matchedMasterInfo["slaves"], err)
		return
	}
	masterSentinels, err = strconv.ParseFloat(matchedMasterInfo["sentinels"], 64)
	if err != nil {
		log.Debugf("parseSentinelMasterString(): couldn't parse sentinels value, got: %s, err: %s", matchedMasterInfo["sentinels"], err)
		return
	}
	ok = true

	return
}

func (e *Exporter) extractConfigMetrics(ch chan<- prometheus.Metric, config []string) (dbCount int, err error) {
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
			e.registerConstMetricGauge(ch, fmt.Sprintf("config_%s", strKey), val)
		}
	}
	return
}

func (e *Exporter) registerConstMetricGauge(ch chan<- prometheus.Metric, metric string, val float64, labels ...string) {
	e.registerConstMetric(ch, metric, val, prometheus.GaugeValue, labels...)
}

func (e *Exporter) registerConstMetric(ch chan<- prometheus.Metric, metric string, val float64, valType prometheus.ValueType, labelValues ...string) {
	descr := e.metricDescriptions[metric]
	if descr == nil {
		descr = newMetricDescr(e.options.Namespace, metric, metric+" metric", labelValues)
	}

	if m, err := prometheus.NewConstMetric(descr, valType, val, labelValues...); err == nil {
		ch <- m
	} else {
		log.Debugf("NewConstMetric() err: %s", err)
	}
}

func (e *Exporter) handleMetricsCommandStats(ch chan<- prometheus.Metric, fieldKey string, fieldValue string) {
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
	if len(splitValue) < 3 {
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

	cmd := splitKey[1]
	e.registerConstMetric(ch, "commands_total", calls, prometheus.CounterValue, cmd)
	e.registerConstMetric(ch, "commands_duration_seconds_total", usecTotal/1e6, prometheus.CounterValue, cmd)
}

func (e *Exporter) handleMetricsReplication(ch chan<- prometheus.Metric, masterHost string, masterPort string, fieldKey string, fieldValue string) bool {
	// only slaves have this field
	if fieldKey == "master_link_status" {
		if fieldValue == "up" {
			e.registerConstMetricGauge(ch, "master_link_up", 1, masterHost, masterPort)
		} else {
			e.registerConstMetricGauge(ch, "master_link_up", 0, masterHost, masterPort)
		}
		return true
	}
	switch fieldKey {

	case "master_last_io_seconds_ago", "slave_repl_offset", "master_sync_in_progress":
		val, _ := strconv.Atoi(fieldValue)
		e.registerConstMetricGauge(ch, fieldKey, float64(val), masterHost, masterPort)
		return true
	}

	// not a slave, try extracting master metrics
	if slaveOffset, slaveIP, slavePort, slaveState, slaveLag, ok := parseConnectedSlaveString(fieldKey, fieldValue); ok {
		e.registerConstMetricGauge(ch,
			"connected_slave_offset_bytes",
			slaveOffset,
			slaveIP, slavePort, slaveState,
		)

		if slaveLag > -1 {
			e.registerConstMetricGauge(ch,
				"connected_slave_lag_seconds",
				slaveLag,
				slaveIP, slavePort, slaveState,
			)
		}
		return true
	}

	return false
}

func (e *Exporter) handleMetricsSentinel(ch chan<- prometheus.Metric, fieldKey string, fieldValue string) bool {

	switch fieldKey {

	case "sentinel_masters", "sentinel_tilt", "sentinel_running_scripts", "sentinel_scripts_queue_length", "sentinel_simulate_failure_flags":
		val, _ := strconv.Atoi(fieldValue)
		e.registerConstMetricGauge(ch, fieldKey, float64(val))
		return true
	}

	if masterName, masterStatus, masterAddress, masterSlaves, masterSentinels, ok := parseSentinelMasterString(fieldKey, fieldValue); ok {
		if masterStatus == "ok" {
			e.registerConstMetricGauge(ch, "sentinel_master_status", 1, masterName, masterAddress, masterStatus)
		} else {
			e.registerConstMetricGauge(ch, "sentinel_master_status", 0, masterName, masterAddress, masterStatus)
		}

		e.registerConstMetricGauge(ch, "sentinel_master_slaves", masterSlaves, masterName, masterAddress)
		e.registerConstMetricGauge(ch, "sentinel_master_sentinels", masterSentinels, masterName, masterAddress)
		return true
	}

	return false
}

func (e *Exporter) handleMetricsServer(ch chan<- prometheus.Metric, fieldKey string, fieldValue string) {
	if fieldKey == "uptime_in_seconds" {
		if uptime, err := strconv.ParseFloat(fieldValue, 64); err == nil {
			e.registerConstMetricGauge(ch, "start_time_seconds", float64(time.Now().Unix())-uptime)
		}
	}
}

func (e *Exporter) extractInfoMetrics(ch chan<- prometheus.Metric, info string, dbCount int) {
	instanceInfo := map[string]string{}
	slaveInfo := map[string]string{}
	handledDBs := map[string]bool{}

	fieldClass := ""
	lines := strings.Split(info, "\n")
	masterHost := ""
	masterPort := ""
	for _, line := range lines {
		line = strings.TrimSpace(line)
		log.Debugf("info: %s", line)
		if len(line) > 0 && strings.HasPrefix(line, "# ") {
			fieldClass = line[2:]
			log.Debugf("set fieldClass: %s", fieldClass)
			continue
		}

		if (len(line) < 2) || (!strings.Contains(line, ":")) {
			continue
		}

		split := strings.SplitN(line, ":", 2)
		fieldKey := split[0]
		fieldValue := split[1]

		var (
			instanceInfoFields = map[string]bool{"role": true, "redis_version": true, "redis_build_id": true, "redis_mode": true, "os": true, "maxmemory_policy": true}
			slaveInfoFields    = map[string]bool{"master_host": true, "master_port": true, "slave_read_only": true}
		)

		if fieldKey == "master_host" {
			masterHost = fieldValue
		}

		if fieldKey == "master_port" {
			masterPort = fieldValue
		}

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
			if ok := e.handleMetricsReplication(ch, masterHost, masterPort, fieldKey, fieldValue); ok {
				continue
			}

		case "Server":
			e.handleMetricsServer(ch, fieldKey, fieldValue)

		case "Commandstats":
			e.handleMetricsCommandStats(ch, fieldKey, fieldValue)
			continue

		case "Keyspace":
			if keysTotal, keysEx, avgTTL, ok := parseDBKeyspaceString(fieldKey, fieldValue); ok {
				dbName := fieldKey

				e.registerConstMetricGauge(ch, "db_keys", keysTotal, dbName)
				e.registerConstMetricGauge(ch, "db_keys_expiring", keysEx, dbName)

				if avgTTL > -1 {
					e.registerConstMetricGauge(ch, "db_avg_ttl_seconds", avgTTL, dbName)
				}
				handledDBs[dbName] = true
				continue
			}

		case "Sentinel":
			e.handleMetricsSentinel(ch, fieldKey, fieldValue)
		}

		if !e.includeMetric(fieldKey) {
			continue
		}

		e.parseAndRegisterConstMetric(ch, fieldKey, fieldValue)
	}

	for dbIndex := 0; dbIndex < dbCount; dbIndex++ {
		dbName := "db" + strconv.Itoa(dbIndex)
		if _, exists := handledDBs[dbName]; !exists {
			e.registerConstMetricGauge(ch, "db_keys", 0, dbName)
			e.registerConstMetricGauge(ch, "db_keys_expiring", 0, dbName)
		}
	}

	e.registerConstMetricGauge(ch, "instance_info", 1,
		instanceInfo["role"],
		instanceInfo["redis_version"],
		instanceInfo["redis_build_id"],
		instanceInfo["redis_mode"],
		instanceInfo["os"],
		instanceInfo["maxmemory_policy"])

	if instanceInfo["role"] == "slave" {
		e.registerConstMetricGauge(ch, "slave_info", 1,
			slaveInfo["master_host"],
			slaveInfo["master_port"],
			slaveInfo["slave_read_only"])
	}
}

func (e *Exporter) extractClusterInfoMetrics(ch chan<- prometheus.Metric, info string) {
	lines := strings.Split(info, "\r\n")

	for _, line := range lines {
		log.Debugf("info: %s", line)

		split := strings.Split(line, ":")
		if len(split) != 2 {
			continue
		}
		fieldKey := split[0]
		fieldValue := split[1]

		if !e.includeMetric(fieldKey) {
			continue
		}

		e.parseAndRegisterConstMetric(ch, fieldKey, fieldValue)
	}
}

func (e *Exporter) extractCheckKeyMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	keys, err := parseKeyArg(e.options.CheckKeys)
	if err != nil {
		log.Errorf("Couldn't parse check-keys: %#v", err)
		return
	}
	log.Debugf("keys: %#v", keys)

	singleKeys, err := parseKeyArg(e.options.CheckSingleKeys)
	if err != nil {
		log.Errorf("Couldn't parse check-single-keys: %#v", err)
		return
	}
	log.Debugf("e.singleKeys: %#v", singleKeys)

	allKeys := append([]dbKeyPair{}, singleKeys...)

	log.Debugf("e.keys: %#v", keys)
	scannedKeys, err := getKeysFromPatterns(c, keys)
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
		e.registerConstMetricGauge(ch, "key_size", info.size, dbLabel, k.key)

		// Only record value metric if value is float-y
		if val, err := redis.Float64(doRedisCmd(c, "GET", k.key)); err == nil {
			e.registerConstMetricGauge(ch, "key_value", val, dbLabel, k.key)
		}
	}
}

func (e *Exporter) extractStreamMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	streams, err := parseKeyArg(e.options.CheckStreams)
	if err != nil {
		log.Errorf("Couldn't parse given stream keys: %s", err)
		return
	}

	singleStreams, err := parseKeyArg(e.options.CheckSingleStreams)
	if err != nil {
		log.Errorf("Couldn't parse check-single-streams: %s", err)
		return
	}
	allStreams := append([]dbKeyPair{}, singleStreams...)

	scannedStreams, err := getKeysFromPatterns(c, streams)
	if err != nil {
		log.Errorf("Error expanding key patterns: %s", err)
	} else {
		allStreams = append(allStreams, scannedStreams...)
	}

	log.Debugf("allStreams: %#v", allStreams)
	for _, k := range allStreams {
		if _, err := doRedisCmd(c, "SELECT", k.db); err != nil {
			log.Debugf("Couldn't select database '%s' when getting stream info", k.db)
			continue
		}
		info, err := getStreamInfo(c, k.key)
		if err != nil {
			log.Errorf("couldn't get info for stream '%s', err: %s", k.key, err)
			continue
		}
		dbLabel := "db" + k.db
		e.registerConstMetricGauge(ch, "stream_length", float64(info.Length), dbLabel, k.key)
		e.registerConstMetricGauge(ch, "stream_radix_tree_keys", float64(info.RadixTreeKeys), dbLabel, k.key)
		e.registerConstMetricGauge(ch, "stream_radix_tree_nodes", float64(info.RadixTreeNodes), dbLabel, k.key)
		e.registerConstMetricGauge(ch, "stream_groups", float64(info.Groups), dbLabel, k.key)
		for _, g := range info.StreamGroupsInfo {
			e.registerConstMetricGauge(ch, "stream_group_consumers", float64(g.Consumers), dbLabel, k.key, g.Name)
			e.registerConstMetricGauge(ch, "stream_group_messages_pending", float64(g.Pending), dbLabel, k.key, g.Name)
			for _, c := range g.StreamGroupConsumersInfo {
				e.registerConstMetricGauge(ch, "stream_group_consumer_messages_pending", float64(c.Pending), dbLabel, k.key, g.Name, c.Name)
				e.registerConstMetricGauge(ch, "stream_group_consumer_idle_seconds", float64(c.Idle)/1e3, dbLabel, k.key, g.Name, c.Name)
			}
		}

	}
}

func (e *Exporter) extractLuaScriptMetrics(ch chan<- prometheus.Metric, c redis.Conn) error {
	log.Debug("Evaluating e.options.LuaScript")
	kv, err := redis.StringMap(doRedisCmd(c, "EVAL", e.options.LuaScript, 0, 0))
	if err != nil {
		log.Errorf("LuaScript error: %v", err)
		return err
	}

	if len(kv) == 0 {
		return nil
	}

	for key, stringVal := range kv {
		if val, err := strconv.ParseFloat(stringVal, 64); err == nil {
			e.registerConstMetricGauge(ch, "script_values", val, key)
		}
	}
	return nil
}

func (e *Exporter) extractSlowLogMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	if reply, err := redis.Int64(doRedisCmd(c, "SLOWLOG", "LEN")); err == nil {
		e.registerConstMetricGauge(ch, "slowlog_length", float64(reply))
	}

	if values, err := redis.Values(doRedisCmd(c, "SLOWLOG", "GET", "1")); err == nil {
		var slowlogLastID int64
		var lastSlowExecutionDurationSeconds float64

		if len(values) > 0 {
			if values, err = redis.Values(values[0], err); err == nil && len(values) > 0 {
				slowlogLastID = values[0].(int64)
				if len(values) > 2 {
					lastSlowExecutionDurationSeconds = float64(values[2].(int64)) / 1e6
				}
			}
		}

		e.registerConstMetricGauge(ch, "slowlog_last_id", float64(slowlogLastID))
		e.registerConstMetricGauge(ch, "last_slow_execution_duration_seconds", lastSlowExecutionDurationSeconds)
	}
}

func (e *Exporter) extractLatencyMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	if reply, err := redis.Values(doRedisCmd(c, "LATENCY", "LATEST")); err == nil {
		for _, l := range reply {
			if latencyResult, err := redis.Values(l, nil); err == nil {
				var eventName string
				var spikeLast, spikeDuration, max int64
				if _, err := redis.Scan(latencyResult, &eventName, &spikeLast, &spikeDuration, &max); err == nil {
					spikeDurationSeconds := float64(spikeDuration) / 1e3
					e.registerConstMetricGauge(ch, "latency_spike_last", float64(spikeLast), eventName)
					e.registerConstMetricGauge(ch, "latency_spike_duration_seconds", spikeDurationSeconds, eventName)
				}
			}
		}
	}
}

func (e *Exporter) extractTile38Metrics(ch chan<- prometheus.Metric, c redis.Conn) {
	info, err := redis.Strings(doRedisCmd(c, "SERVER"))
	if err != nil {
		log.Errorf("extractTile38Metrics() err: %s", err)
		return
	}

	for i := 0; i < len(info); i += 2 {
		fieldKey := "tile38_" + info[i]
		fieldValue := info[i+1]
		log.Debugf("tile38   key:%s   val:%s", fieldKey, fieldValue)

		if !e.includeMetric(fieldKey) {
			continue
		}

		e.parseAndRegisterConstMetric(ch, fieldKey, fieldValue)
	}
}

func (e *Exporter) extractConnectedClientMetrics(ch chan<- prometheus.Metric, c redis.Conn) {
	if reply, err := redis.String(doRedisCmd(c, "CLIENT", "LIST")); err == nil {
		clients := strings.Split(reply, "\n")

		for _, c := range clients {
			if host, port, name, age, idle, flags, db, omem, cmd, ok := parseClientListString(c); ok {
				e.registerConstMetricGauge(ch, "connected_clients_details", 1.0, host, port, name, age, idle, flags, db, omem, cmd)
			}
		}
	}
}

func (e *Exporter) parseAndRegisterConstMetric(ch chan<- prometheus.Metric, fieldKey, fieldValue string) {
	orgMetricName := sanitizeMetricName(fieldKey)
	metricName := orgMetricName
	if newName, ok := e.metricMapGauges[metricName]; ok {
		metricName = newName
	} else {
		if newName, ok := e.metricMapCounters[metricName]; ok {
			metricName = newName
		}
	}

	var err error
	var val float64

	switch fieldValue {

	case "ok", "true":
		val = 1

	case "err", "fail", "false":
		val = 0

	default:
		val, err = strconv.ParseFloat(fieldValue, 64)

	}
	if err != nil {
		log.Debugf("couldn't parse %s, err: %s", fieldValue, err)
	}

	t := prometheus.GaugeValue
	if e.metricMapCounters[orgMetricName] != "" {
		t = prometheus.CounterValue
	}

	switch metricName {
	case "latest_fork_usec":
		metricName = "latest_fork_seconds"
		val = val / 1e6
	}

	e.registerConstMetric(ch, metricName, val, t)
}

func doRedisCmd(c redis.Conn, cmd string, args ...interface{}) (interface{}, error) {
	log.Debugf("c.Do() - running command: %s %s", cmd, args)
	res, err := c.Do(cmd, args...)
	if err != nil {
		log.Debugf("c.Do() - err: %s", err)
	}
	log.Debugf("c.Do() - done")
	return res, err
}

var errNotFound = errors.New("key not found")

// getKeyInfo takes a key and returns the type, and the size or length of the value stored at that key.
func getKeyInfo(c redis.Conn, key string) (info keyInfo, err error) {
	if info.keyType, err = redis.String(doRedisCmd(c, "TYPE", key)); err != nil {
		return info, err
	}

	switch info.keyType {
	case "none":
		return info, errNotFound
	case "string":
		if size, err := redis.Int64(doRedisCmd(c, "PFCOUNT", key)); err == nil {
			// hyperloglog
			info.size = float64(size)
		} else if size, err := redis.Int64(doRedisCmd(c, "STRLEN", key)); err == nil {
			info.size = float64(size)
		}
	case "list":
		if size, err := redis.Int64(doRedisCmd(c, "LLEN", key)); err == nil {
			info.size = float64(size)
		}
	case "set":
		if size, err := redis.Int64(doRedisCmd(c, "SCARD", key)); err == nil {
			info.size = float64(size)
		}
	case "zset":
		if size, err := redis.Int64(doRedisCmd(c, "ZCARD", key)); err == nil {
			info.size = float64(size)
		}
	case "hash":
		if size, err := redis.Int64(doRedisCmd(c, "HLEN", key)); err == nil {
			info.size = float64(size)
		}
	case "stream":
		if size, err := redis.Int64(doRedisCmd(c, "XLEN", key)); err == nil {
			info.size = float64(size)
		}
	default:
		err = fmt.Errorf("unknown type: %v for key: %v", info.keyType, key)
	}

	return info, err
}

func getStreamInfo(c redis.Conn, key string) (*streamInfo, error) {
	v, err := redis.Values(doRedisCmd(c, "XINFO", "STREAM", key))
	if err != nil {
		return nil, err
	}
	// Scan slice to struct
	var stream streamInfo
	if err := redis.ScanStruct(v, &stream); err != nil {
		return nil, err
	}

	stream.StreamGroupsInfo, err = scanStreamGroups(c, key)
	if err != nil {
		return nil, err
	}

	log.Debugf("stream: %#v", &stream)
	return &stream, nil
}

func scanStreamGroups(c redis.Conn, stream string) ([]streamGroupsInfo, error) {
	groups, err := redis.Values(doRedisCmd(c, "XINFO", "GROUPS", stream))
	if err != nil {
		return nil, err
	}

	var result []streamGroupsInfo
	for _, g := range groups {
		v, err := redis.Values(g, nil)
		if err != nil {
			log.Errorf("Couldn't convert group values for stream '%s': %s", stream, err)
			continue
		}
		log.Debugf("streamGroupsInfo value: %#v", v)

		var group streamGroupsInfo
		if err := redis.ScanStruct(v, &group); err != nil {
			log.Errorf("Couldn't scan group in stream '%s': %s", stream, err)
			continue
		}

		group.StreamGroupConsumersInfo, err = scanStreamGroupConsumers(c, stream, group.Name)
		if err != nil {
			return nil, err
		}

		result = append(result, group)
	}

	log.Debugf("groups: %v", result)
	return result, nil
}

func scanStreamGroupConsumers(c redis.Conn, stream string, group string) ([]streamGroupConsumersInfo, error) {
	consumers, err := redis.Values(doRedisCmd(c, "XINFO", "CONSUMERS", stream, group))
	if err != nil {
		return nil, err
	}

	var result []streamGroupConsumersInfo
	for _, c := range consumers {

		v, err := redis.Values(c, nil)
		if err != nil {
			log.Errorf("Couldn't convert consumer values for group '%s' in stream '%s': %s", group, stream, err)
			continue
		}
		log.Debugf("streamGroupConsumersInfo value: %#v", v)

		var consumer streamGroupConsumersInfo
		if err := redis.ScanStruct(v, &consumer); err != nil {
			log.Errorf("Couldn't scan consumers for  group '%s' in stream '%s': %s", group, stream, err)
			continue
		}

		result = append(result, consumer)
	}

	log.Debugf("consumers: %v", result)
	return result, nil
}

// scanForKeys returns a list of keys matching `pattern` by using `SCAN`, which is safer for production systems than using `KEYS`.
// This function was adapted from: https://github.com/reisinger/examples-redigo
func scanForKeys(c redis.Conn, pattern string) ([]string, error) {
	iter := 0
	keys := []string{}

	for {
		arr, err := redis.Values(doRedisCmd(c, "SCAN", iter, "MATCH", pattern))
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
			if _, err := doRedisCmd(c, "SELECT", k.db); err != nil {
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

func (e *Exporter) connectToRedis() (redis.Conn, error) {
	options := []redis.DialOption{
		redis.DialConnectTimeout(e.options.ConnectionTimeouts),
		redis.DialReadTimeout(e.options.ConnectionTimeouts),
		redis.DialWriteTimeout(e.options.ConnectionTimeouts),

		redis.DialTLSConfig(&tls.Config{
			InsecureSkipVerify: e.options.SkipTLSVerification,
			Certificates:       e.options.ClientCertificates,
			RootCAs:            e.options.CaCertificates,
		}),
	}

	if e.options.User != "" {
		options = append(options, redis.DialUsername(e.options.User))
	}

	if e.options.Password != "" {
		options = append(options, redis.DialPassword(e.options.Password))
	}

	uri := e.redisAddr
	if !strings.Contains(uri, "://") {
		uri = "redis://" + uri
	}

	log.Debugf("Trying DialURL(): %s", uri)
	c, err := redis.DialURL(uri, options...)
	if err != nil {
		log.Debugf("DialURL() failed, err: %s", err)
		if frags := strings.Split(e.redisAddr, "://"); len(frags) == 2 {
			log.Debugf("Trying: Dial(): %s %s", frags[0], frags[1])
			c, err = redis.Dial(frags[0], frags[1], options...)
		} else {
			log.Debugf("Trying: Dial(): tcp %s", e.redisAddr)
			c, err = redis.Dial("tcp", e.redisAddr, options...)
		}
	}
	return c, err
}

func (e *Exporter) scrapeRedisHost(ch chan<- prometheus.Metric) error {
	defer log.Debugf("scrapeRedisHost() done")

	startTime := time.Now()
	c, err := e.connectToRedis()
	connectTookSeconds := time.Since(startTime).Seconds()
	e.registerConstMetricGauge(ch, "exporter_last_scrape_connect_time_seconds", connectTookSeconds)

	if err != nil {
		log.Errorf("Couldn't connect to redis instance")
		log.Debugf("connectToRedis( %s ) err: %s", e.redisAddr, err)
		return err
	}
	defer c.Close()

	log.Debugf("connected to: %s", e.redisAddr)
	log.Debugf("connecting took %f seconds", connectTookSeconds)

	if e.options.PingOnConnect {
		startTime := time.Now()

		if _, err := doRedisCmd(c, "PING"); err != nil {
			log.Errorf("Couldn't PING server, err: %s", err)
		} else {
			pingTookSeconds := time.Since(startTime).Seconds()
			e.registerConstMetricGauge(ch, "exporter_last_scrape_ping_time_seconds", pingTookSeconds)
			log.Debugf("PING took %f seconds", pingTookSeconds)
		}
	}

	if e.options.SetClientName {
		if _, err := doRedisCmd(c, "CLIENT", "SETNAME", "redis_exporter"); err != nil {
			log.Errorf("Couldn't set client name, err: %s", err)
		}
	}

	dbCount := 0
	if config, err := redis.Strings(doRedisCmd(c, e.options.ConfigCommandName, "GET", "*")); err == nil {
		log.Debugf("Redis CONFIG GET * result: [%#v]", config)
		dbCount, err = e.extractConfigMetrics(ch, config)
		if err != nil {
			log.Errorf("Redis CONFIG err: %s", err)
			return err
		}
	} else {
		log.Debugf("Redis CONFIG err: %s", err)
	}

	infoAll, err := redis.String(doRedisCmd(c, "INFO", "ALL"))
	if err != nil {
		log.Debugf("Redis INFO ALL err: %s", err)
		infoAll, err = redis.String(doRedisCmd(c, "INFO"))
		if err != nil {
			log.Errorf("Redis INFO err: %s", err)
			return err
		}
	}
	log.Debugf("Redis INFO ALL result: [%#v]", infoAll)

	if strings.Contains(infoAll, "cluster_enabled:1") {
		if clusterInfo, err := redis.String(doRedisCmd(c, "CLUSTER", "INFO")); err == nil {
			e.extractClusterInfoMetrics(ch, clusterInfo)

			// in cluster mode Redis only supports one database so no extra DB number padding needed
			dbCount = 1
		} else {
			log.Errorf("Redis CLUSTER INFO err: %s", err)
		}
	} else if dbCount == 0 {
		// in non-cluster mode, if dbCount is zero then "CONFIG" failed to retrieve a valid
		// number of databases and we use the Redis config default which is 16

		dbCount = 16
	}

	log.Debugf("dbCount: %d", dbCount)

	e.extractInfoMetrics(ch, infoAll, dbCount)

	e.extractLatencyMetrics(ch, c)

	e.extractCheckKeyMetrics(ch, c)

	e.extractSlowLogMetrics(ch, c)

	e.extractStreamMetrics(ch, c)

	if e.options.LuaScript != nil && len(e.options.LuaScript) > 0 {
		if err := e.extractLuaScriptMetrics(ch, c); err != nil {
			return err
		}
	}

	if e.options.ExportClientList {
		e.extractConnectedClientMetrics(ch, c)
	}

	if e.options.IsTile38 {
		e.extractTile38Metrics(ch, c)
	}

	return nil
}
