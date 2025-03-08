package exporter

import (
	"fmt"
	"net/http"
	"net/url"
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

type BuildInfo struct {
	Version   string
	CommitSha string
	Date      string
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

	buildInfo BuildInfo
}

type Options struct {
	User                           string
	Password                       string
	Namespace                      string
	PasswordMap                    map[string]string
	ConfigCommandName              string
	CheckKeys                      string
	CheckSingleKeys                string
	CheckStreams                   string
	CheckSingleStreams             string
	StreamsExcludeConsumerMetrics  bool
	CheckKeysBatchSize             int64
	CheckKeyGroups                 string
	MaxDistinctKeyGroups           int64
	CountKeys                      string
	LuaScript                      map[string][]byte
	ClientCertFile                 string
	ClientKeyFile                  string
	CaCertFile                     string
	InclConfigMetrics              bool
	InclModulesMetrics             bool
	DisableExportingKeyValues      bool
	ExcludeLatencyHistogramMetrics bool
	RedactConfigMetrics            bool
	InclSystemMetrics              bool
	SkipTLSVerification            bool
	SetClientName                  bool
	IsTile38                       bool
	IsCluster                      bool
	ExportClientList               bool
	ExportClientsInclPort          bool
	ConnectionTimeouts             time.Duration
	MetricsPath                    string
	RedisMetricsOnly               bool
	PingOnConnect                  bool
	RedisPwdFile                   string
	Registry                       *prometheus.Registry
	BuildInfo                      BuildInfo
	BasicAuthUsername              string
	BasicAuthPassword              string
	SkipCheckKeysForRoleMaster     bool
}

// NewRedisExporter returns a new exporter of Redis metrics.
func NewRedisExporter(uri string, opts Options) (*Exporter, error) {
	log.Debugf("NewRedisExporter options: %#v", opts)

	switch {
	case strings.HasPrefix(uri, "valkey://"):
		uri = strings.Replace(uri, "valkey://", "redis://", 1)
	case strings.HasPrefix(uri, "valkeys://"):
		uri = strings.Replace(uri, "valkeys://", "rediss://", 1)
	}

	log.Debugf("NewRedisExporter = using redis uri: %s", uri)

	e := &Exporter{
		redisAddr: uri,
		options:   opts,
		namespace: opts.Namespace,

		buildInfo: opts.BuildInfo,

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
			"io_threads_active": "io_threads_active",

			// # Clients
			"connected_clients":            "connected_clients",
			"blocked_clients":              "blocked_clients",
			"maxclients":                   "max_clients",
			"tracking_clients":             "tracking_clients",
			"clients_in_timeout_table":     "clients_in_timeout_table",
			"pubsub_clients":               "pubsub_clients",               // Added in Redis 7.4
			"watching_clients":             "watching_clients",             // Added in Redis 7.4
			"total_watched_keys":           "total_watched_keys",           // Added in Redis 7.4
			"total_blocking_keys":          "total_blocking_keys",          // Added in Redis 7.2
			"total_blocking_keys_on_nokey": "total_blocking_keys_on_nokey", // Added in Redis 7.2

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

			"used_memory":              "memory_used_bytes",
			"used_memory_rss":          "memory_used_rss_bytes",
			"used_memory_peak":         "memory_used_peak_bytes",
			"used_memory_lua":          "memory_used_lua_bytes",
			"used_memory_vm_eval":      "memory_used_vm_eval_bytes",      // Added in Redis 7.0
			"used_memory_scripts_eval": "memory_used_scripts_eval_bytes", // Added in Redis 7.0
			"used_memory_overhead":     "memory_used_overhead_bytes",
			"used_memory_startup":      "memory_used_startup_bytes",
			"used_memory_dataset":      "memory_used_dataset_bytes",
			"number_of_cached_scripts": "number_of_cached_scripts",       // Added in Redis 7.0
			"number_of_functions":      "number_of_functions",            // Added in Redis 7.0
			"number_of_libraries":      "number_of_libraries",            // Added in Redis 7.4
			"used_memory_vm_functions": "memory_used_vm_functions_bytes", // Added in Redis 7.0
			"used_memory_scripts":      "memory_used_scripts_bytes",      // Added in Redis 7.0
			"used_memory_functions":    "memory_used_functions_bytes",    // Added in Redis 7.0
			"used_memory_vm_total":     "memory_used_vm_total",           // Added in Redis 7.0
			"maxmemory":                "memory_max_bytes",

			"maxmemory_reservation":         "memory_max_reservation_bytes",
			"maxmemory_desired_reservation": "memory_max_reservation_desired_bytes",

			"maxfragmentationmemory_reservation":         "memory_max_fragmentation_reservation_bytes",
			"maxfragmentationmemory_desired_reservation": "memory_max_fragmentation_reservation_desired_bytes",

			"mem_fragmentation_ratio": "mem_fragmentation_ratio",
			"mem_fragmentation_bytes": "mem_fragmentation_bytes",
			"mem_clients_slaves":      "mem_clients_slaves",
			"mem_clients_normal":      "mem_clients_normal",

			"expired_stale_perc": "expired_stale_percentage",

			// https://github.com/antirez/redis/blob/17bf0b25c1171486e3a1b089f3181fff2bc0d4f0/src/evict.c#L349-L352
			// ... the sum of AOF and slaves buffer ....
			"mem_not_counted_for_evict":           "mem_not_counted_for_eviction_bytes",
			"mem_total_replication_buffers":       "mem_total_replication_buffers_bytes",       // Added in Redis 7.0
			"mem_overhead_db_hashtable_rehashing": "mem_overhead_db_hashtable_rehashing_bytes", // Added in Redis 7.4

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
			"async_loading":                "async_loading", // Added in Redis 7.0
			"rdb_changes_since_last_save":  "rdb_changes_since_last_save",
			"rdb_bgsave_in_progress":       "rdb_bgsave_in_progress",
			"rdb_last_save_time":           "rdb_last_save_timestamp_seconds",
			"rdb_last_bgsave_status":       "rdb_last_bgsave_status",
			"rdb_last_bgsave_time_sec":     "rdb_last_bgsave_duration_sec",
			"rdb_current_bgsave_time_sec":  "rdb_current_bgsave_duration_sec",
			"rdb_saves":                    "rdb_saves_total",
			"rdb_last_cow_size":            "rdb_last_cow_size_bytes",
			"rdb_last_load_keys_expired":   "rdb_last_load_expired_keys", // Added in Redis 7.0
			"rdb_last_load_keys_loaded":    "rdb_last_load_loaded_keys",  // Added in Redis 7.0
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
			"aof_rewrite_buffer_length":    "aof_rewrite_buffer_length", // Added in Redis 7.0
			"aof_pending_bio_fsync":        "aof_pending_bio_fsync",
			"aof_delayed_fsync":            "aof_delayed_fsync",
			"aof_last_bgrewrite_status":    "aof_last_bgrewrite_status",
			"aof_last_write_status":        "aof_last_write_status",
			"module_fork_in_progress":      "module_fork_in_progress",
			"module_fork_last_cow_size":    "module_fork_last_cow_size",

			// # Stats
			"current_eviction_exceeded_time": "current_eviction_exceeded_time_ms",
			"pubsub_channels":                "pubsub_channels",
			"pubsub_patterns":                "pubsub_patterns",
			"pubsubshard_channels":           "pubsubshard_channels", // Added in Redis 7.0.3
			"latest_fork_usec":               "latest_fork_usec",
			"tracking_total_keys":            "tracking_total_keys",
			"tracking_total_items":           "tracking_total_items",
			"tracking_total_prefixes":        "tracking_total_prefixes",

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
			"tile38_aof_size":             "tile38_aof_size_bytes",
			"tile38_avg_point_size":       "tile38_avg_item_size_bytes",
			"tile38_sys_cpus":             "tile38_cpus_total",
			"tile38_heap_released_bytes":  "tile38_heap_released_bytes",
			"tile38_heap_alloc_bytes":     "tile38_heap_size_bytes",
			"tile38_http_transport":       "tile38_http_transport",
			"tile38_in_memory_size":       "tile38_in_memory_size_bytes",
			"tile38_max_heap_size":        "tile38_max_heap_size_bytes",
			"tile38_alloc_bytes":          "tile38_mem_alloc_bytes",
			"tile38_num_collections":      "tile38_num_collections_total",
			"tile38_num_hooks":            "tile38_num_hooks_total",
			"tile38_num_objects":          "tile38_num_objects_total",
			"tile38_num_points":           "tile38_num_points_total",
			"tile38_pointer_size":         "tile38_pointer_size_bytes",
			"tile38_read_only":            "tile38_read_only",
			"tile38_go_threads":           "tile38_threads_total",
			"tile38_go_goroutines":        "tile38_go_goroutines_total",
			"tile38_last_gc_time_seconds": "tile38_last_gc_time_seconds",
			"tile38_next_gc_bytes":        "tile38_next_gc_bytes",

			// addtl. KeyDB metrics
			"server_threads":        "server_threads_total",
			"long_lock_waits":       "long_lock_waits_total",
			"current_client_thread": "current_client_thread",

			// Redis Modules metrics, RediSearch module
			"search_number_of_indexes":   "search_number_of_indexes",
			"search_used_memory_indexes": "search_used_memory_indexes_bytes",
			"search_global_idle":         "search_global_idle",
			"search_global_total":        "search_global_total",
			"search_bytes_collected":     "search_collected_bytes",
			"search_dialect_1":           "search_dialect_1",
			"search_dialect_2":           "search_dialect_2",
			"search_dialect_3":           "search_dialect_3",
			"search_dialect_4":           "search_dialect_4",
		},

		metricMapCounters: map[string]string{
			"total_connections_received": "connections_received_total",
			"total_commands_processed":   "commands_processed_total",

			"rejected_connections":   "rejected_connections_total",
			"total_net_input_bytes":  "net_input_bytes_total",
			"total_net_output_bytes": "net_output_bytes_total",

			"total_net_repl_input_bytes":  "net_repl_input_bytes_total",
			"total_net_repl_output_bytes": "net_repl_output_bytes_total",

			"expired_subkeys":                "expired_subkeys_total",
			"expired_keys":                   "expired_keys_total",
			"expired_time_cap_reached_count": "expired_time_cap_reached_total",
			"expire_cycle_cpu_milliseconds":  "expire_cycle_cpu_time_ms_total",
			"evicted_keys":                   "evicted_keys_total",
			"evicted_clients":                "evicted_clients_total", // Added in Redis 7.0
			"evicted_scripts":                "evicted_scripts_total", // Added in Redis 7.4
			"total_eviction_exceeded_time":   "eviction_exceeded_time_ms_total",
			"keyspace_hits":                  "keyspace_hits_total",
			"keyspace_misses":                "keyspace_misses_total",

			"used_cpu_sys":              "cpu_sys_seconds_total",
			"used_cpu_user":             "cpu_user_seconds_total",
			"used_cpu_sys_children":     "cpu_sys_children_seconds_total",
			"used_cpu_user_children":    "cpu_user_children_seconds_total",
			"used_cpu_sys_main_thread":  "cpu_sys_main_thread_seconds_total",
			"used_cpu_user_main_thread": "cpu_user_main_thread_seconds_total",

			"unexpected_error_replies":                  "unexpected_error_replies",
			"total_error_replies":                       "total_error_replies",
			"dump_payload_sanitizations":                "dump_payload_sanitizations",
			"total_reads_processed":                     "total_reads_processed",
			"total_writes_processed":                    "total_writes_processed",
			"io_threaded_reads_processed":               "io_threaded_reads_processed",
			"io_threaded_writes_processed":              "io_threaded_writes_processed",
			"client_query_buffer_limit_disconnections":  "client_query_buffer_limit_disconnections_total",
			"client_output_buffer_limit_disconnections": "client_output_buffer_limit_disconnections_total",
			"reply_buffer_shrinks":                      "reply_buffer_shrinks_total",
			"reply_buffer_expands":                      "reply_buffer_expands_total",
			"acl_access_denied_auth":                    "acl_access_denied_auth_total",
			"acl_access_denied_cmd":                     "acl_access_denied_cmd_total",
			"acl_access_denied_key":                     "acl_access_denied_key_total",
			"acl_access_denied_channel":                 "acl_access_denied_channel_total",

			// addtl. KeyDB metrics
			"cached_keys":                  "cached_keys_total",
			"storage_provider_read_hits":   "storage_provider_read_hits",
			"storage_provider_read_misses": "storage_provider_read_misses",

			// Redis Modules metrics, RediSearch module
			"search_total_indexing_time": "search_indexing_time_ms_total",
			"search_total_cycles":        "search_cycles_total",
			"search_total_ms_run":        "search_run_ms_total",
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

	if countKeys, err := parseKeyArg(opts.CountKeys); err != nil {
		return nil, fmt.Errorf("couldn't parse count-keys: %s", err)
	} else {
		log.Debugf("countKeys: %#v", countKeys)
	}

	if opts.InclSystemMetrics {
		e.metricMapGauges["total_system_memory"] = "total_system_memory_bytes"
	}

	e.metricDescriptions = map[string]*prometheus.Desc{}

	for k, desc := range map[string]struct {
		txt  string
		lbls []string
	}{
		"commands_duration_seconds_total":                    {txt: `Total amount of time in seconds spent per command`, lbls: []string{"cmd"}},
		"commands_failed_calls_total":                        {txt: `Total number of errors prior command execution per command`, lbls: []string{"cmd"}},
		"commands_rejected_calls_total":                      {txt: `Total number of errors within command execution per command`, lbls: []string{"cmd"}},
		"commands_total":                                     {txt: `Total number of calls per command`, lbls: []string{"cmd"}},
		"commands_latencies_usec":                            {txt: `A histogram of latencies per command`, lbls: []string{"cmd"}},
		"latency_percentiles_usec":                           {txt: `A summary of latency percentile distribution per command`, lbls: []string{"cmd"}},
		"config_client_output_buffer_limit_bytes":            {txt: `The configured buffer limits per class`, lbls: []string{"class", "limit"}},
		"config_client_output_buffer_limit_overcome_seconds": {txt: `How long for buffer limits per class to be exceeded before replicas are dropped`, lbls: []string{"class", "limit"}},
		"config_key_value":                                   {txt: `Config key and value`, lbls: []string{"key", "value"}},
		"config_value":                                       {txt: `Config key and value as metric`, lbls: []string{"key"}},
		"connected_slave_lag_seconds":                        {txt: "Lag of connected slave", lbls: []string{"slave_ip", "slave_port", "slave_state"}},
		"connected_slave_offset_bytes":                       {txt: "Offset of connected slave", lbls: []string{"slave_ip", "slave_port", "slave_state"}},
		"db_avg_ttl_seconds":                                 {txt: "Avg TTL in seconds", lbls: []string{"db"}},
		"db_keys":                                            {txt: "Total number of keys by DB", lbls: []string{"db"}},
		"db_keys_expiring":                                   {txt: "Total number of expiring keys by DB", lbls: []string{"db"}},
		"db_keys_cached":                                     {txt: "Total number of cached keys by DB", lbls: []string{"db"}},
		"errors_total":                                       {txt: `Total number of errors per error type`, lbls: []string{"err"}},
		"exporter_last_scrape_error":                         {txt: "The last scrape error status.", lbls: []string{"err"}},
		"instance_info":                                      {txt: "Information about the Redis instance", lbls: []string{"role", "redis_version", "redis_build_id", "redis_mode", "os", "maxmemory_policy", "tcp_port", "run_id", "process_id", "master_replid"}},
		"key_group_count":                                    {txt: `Count of keys in key group`, lbls: []string{"db", "key_group"}},
		"key_group_memory_usage_bytes":                       {txt: `Total memory usage of key group in bytes`, lbls: []string{"db", "key_group"}},
		"key_size":                                           {txt: `The length or size of "key"`, lbls: []string{"db", "key"}},
		"key_value":                                          {txt: `The value of "key"`, lbls: []string{"db", "key"}},
		"key_value_as_string":                                {txt: `The value of "key" as a string`, lbls: []string{"db", "key", "val"}},
		"keys_count":                                         {txt: `Count of keys`, lbls: []string{"db", "key"}},
		"last_key_groups_scrape_duration_milliseconds":       {txt: `Duration of the last key group metrics scrape in milliseconds`},
		"last_slow_execution_duration_seconds":               {txt: `The amount of time needed for last slow execution, in seconds`},
		"latency_spike_duration_seconds":                     {txt: `Length of the last latency spike in seconds`, lbls: []string{"event_name"}},
		"latency_spike_last":                                 {txt: `When the latency spike last occurred`, lbls: []string{"event_name"}},
		"master_last_io_seconds_ago":                         {txt: "Master last io seconds ago", lbls: []string{"master_host", "master_port"}},
		"master_link_up":                                     {txt: "Master link status on Redis slave", lbls: []string{"master_host", "master_port"}},
		"master_sync_in_progress":                            {txt: "Master sync in progress", lbls: []string{"master_host", "master_port"}},
		"number_of_distinct_key_groups":                      {txt: `Number of distinct key groups`, lbls: []string{"db"}},
		"script_result":                                      {txt: "Result of the collect script evaluation", lbls: []string{"filename"}},
		"script_values":                                      {txt: "Values returned by the collect script", lbls: []string{"key", "filename"}},
		"sentinel_master_ok_sentinels":                       {txt: "The number of okay sentinels monitoring this master", lbls: []string{"master_name", "master_address"}},
		"sentinel_master_ok_slaves":                          {txt: "The number of okay slaves of the master", lbls: []string{"master_name", "master_address"}},
		"sentinel_master_sentinels":                          {txt: "The number of sentinels monitoring this master", lbls: []string{"master_name", "master_address"}},
		"sentinel_master_slaves":                             {txt: "The number of slaves of the master", lbls: []string{"master_name", "master_address"}},
		"sentinel_master_status":                             {txt: "Master status on Sentinel", lbls: []string{"master_name", "master_address", "master_status"}},
		"sentinel_master_ckquorum_status":                    {txt: "Master ckquorum status", lbls: []string{"master_name", "message"}},
		"sentinel_masters":                                   {txt: "The number of masters this sentinel is watching"},
		"sentinel_master_setting_ckquorum":                   {txt: "Show the current ckquorum config for each master", lbls: []string{"master_name", "master_address"}},
		"sentinel_master_setting_failover_timeout":           {txt: "Show the current failover-timeout config for each master", lbls: []string{"master_name", "master_address"}},
		"sentinel_master_setting_parallel_syncs":             {txt: "Show the current parallel-syncs config for each master", lbls: []string{"master_name", "master_address"}},
		"sentinel_master_setting_down_after_milliseconds":    {txt: "Show the current down-after-milliseconds config for each master", lbls: []string{"master_name", "master_address"}},
		"sentinel_running_scripts":                           {txt: "Number of scripts in execution right now"},
		"sentinel_scripts_queue_length":                      {txt: "Queue of user scripts to execute"},
		"sentinel_simulate_failure_flags":                    {txt: "Failures simulations"},
		"sentinel_tilt":                                      {txt: "Sentinel is in TILT mode"},
		"slave_info":                                         {txt: "Information about the Redis slave", lbls: []string{"master_host", "master_port", "read_only"}},
		"slave_repl_offset":                                  {txt: "Slave replication offset", lbls: []string{"master_host", "master_port"}},
		"slowlog_last_id":                                    {txt: `Last id of slowlog`},
		"slowlog_length":                                     {txt: `Total slowlog`},
		"start_time_seconds":                                 {txt: "Start time of the Redis instance since unix epoch in seconds."},
		"stream_group_consumer_idle_seconds":                 {txt: `Consumer idle time in seconds`, lbls: []string{"db", "stream", "group", "consumer"}},
		"stream_group_consumer_messages_pending":             {txt: `Pending number of messages for this specific consumer`, lbls: []string{"db", "stream", "group", "consumer"}},
		"stream_group_consumers":                             {txt: `Consumers count of stream group`, lbls: []string{"db", "stream", "group"}},
		"stream_group_entries_read":                          {txt: `Total number of entries read from the stream group`, lbls: []string{"db", "stream", "group"}},
		"stream_group_lag":                                   {txt: `The number of messages waiting to be delivered to the stream group's consumers`, lbls: []string{"db", "stream", "group"}},
		"stream_group_last_delivered_id":                     {txt: `The epoch timestamp (ms) of the last delivered message`, lbls: []string{"db", "stream", "group"}},
		"stream_group_messages_pending":                      {txt: `Pending number of messages in that stream group`, lbls: []string{"db", "stream", "group"}},
		"stream_groups":                                      {txt: `Groups count of stream`, lbls: []string{"db", "stream"}},
		"stream_last_generated_id":                           {txt: `The epoch timestamp (ms) of the latest message on the stream`, lbls: []string{"db", "stream"}},
		"stream_length":                                      {txt: `The number of elements of the stream`, lbls: []string{"db", "stream"}},
		"stream_max_deleted_entry_id":                        {txt: `The epoch timestamp (ms) of last message was deleted from the stream`, lbls: []string{"db", "stream"}},
		"stream_first_entry_id":                              {txt: `The epoch timestamp (ms) of the first message in the stream`, lbls: []string{"db", "stream"}},
		"stream_last_entry_id":                               {txt: `The epoch timestamp (ms) of the last message in the stream`, lbls: []string{"db", "stream"}},
		"stream_radix_tree_keys":                             {txt: `Radix tree keys count"`, lbls: []string{"db", "stream"}},
		"stream_radix_tree_nodes":                            {txt: `Radix tree nodes count`, lbls: []string{"db", "stream"}},
		"up":                                                 {txt: "Information about the Redis instance"},
		"module_info":                                        {txt: "Information about loaded Redis module", lbls: []string{"name", "ver", "api", "filters", "usedby", "using"}},
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
			buildInfoCollector := prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Namespace: opts.Namespace,
				Name:      "exporter_build_info",
				Help:      "redis exporter build_info",
			}, []string{"version", "commit_sha", "build_date", "golang_version"})
			buildInfoCollector.WithLabelValues(e.buildInfo.Version, e.buildInfo.CommitSha, e.buildInfo.Date, runtime.Version()).Set(1)
			e.options.Registry.MustRegister(buildInfoCollector)
		}
	}

	e.mux.HandleFunc("/", e.indexHandler)
	e.mux.HandleFunc("/scrape", e.scrapeHandler)
	e.mux.HandleFunc("/health", e.healthHandler)
	e.mux.HandleFunc("/-/reload", e.reloadPwdFile)

	return e, nil
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
		var up float64
		if err := e.scrapeRedisHost(ch); err != nil {
			e.registerConstMetricGauge(ch, "exporter_last_scrape_error", 1.0, fmt.Sprintf("%s", err))
		} else {
			up = 1
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

func (e *Exporter) extractConfigMetrics(ch chan<- prometheus.Metric, config []interface{}) (dbCount int, err error) {
	if len(config)%2 != 0 {
		return 0, fmt.Errorf("invalid config: %#v", config)
	}

	for pos := 0; pos < len(config)/2; pos++ {
		strKey, err := redis.String(config[pos*2], nil)
		if err != nil {
			log.Errorf("invalid config key name, err: %s, skipped", err)
			continue
		}

		strVal, err := redis.String(config[pos*2+1], nil)
		if err != nil {
			log.Debugf("invalid config value for key name %s, err: %s, skipped", strKey, err)
			continue
		}

		if strKey == "databases" {
			if dbCount, err = strconv.Atoi(strVal); err != nil {
				return 0, fmt.Errorf("invalid config value for key databases: %#v", strVal)
			}
		}

		if e.options.InclConfigMetrics {
			if redact := map[string]bool{
				"masterauth":               true,
				"requirepass":              true,
				"tls-key-file-pass":        true,
				"tls-client-key-file-pass": true,
			}[strKey]; !redact || !e.options.RedactConfigMetrics {
				e.registerConstMetricGauge(ch, "config_key_value", 1.0, strKey, strVal)
				if val, err := strconv.ParseFloat(strVal, 64); err == nil {
					e.registerConstMetricGauge(ch, "config_value", val, strKey)
				}
			}
		}

		if map[string]bool{
			"io-threads": true,
			"maxclients": true,
			"maxmemory":  true,
		}[strKey] {
			if val, err := strconv.ParseFloat(strVal, 64); err == nil {
				strKey = strings.ReplaceAll(strKey, "-", "_")
				e.registerConstMetricGauge(ch, fmt.Sprintf("config_%s", strKey), val)
			}
		}

		if strKey == "client-output-buffer-limit" {
			// client-output-buffer-limit "normal 0 0 0 slave 1610612736 1610612736 0 pubsub 33554432 8388608 60"
			splitVal := strings.Split(strVal, " ")
			for i := 0; i < len(splitVal); i += 4 {
				class := splitVal[i]
				if val, err := strconv.ParseFloat(splitVal[i+1], 64); err == nil {
					e.registerConstMetricGauge(ch, "config_client_output_buffer_limit_bytes", val, class, "hard")
				}
				if val, err := strconv.ParseFloat(splitVal[i+2], 64); err == nil {
					e.registerConstMetricGauge(ch, "config_client_output_buffer_limit_bytes", val, class, "soft")
				}
				if val, err := strconv.ParseFloat(splitVal[i+3], 64); err == nil {
					e.registerConstMetricGauge(ch, "config_client_output_buffer_limit_overcome_seconds", val, class, "soft")
				}
			}
		}
	}
	return
}

func (e *Exporter) scrapeRedisHost(ch chan<- prometheus.Metric) error {
	defer log.Debugf("scrapeRedisHost() done")

	startTime := time.Now()
	c, err := e.connectToRedis()
	connectTookSeconds := time.Since(startTime).Seconds()
	e.registerConstMetricGauge(ch, "exporter_last_scrape_connect_time_seconds", connectTookSeconds)

	if err != nil {
		var redactedAddr string
		if redisURL, err2 := url.Parse(e.redisAddr); err2 != nil {
			log.Debugf("url.Parse( %s ) err: %s", e.redisAddr, err2)
			redactedAddr = "<redacted>"
		} else {
			redactedAddr = redisURL.Redacted()
		}
		log.Errorf("Couldn't connect to redis instance (%s)", redactedAddr)
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
	if e.options.ConfigCommandName == "-" {
		log.Debugf("Skipping extractConfigMetrics()")
	} else {
		if config, err := redis.Values(doRedisCmd(c, e.options.ConfigCommandName, "GET", "*")); err == nil {
			dbCount, err = e.extractConfigMetrics(ch, config)
			if err != nil {
				log.Errorf("Redis extractConfigMetrics() err: %s", err)
				return err
			}
		} else {
			log.Debugf("Redis CONFIG err: %s", err)
		}
	}

	infoAll, err := redis.String(doRedisCmd(c, "INFO", "ALL"))
	if err != nil || infoAll == "" {
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

	role := e.extractInfoMetrics(ch, infoAll, dbCount)

	if !e.options.ExcludeLatencyHistogramMetrics {
		e.extractLatencyMetrics(ch, infoAll, c)
	}

	// skip for master?
	// (can help with reducing work load on the master node)
	if role != InstanceRoleSlave || e.options.SkipCheckKeysForRoleMaster {
		if err := e.extractCheckKeyMetrics(ch, c); err != nil {
			log.Errorf("extractCheckKeyMetrics() err: %s", err)
		}
	}

	e.extractSlowLogMetrics(ch, c)

	e.extractStreamMetrics(ch, c)

	e.extractCountKeysMetrics(ch, c)

	e.extractKeyGroupMetrics(ch, c, dbCount)

	if strings.Contains(infoAll, "# Sentinel") {
		e.extractSentinelMetrics(ch, c)
	}

	if e.options.ExportClientList {
		e.extractConnectedClientMetrics(ch, c)
	}

	if e.options.IsTile38 {
		e.extractTile38Metrics(ch, c)
	}

	if e.options.InclModulesMetrics {
		e.extractModulesMetrics(ch, c)
	}

	if len(e.options.LuaScript) > 0 {
		for filename, script := range e.options.LuaScript {
			if err := e.extractLuaScriptMetrics(ch, c, filename, script); err != nil {
				return err
			}
		}
	}

	return nil
}
