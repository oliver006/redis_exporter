package exporter

/*
  to run the tests with redis running on anything but localhost:6379 use
  $ go test   --redis.addr=<host>:<port>

  for html coverage report run
  $ go test -coverprofile=coverage.out  && go tool cover -html=coverage.out
*/

import (
	"fmt"
	"github.com/mna/redisc"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	log "github.com/sirupsen/logrus"
)

const (
	dbNumStr        = "11"
	altDBNumStr     = "12"
	invalidDBNumStr = "16"

	anotherAltDbNumStr = "14"
)

const (
	TestKeysSetName    = "test-set"
	TestKeysZSetName   = "test-zset"
	TestKeysStreamName = "test-stream"
	TestKeysHllName    = "test-hll"
	TestKeysHashName   = "test-hash"
	TestKeyGroup1      = "test_group_1"
	TestKeyGroup2      = "test_group_2"
)

var (
	AllTestKeys = []string{
		TestKeysSetName, TestKeysZSetName,
		TestKeysStreamName,
		TestKeysHllName, TestKeysHashName,
		TestKeyGroup1, TestKeyGroup2,
	}
)

var (
	testKeys         []string
	testKeysExpiring []string
	testKeysList     []string

	dbNumStrFull = fmt.Sprintf("db%s", dbNumStr)
)

var (
	TestKeyNameSingleString = "" // initialized with a timestamp at runtime
	TestKeyNameSet          = "test-set"
	TestKeyNameStream       = "test-stream"
	TestKeyNameHll          = "test-hll"
)

func getTestExporter() *Exporter {
	return getTestExporterWithOptions(Options{Namespace: "test"})
}

func getTestExporterWithOptions(opt Options) *Exporter {
	addr := os.Getenv("TEST_REDIS_URI")
	if addr == "" {
		panic("missing env var TEST_REDIS_URI")
	}
	e, _ := NewRedisExporter(addr, opt)
	return e
}

func getTestExporterWithAddr(addr string) *Exporter {
	e, _ := NewRedisExporter(addr, Options{Namespace: "test"})
	return e
}

func getTestExporterWithAddrAndOptions(addr string, opt Options) *Exporter {
	e, _ := NewRedisExporter(addr, opt)
	return e
}

func setupKeys(t *testing.T, c redis.Conn, dbNum string) error {
	if _, err := doRedisCmd(c, "SELECT", dbNum); err != nil {
		// not failing on this one - cluster doesn't allow for SELECT so we log and ignore the error
		log.Printf("setupTestKeys() - couldn't setup redis, err: %s ", err)
	}

	testValue := 1234.56
	for _, key := range testKeys {
		if _, err := doRedisCmd(c, "SET", key, testValue); err != nil {
			t.Errorf("couldn't setup redis, err: %s ", err)
			return err
		}
	}

	// set to expire in 600 seconds, should be plenty for a test run
	for _, key := range testKeysExpiring {
		if _, err := doRedisCmd(c, "SETEX", key, "600", testValue); err != nil {
			t.Errorf("couldn't setup redis, err: %s ", err)
			return err
		}
	}

	for _, key := range testKeysList {
		for _, val := range testKeys {
			if _, err := doRedisCmd(c, "LPUSH", key, val); err != nil {
				t.Errorf("couldn't setup redis, err: %s ", err)
				return err
			}
		}
	}

	if _, err := c.Do("PFADD", TestKeyNameHll, "val1"); err != nil {
		t.Errorf("PFADD err: %s", err)
		return err
	}
	if _, err := c.Do("PFADD", TestKeyNameHll, "val22"); err != nil {
		t.Errorf("PFADD err: %s", err)
		return err
	}
	if _, err := c.Do("PFADD", TestKeyNameHll, "val333"); err != nil {
		t.Errorf("PFADD err: %s", err)
		return err
	}

	if _, err := c.Do("SADD", TestKeyNameSet, "test-val-1"); err != nil {
		t.Errorf("SADD err: %s", err)
		return err
	}
	if _, err := c.Do("SADD", TestKeyNameSet, "test-val-2"); err != nil {
		t.Errorf("SADD err: %s", err)
		return err
	}

	if _, err := c.Do("SET", TestKeyNameSingleString, "this-is-a-string"); err != nil {
		t.Errorf("PFADD err: %s", err)
		return err
	}
	if _, err := doRedisCmd(c, "ZADD", TestKeysZSetName, "23", "test-zzzval-2"); err != nil {
		t.Errorf("ZADD err: %s", err)
		return err
	}
	if _, err := doRedisCmd(c, "ZADD", TestKeysZSetName, "45", "test-zzzval-3"); err != nil {
		t.Errorf("ZADD err: %s", err)
		return err
	}

	if _, err := doRedisCmd(c, "SET", TestKeyNameSingleString, "this-is-a-string"); err != nil {
		t.Errorf("SET %s err: %s", TestKeyNameSingleString, err)
		return err
	}

	if _, err := doRedisCmd(c, "HSET", TestKeysHashName, "field1", "Hello"); err != nil {
		t.Errorf("HSET err: %s", err)
		return err
	}
	if _, err := doRedisCmd(c, "HSET", TestKeysHashName, "field2", "World"); err != nil {
		t.Errorf("HSET err: %s", err)
		return err
	}
	if _, err := doRedisCmd(c, "HSET", TestKeysHashName, "field3", "What's"); err != nil {
		t.Errorf("HSET err: %s", err)
		return err
	}
	if _, err := doRedisCmd(c, "HSET", TestKeysHashName, "field4", "new?"); err != nil {
		t.Errorf("HSET err: %s", err)
		return err
	}

	if x, err := redis.String(doRedisCmd(c, "HGET", TestKeysHashName, "field4")); err != nil || x != "new?" {
		t.Errorf("HGET %s err: %s  x: %s", TestKeysHashName, err, x)
	}

	// Create test streams
	c.Do("XGROUP", "CREATE", TestKeyNameStream, "test_group_1", "$", "MKSTREAM")
	c.Do("XGROUP", "CREATE", TestKeyNameStream, "test_group_2", "$", "MKSTREAM")
	c.Do("XADD", TestKeyNameStream, TestStreamTimestamps[0], "field_1", "str_1")
	c.Do("XADD", TestKeyNameStream, TestStreamTimestamps[1], "field_2", "str_2")

	// Process messages to assign Consumers to their groups
	c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_1", "COUNT", "1", "STREAMS", TestKeyNameStream, ">")
	c.Do("XREADGROUP", "GROUP", "test_group_1", "test_consumer_2", "COUNT", "1", "STREAMS", TestKeyNameStream, ">")
	c.Do("XREADGROUP", "GROUP", "test_group_2", "test_consumer_1", "COUNT", "1", "STREAMS", TestKeyNameStream, "0")

	time.Sleep(time.Millisecond * 100)
	return nil
}

func deleteKeys(c redis.Conn, dbNum string) {
	if _, err := doRedisCmd(c, "SELECT", dbNum); err != nil {
		log.Printf("deleteTestKeys() - couldn't setup redis, err: %s ", err)
		// not failing on this one - cluster doesn't allow for SELECT so we log and ignore the error
	}

	for _, key := range AllTestKeys {
		doRedisCmd(c, "DEL", key)
	}

	for _, key := range testKeysExpiring {
		c.Do("DEL", key)
	}

	for _, key := range testKeysList {
		c.Do("DEL", key)
	}

	c.Do("DEL", TestKeyNameHll)
	c.Do("DEL", TestKeyNameSet)
	c.Do("DEL", TestKeyNameStream)
	c.Do("DEL", TestKeyNameSingleString)
}

func setupTestKeys(t *testing.T, uri string) {
	log.Debugf("setupTestKeys uri: %s", uri)
	c, err := redis.DialURL(uri)
	if err != nil {
		t.Fatalf("couldn't setup redis for uri %s, err: %s ", uri, err)
		return
	}
	defer c.Close()

	if err := setupKeys(t, c, dbNumStr); err != nil {
		t.Fatalf("couldn't setup test keys, err: %s ", err)
	}
	if err := setupKeys(t, c, altDBNumStr); err != nil {
		t.Fatalf("couldn't setup test keys, err: %s ", err)
	}
	if err := setupKeys(t, c, anotherAltDbNumStr); err != nil {
		t.Fatalf("couldn't setup test keys, err: %s ", err)
	}
}

func setupTestKeysCluster(t *testing.T, uri string) {
	log.Debugf("Creating cluster object")
	cluster := redisc.Cluster{
		StartupNodes: []string{
			strings.Replace(uri, "redis://", "", 1),
		},
		DialOptions: []redis.DialOption{},
	}

	if err := cluster.Refresh(); err != nil {
		log.Fatalf("Refresh failed: %v", err)
	}

	conn, err := cluster.Dial()
	if err != nil {
		log.Errorf("Dial() failed: %v", err)
	}

	c, err := redisc.RetryConn(conn, 10, 100*time.Millisecond)
	if err != nil {
		log.Errorf("RetryConn() failed: %v", err)
	}

	// cluster only supports db==0
	if err := setupKeys(t, c, "0"); err != nil {
		t.Fatalf("couldn't setup test keys, err: %s ", err)
		return
	}

	time.Sleep(time.Second)

	if x, err := redis.Strings(doRedisCmd(c, "KEYS", "*")); err != nil {
		t.Errorf("KEYS * err: %s", err)
	} else {
		t.Logf("KEYS * -> %#v", x)
	}
}

func deleteTestKeys(t *testing.T, addr string) error {
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}
	defer c.Close()

	deleteKeys(c, dbNumStr)
	deleteKeys(c, altDBNumStr)
	deleteKeys(c, anotherAltDbNumStr)

	return nil
}

func deleteTestKeysCluster(t *testing.T, addr string) error {
	e, _ := NewRedisExporter(addr, Options{})
	c, err := e.connectToRedisCluster()
	if err != nil {
		t.Errorf("couldn't setup redis CLUSTER, err: %s ", err)
		return err
	}

	defer c.Close()

	// cluster only supports db==0
	deleteKeys(c, "0")

	return nil
}

func TestIncludeSystemMemoryMetric(t *testing.T) {
	for _, inc := range []bool{false, true} {
		e, _ := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), Options{Namespace: "test", InclSystemMetrics: inc})
		ts := httptest.NewServer(e)

		body := downloadURL(t, ts.URL+"/metrics")
		if inc && !strings.Contains(body, "total_system_memory_bytes") {
			t.Errorf("want metrics to include total_system_memory_bytes, have:\n%s", body)
		} else if !inc && strings.Contains(body, "total_system_memory_bytes") {
			t.Errorf("did NOT want metrics to include total_system_memory_bytes, have:\n%s", body)
		}

		ts.Close()
	}
}

func TestIncludeConfigMetrics(t *testing.T) {
	for _, inc := range []bool{false, true} {
		e, _ := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), Options{Namespace: "test", InclConfigMetrics: inc})
		ts := httptest.NewServer(e)

		what := `test_config_key_value{key="appendonly",value="no"}`

		body := downloadURL(t, ts.URL+"/metrics")
		if inc && !strings.Contains(body, what) {
			t.Errorf("want metrics to include test_config_key_value, have:\n%s", body)
		} else if !inc && strings.Contains(body, what) {
			t.Errorf("did NOT want metrics to include test_config_key_value, have:\n%s", body)
		}

		ts.Close()
	}
}

func TestClientOutputBufferLimitMetrics(t *testing.T) {
	for _, class := range []string{
		`normal`,
		`pubsub`,
		`slave`,
	} {
		for _, limit := range []string{
			`hard`,
			`soft`,
		} {
			want := fmt.Sprintf("%s{class=\"%s\",limit=\"%s\"}", "config_client_output_buffer_limit_bytes", class, limit)
			e, _ := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), Options{Namespace: "test"})
			ts := httptest.NewServer(e)

			body := downloadURL(t, ts.URL+"/metrics")

			if !strings.Contains(body, want) {
				t.Errorf("want metrics to include %s, have:\n%s", want, body)
			}
		}

		want := fmt.Sprintf("%s{class=\"%s\",limit=\"soft\"}", "config_client_output_buffer_limit_overcome_seconds", class)
		e, _ := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), Options{Namespace: "test"})
		ts := httptest.NewServer(e)

		body := downloadURL(t, ts.URL+"/metrics")

		if !strings.Contains(body, want) {
			t.Errorf("want metrics to include %s, have:\n%s", want, body)
		}
	}
}

func TestExcludeConfigMetricsViaCONFIGCommand(t *testing.T) {
	for _, inc := range []bool{false, true} {
		e, _ := NewRedisExporter(os.Getenv("TEST_REDIS_URI"),
			Options{
				Namespace:         "test",
				ConfigCommandName: "-",
				InclConfigMetrics: inc})
		ts := httptest.NewServer(e)

		what := `test_config_key_value{key="appendonly",value="no"}`

		body := downloadURL(t, ts.URL+"/metrics")
		if strings.Contains(body, what) {
			t.Fatalf("found test_config_key_value but should have skipped CONFIG call")
		}

		ts.Close()
	}
}

func TestNonExistingHost(t *testing.T) {
	e, _ := NewRedisExporter("unix:///tmp/doesnt.exist", Options{Namespace: "test"})

	chM := make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	want := map[string]float64{"test_exporter_last_scrape_error": 1.0, "test_exporter_scrapes_total": 1.0}

	for m := range chM {
		descString := m.Desc().String()
		for k := range want {
			if strings.Contains(descString, k) {
				g := &dto.Metric{}
				m.Write(g)
				val := 0.0

				if g.GetGauge() != nil {
					val = *g.GetGauge().Value
				} else if g.GetCounter() != nil {
					val = *g.GetCounter().Value
				} else {
					continue
				}

				if val == want[k] {
					want[k] = -1.0
				}
			}
		}
	}
	for k, v := range want {
		if v > 0 {
			t.Errorf("didn't find %s", k)
		}
	}
}

func TestKeysReset(t *testing.T) {
	e, _ := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), Options{Namespace: "test", CheckSingleKeys: dbNumStrFull + "=" + testKeys[0]})
	ts := httptest.NewServer(e)
	defer ts.Close()

	setupTestKeys(t, os.Getenv("TEST_REDIS_URI"))
	defer deleteTestKeys(t, os.Getenv("TEST_REDIS_URI"))

	body := downloadURL(t, ts.URL+"/metrics")
	if !strings.Contains(body, testKeys[0]) {
		t.Errorf("Did not find key %q\n%s", testKeys[0], body)
	}

	deleteTestKeys(t, os.Getenv("TEST_REDIS_URI"))

	body = downloadURL(t, ts.URL+"/metrics")
	if !strings.Contains(body, testKeys[0]) {
		t.Errorf("Key %q (from check-single-keys) should be available in metrics with default value 0\n%s", testKeys[0], body)
	}
}

func TestRedisMetricsOnly(t *testing.T) {
	for _, inc := range []bool{false, true} {
		r := prometheus.NewRegistry()
		e, err := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), Options{Namespace: "test", Registry: r, RedisMetricsOnly: inc})
		if err != nil {
			t.Fatalf(`error when creating exporter with registry: %s`, err)
		}
		ts := httptest.NewServer(e)

		body := downloadURL(t, ts.URL+"/metrics")
		if inc && strings.Contains(body, "exporter_build_info") {
			t.Errorf("want metrics to include exporter_build_info, have:\n%s", body)
		} else if !inc && !strings.Contains(body, "exporter_build_info") {
			t.Errorf("did NOT want metrics to include exporter_build_info, have:\n%s", body)
		}

		ts.Close()
	}
}

func TestConnectionDurations(t *testing.T) {
	metric1 := "exporter_last_scrape_ping_time_seconds"
	metric2 := "exporter_last_scrape_connect_time_seconds"

	for _, inclPing := range []bool{false, true} {
		e, _ := NewRedisExporter(os.Getenv("TEST_REDIS_URI"), Options{Namespace: "test", PingOnConnect: inclPing})
		ts := httptest.NewServer(e)

		body := downloadURL(t, ts.URL+"/metrics")
		if inclPing && !strings.Contains(body, metric1) {
			t.Fatalf("want metrics to include %s, have:\n%s", metric1, body)
		} else if !inclPing && strings.Contains(body, metric1) {
			t.Fatalf("did NOT want metrics to include %s, have:\n%s", metric1, body)
		}

		// always expect this one
		if !strings.Contains(body, metric2) {
			t.Fatalf("want metrics to include %s, have:\n%s", metric2, body)
		}
		ts.Close()
	}
}

func TestKeyDbMetrics(t *testing.T) {
	if os.Getenv("TEST_KEYDB01_URI") == "" {
		t.Skipf("Skipping due to missing TEST_KEYDB01_URI")
	}

	setupTestKeys(t, os.Getenv("TEST_KEYDB01_URI"))
	defer deleteTestKeys(t, os.Getenv("TEST_KEYDB01_URI"))

	for _, want := range []string{
		`test_db_keys_cached`,
		`test_storage_provider_read_hits`,
	} {
		e, _ := NewRedisExporter(os.Getenv("TEST_KEYDB01_URI"), Options{Namespace: "test"})
		ts := httptest.NewServer(e)

		body := downloadURL(t, ts.URL+"/metrics")
		if !strings.Contains(body, want) {
			t.Errorf("want metrics to include %s, have:\n%s", want, body)
		}

		ts.Close()
	}
}

func TestExtractInfoMetrics(t *testing.T) {
	infoStr := `
# Server
redis_version:7.4.0
redis_git_sha1:00000000
redis_git_dirty:0
redis_build_id:ca02b5915bf0d9c6
redis_mode:standalone
os:Linux 5.10.0-32-cloud-amd64 x86_64
arch_bits:64
monotonic_clock:POSIX clock_gettime
multiplexing_api:epoll
atomicvar_api:c11-builtin
gcc_version:10.2.1
process_id:11561
process_supervised:systemd
run_id:d57a856ea44f5252ccca5c6eef5662ee44a22642
tcp_port:6379
server_time_usec:1730045798756384
uptime_in_seconds:3503939
uptime_in_days:40
hz:10
configured_hz:10
lru_clock:1992550
executable:/usr/bin/redis-server
config_file:/etc/redis/redis.conf
io_threads_active:0
listener0:name=tcp,bind=*,bind=-::*,port=6379

# Clients
connected_clients:536
cluster_connections:0
maxclients:10000
client_recent_max_input_buffer:229376
client_recent_max_output_buffer:40984
blocked_clients:10
tracking_clients:0
pubsub_clients:2
watching_clients:0
clients_in_timeout_table:10
total_watched_keys:0
total_blocking_keys:2
total_blocking_keys_on_nokey:0

# Memory
used_memory:27514219712
used_memory_human:25.62G
used_memory_rss:26951938048
used_memory_rss_human:25.10G
used_memory_peak:28271280688
used_memory_peak_human:26.33G
used_memory_peak_perc:97.32%
used_memory_overhead:2157727822
used_memory_startup:946424
used_memory_dataset:25356491890
used_memory_dataset_perc:92.16%
allocator_allocated:27515638760
allocator_active:27885133824
allocator_resident:28144386048
allocator_muzzy:0
total_system_memory:33672261632
total_system_memory_human:31.36G
used_memory_lua:120832
used_memory_vm_eval:120832
used_memory_lua_human:118.00K
used_memory_scripts_eval:64328
number_of_cached_scripts:12
number_of_functions:0
number_of_libraries:0
used_memory_vm_functions:32768
used_memory_vm_total:153600
used_memory_vm_total_human:150.00K
used_memory_functions:296
used_memory_scripts:64624
used_memory_scripts_human:63.11K
maxmemory:0
maxmemory_human:0B
maxmemory_policy:noeviction
allocator_frag_ratio:1.01
allocator_frag_bytes:369367384
allocator_rss_ratio:1.01
allocator_rss_bytes:259252224
rss_overhead_ratio:0.96
rss_overhead_bytes:-1192448000
mem_fragmentation_ratio:0.98
mem_fragmentation_bytes:-562326136
mem_not_counted_for_evict:78480
mem_replication_backlog:1048592
mem_total_replication_buffers:1131216
mem_clients_slaves:82640
mem_clients_normal:3446942
mem_cluster_links:0
mem_aof_buffer:0
mem_allocator:jemalloc-5.3.0
mem_overhead_db_hashtable_rehashing:0
active_defrag_running:0
lazyfree_pending_objects:0
lazyfreed_objects:0

# Persistence
loading:0
async_loading:0
current_cow_peak:7811072
current_cow_size:7811072
current_cow_size_age:56
current_fork_perc:24.95
current_save_keys_processed:6354945
current_save_keys_total:25471640
rdb_changes_since_last_save:26716
rdb_bgsave_in_progress:1
rdb_last_save_time:1730045679
rdb_last_bgsave_status:ok
rdb_last_bgsave_time_sec:220
rdb_current_bgsave_time_sec:57
rdb_saves:7695
rdb_last_cow_size:162373632
rdb_last_load_keys_expired:0
rdb_last_load_keys_loaded:17485901
aof_enabled:0
aof_rewrite_in_progress:0
aof_rewrite_scheduled:0
aof_last_rewrite_time_sec:-1
aof_current_rewrite_time_sec:-1
aof_last_bgrewrite_status:ok
aof_rewrites:0
aof_rewrites_consecutive_failures:0
aof_last_write_status:ok
aof_last_cow_size:0
module_fork_in_progress:0
module_fork_last_cow_size:0

# Stats
total_connections_received:8428652
total_commands_processed:361818968
instantaneous_ops_per_sec:238
total_net_input_bytes:143297336251
total_net_output_bytes:1671966520991
total_net_repl_input_bytes:13003333819
total_net_repl_output_bytes:1598215288158
instantaneous_input_kbps:201.32
instantaneous_output_kbps:448.47
instantaneous_input_repl_kbps:0.00
instantaneous_output_repl_kbps:384.88
rejected_connections:0
sync_full:766
sync_partial_ok:0
sync_partial_err:766
expired_subkeys:0
expired_keys:16645002
expired_stale_perc:3.83
expired_time_cap_reached_count:0
expire_cycle_cpu_milliseconds:1975019
evicted_keys:0
evicted_clients:0
evicted_scripts:0
total_eviction_exceeded_time:0
current_eviction_exceeded_time:0
keyspace_hits:99218277
keyspace_misses:8044632
pubsub_channels:1
pubsub_patterns:0
pubsubshard_channels:0
latest_fork_usec:1027383
total_forks:8441
migrate_cached_sockets:0
slave_expires_tracked_keys:0
active_defrag_hits:0
active_defrag_misses:0
active_defrag_key_hits:0
active_defrag_key_misses:0
total_active_defrag_time:0
current_active_defrag_time:0
tracking_total_keys:0
tracking_total_items:0
tracking_total_prefixes:0
unexpected_error_replies:0
total_error_replies:855
dump_payload_sanitizations:0
total_reads_processed:219947919
total_writes_processed:381447627
io_threaded_reads_processed:0
io_threaded_writes_processed:0
client_query_buffer_limit_disconnections:0
client_output_buffer_limit_disconnections:21
reply_buffer_shrinks:8569188
reply_buffer_expands:9175893
eventloop_cycles:331700552
eventloop_duration_sum:59371056253
eventloop_duration_cmd_sum:5979133498
instantaneous_eventloop_cycles_per_sec:249
instantaneous_eventloop_duration_usec:141
acl_access_denied_auth:0
acl_access_denied_cmd:0
acl_access_denied_key:0
acl_access_denied_channel:0

# Replication
role:master
connected_slaves:2
slave0:ip=192.168.150.63,port=6379,state=online,offset=127763393630,lag=0
slave1:ip=192.168.150.89,port=6379,state=online,offset=127763585576,lag=0
master_failover_state:no-failover
master_replid:483dbe4b878ee700cd76746334c9c3303733adc4
master_replid2:d0c740965b559c71e64097bbe6597ad1ed64c284
master_repl_offset:127763635259
second_repl_offset:9051173903
repl_backlog_active:1
repl_backlog_size:1048576
repl_backlog_first_byte_offset:127762511837
repl_backlog_histlen:1123423

# CPU
used_cpu_sys:31391.772752
used_cpu_user:35173.231869
used_cpu_sys_children:168716.402404
used_cpu_user_children:734155.152644
used_cpu_sys_main_thread:31336.128033
used_cpu_user_main_thread:35141.918028

# Modules

# Commandstats
cmdstat_cluster|info:calls=2,usec=91,usec_per_call=45.50,rejected_calls=0,failed_calls=2
cmdstat_publish:calls=8330832,usec=161648765,usec_per_call=19.40,rejected_calls=0,failed_calls=0
cmdstat_unlink:calls=86169,usec=286114,usec_per_call=3.32,rejected_calls=0,failed_calls=0
cmdstat_del:calls=20582552,usec=808488405,usec_per_call=39.28,rejected_calls=0,failed_calls=0
cmdstat_set:calls=3805683,usec=64899770,usec_per_call=17.05,rejected_calls=0,failed_calls=0
cmdstat_hset:calls=290626,usec=6289627,usec_per_call=21.64,rejected_calls=0,failed_calls=0
cmdstat_slowlog|len:calls=408791,usec=806454,usec_per_call=1.97,rejected_calls=0,failed_calls=0
cmdstat_slowlog|get:calls=408791,usec=3637596,usec_per_call=8.90,rejected_calls=0,failed_calls=0
cmdstat_config|rewrite:calls=3,usec=643968,usec_per_call=214656.00,rejected_calls=0,failed_calls=0
cmdstat_config|get:calls=408806,usec=202690410,usec_per_call=495.81,rejected_calls=0,failed_calls=0
cmdstat_expire:calls=11799166,usec=94434285,usec_per_call=8.00,rejected_calls=0,failed_calls=0
cmdstat_pttl:calls=21891897,usec=77553247,usec_per_call=3.54,rejected_calls=0,failed_calls=0
cmdstat_zremrangebyscore:calls=1,usec=14,usec_per_call=14.00,rejected_calls=0,failed_calls=0
cmdstat_bzpopmin:calls=297301,usec=6662117,usec_per_call=22.41,rejected_calls=0,failed_calls=0
cmdstat_xtrim:calls=12537,usec=181228,usec_per_call=14.46,rejected_calls=0,failed_calls=0
cmdstat_quit:calls=1035199,usec=1104981,usec_per_call=1.07,rejected_calls=0,failed_calls=0
cmdstat_rpush:calls=8263928,usec=167261779,usec_per_call=20.24,rejected_calls=0,failed_calls=0
cmdstat_sscan:calls=4,usec=128,usec_per_call=32.00,rejected_calls=0,failed_calls=0
cmdstat_llen:calls=252,usec=414,usec_per_call=1.64,rejected_calls=0,failed_calls=0
cmdstat_select:calls=102,usec=388,usec_per_call=3.80,rejected_calls=0,failed_calls=0
cmdstat_subscribe:calls=30,usec=230,usec_per_call=7.67,rejected_calls=0,failed_calls=0
cmdstat_zremrangebyrank:calls=1,usec=2,usec_per_call=2.00,rejected_calls=0,failed_calls=0
cmdstat_hdel:calls=76,usec=978,usec_per_call=12.87,rejected_calls=0,failed_calls=0
cmdstat_lpush:calls=200159,usec=130658,usec_per_call=0.65,rejected_calls=0,failed_calls=0
cmdstat_hmset:calls=126662,usec=3716104,usec_per_call=29.34,rejected_calls=0,failed_calls=0
cmdstat_multi:calls=173529,usec=457630,usec_per_call=2.64,rejected_calls=0,failed_calls=0
cmdstat_zadd:calls=253,usec=20377,usec_per_call=80.54,rejected_calls=0,failed_calls=0
cmdstat_ttl:calls=10317,usec=31068,usec_per_call=3.01,rejected_calls=0,failed_calls=0
cmdstat_hget:calls=12669,usec=180334,usec_per_call=14.23,rejected_calls=0,failed_calls=0
cmdstat_brpop:calls=1287862,usec=22547318,usec_per_call=17.51,rejected_calls=0,failed_calls=0
cmdstat_rpoplpush:calls=297440,usec=443351,usec_per_call=1.49,rejected_calls=0,failed_calls=0
cmdstat_rpop:calls=186168,usec=186244,usec_per_call=1.00,rejected_calls=0,failed_calls=0
cmdstat_mset:calls=100000,usec=218843,usec_per_call=2.19,rejected_calls=0,failed_calls=0
cmdstat_hscan:calls=2,usec=119,usec_per_call=59.50,rejected_calls=0,failed_calls=0
cmdstat_zpopmin:calls=297318,usec=574593,usec_per_call=1.93,rejected_calls=0,failed_calls=0
cmdstat_info:calls=3552721,usec=545767580,usec_per_call=153.62,rejected_calls=0,failed_calls=0
cmdstat_type:calls=21900906,usec=11440582,usec_per_call=0.52,rejected_calls=0,failed_calls=0
cmdstat_srem:calls=21967,usec=205990,usec_per_call=9.38,rejected_calls=0,failed_calls=0
cmdstat_evalsha:calls=24584896,usec=536488800,usec_per_call=21.82,rejected_calls=0,failed_calls=1
cmdstat_script|load:calls=28,usec=2038,usec_per_call=72.79,rejected_calls=0,failed_calls=0
cmdstat_hlen:calls=4,usec=43,usec_per_call=10.75,rejected_calls=0,failed_calls=0
cmdstat_zcard:calls=123,usec=795,usec_per_call=6.46,rejected_calls=0,failed_calls=0
cmdstat_get:calls=32163539,usec=338044928,usec_per_call=10.51,rejected_calls=0,failed_calls=0
cmdstat_xadd:calls=100615,usec=189231,usec_per_call=1.88,rejected_calls=0,failed_calls=0
cmdstat_sadd:calls=19194443,usec=278327343,usec_per_call=14.50,rejected_calls=0,failed_calls=0
cmdstat_latency|latest:calls=408791,usec=1483868,usec_per_call=3.63,rejected_calls=0,failed_calls=0
cmdstat_scard:calls=832023,usec=12443254,usec_per_call=14.96,rejected_calls=0,failed_calls=0
cmdstat_pexpireat:calls=36353,usec=362794,usec_per_call=9.98,rejected_calls=0,failed_calls=0
cmdstat_client|list:calls=5,usec=17739,usec_per_call=3547.80,rejected_calls=0,failed_calls=0
cmdstat_client|kill:calls=6,usec=1225,usec_per_call=204.17,rejected_calls=0,failed_calls=0
cmdstat_client|setname:calls=408878,usec=2616988,usec_per_call=6.40,rejected_calls=0,failed_calls=0
cmdstat_lpop:calls=18821757,usec=134359211,usec_per_call=7.14,rejected_calls=0,failed_calls=0
cmdstat_restore:calls=43884842,usec=487963864,usec_per_call=11.12,rejected_calls=0,failed_calls=22
cmdstat_getrange:calls=9,usec=127,usec_per_call=14.11,rejected_calls=0,failed_calls=0
cmdstat_incr:calls=100123,usec=38425,usec_per_call=0.38,rejected_calls=0,failed_calls=0
cmdstat_exists:calls=28823736,usec=301439901,usec_per_call=10.46,rejected_calls=0,failed_calls=0
cmdstat_hincrby:calls=246,usec=1056,usec_per_call=4.29,rejected_calls=0,failed_calls=0
cmdstat_lrange:calls=12419,usec=34472,usec_per_call=2.78,rejected_calls=0,failed_calls=0
cmdstat_acl|list:calls=1,usec=69,usec_per_call=69.00,rejected_calls=0,failed_calls=0
cmdstat_hexists:calls=297563,usec=3170556,usec_per_call=10.66,rejected_calls=0,failed_calls=0
cmdstat_setex:calls=42709499,usec=1702327568,usec_per_call=39.86,rejected_calls=0,failed_calls=0
cmdstat_zrange:calls=297317,usec=160430,usec_per_call=0.54,rejected_calls=0,failed_calls=0
cmdstat_module|list:calls=7,usec=83,usec_per_call=11.86,rejected_calls=0,failed_calls=0
cmdstat_slaveof:calls=3,usec=1514,usec_per_call=504.67,rejected_calls=35,failed_calls=0
cmdstat_incrby:calls=5925004,usec=48217258,usec_per_call=8.14,rejected_calls=0,failed_calls=0
cmdstat_psync:calls=766,usec=200774,usec_per_call=262.11,rejected_calls=0,failed_calls=0
cmdstat_ping:calls=11778184,usec=41458589,usec_per_call=3.52,rejected_calls=553,failed_calls=0
cmdstat_scan:calls=416,usec=1895913,usec_per_call=4557.48,rejected_calls=0,failed_calls=0
cmdstat_setnx:calls=673281,usec=6293487,usec_per_call=9.35,rejected_calls=0,failed_calls=0
cmdstat_zrem:calls=5,usec=214,usec_per_call=42.80,rejected_calls=0,failed_calls=0
cmdstat_smembers:calls=21659,usec=304697,usec_per_call=14.07,rejected_calls=0,failed_calls=0
cmdstat_strlen:calls=9558,usec=39794,usec_per_call=4.16,rejected_calls=0,failed_calls=0
cmdstat_hmget:calls=92592,usec=1053225,usec_per_call=11.37,rejected_calls=0,failed_calls=0
cmdstat_lrem:calls=123,usec=3006,usec_per_call=24.44,rejected_calls=0,failed_calls=0
cmdstat_hello:calls=2,usec=54,usec_per_call=27.00,rejected_calls=0,failed_calls=0
cmdstat_httl:calls=2,usec=65,usec_per_call=32.50,rejected_calls=0,failed_calls=0
cmdstat_exec:calls=173529,usec=9435424,usec_per_call=54.37,rejected_calls=0,failed_calls=35
cmdstat_hgetall:calls=159,usec=2516,usec_per_call=15.82,rejected_calls=0,failed_calls=0
cmdstat_dbsize:calls=38,usec=276,usec_per_call=7.26,rejected_calls=0,failed_calls=0
cmdstat_zrangebyscore:calls=885962,usec=9277614,usec_per_call=10.47,rejected_calls=0,failed_calls=0
cmdstat_time:calls=21816610,usec=7396859,usec_per_call=0.34,rejected_calls=0,failed_calls=0
cmdstat_command:calls=2,usec=8162,usec_per_call=4081.00,rejected_calls=0,failed_calls=0
cmdstat_command|docs:calls=5,usec=10018,usec_per_call=2003.60,rejected_calls=0,failed_calls=0
cmdstat_memory|usage:calls=10317,usec=48586,usec_per_call=4.71,rejected_calls=0,failed_calls=0
cmdstat_eval:calls=162,usec=96045,usec_per_call=592.87,rejected_calls=0,failed_calls=0
cmdstat_replconf:calls=1962644,usec=4449807,usec_per_call=2.27,rejected_calls=0,failed_calls=0

# Errorstats
errorstat_BUSYKEY:count=3
errorstat_ERR:count=228
errorstat_EXECABORT:count=35
errorstat_LOADING:count=588
errorstat_NOSCRIPT:count=1

# Latencystats
latency_percentiles_usec_cluster|info:p50=27.007,p99=64.255,p99.9=64.255
latency_percentiles_usec_publish:p50=20.095,p99=62.207,p99.9=161.791
latency_percentiles_usec_unlink:p50=3.007,p99=13.055,p99.9=35.071
latency_percentiles_usec_del:p50=28.031,p99=235.519,p99.9=749.567
latency_percentiles_usec_set:p50=9.023,p99=75.263,p99.9=311.295
latency_percentiles_usec_hset:p50=15.039,p99=88.063,p99.9=798.719
latency_percentiles_usec_slowlog|len:p50=2.007,p99=10.047,p99.9=33.023
latency_percentiles_usec_slowlog|get:p50=7.007,p99=37.119,p99.9=92.159
latency_percentiles_usec_config|rewrite:p50=95944.703,p99=511705.087,p99.9=511705.087
latency_percentiles_usec_config|get:p50=354.303,p99=1957.887,p99.9=4014.079
latency_percentiles_usec_expire:p50=7.007,p99=33.023,p99.9=63.231
latency_percentiles_usec_pttl:p50=2.007,p99=16.063,p99.9=51.199
latency_percentiles_usec_zremrangebyscore:p50=14.015,p99=14.015,p99.9=14.015
latency_percentiles_usec_bzpopmin:p50=18.047,p99=58.111,p99.9=679.935
latency_percentiles_usec_xtrim:p50=10.047,p99=48.127,p99.9=103.423
latency_percentiles_usec_quit:p50=1.003,p99=2.007,p99.9=13.055
latency_percentiles_usec_rpush:p50=17.023,p99=72.191,p99.9=350.207
latency_percentiles_usec_sscan:p50=31.103,p99=34.047,p99.9=34.047
latency_percentiles_usec_llen:p50=1.003,p99=14.015,p99.9=162.815
latency_percentiles_usec_select:p50=2.007,p99=24.063,p99.9=26.111
latency_percentiles_usec_subscribe:p50=5.023,p99=35.071,p99.9=35.071
latency_percentiles_usec_zremrangebyrank:p50=2.007,p99=2.007,p99.9=2.007
latency_percentiles_usec_hdel:p50=10.047,p99=49.151,p99.9=49.151
latency_percentiles_usec_lpush:p50=0.001,p99=4.015,p99.9=21.119
latency_percentiles_usec_hmset:p50=20.095,p99=89.087,p99.9=757.759
latency_percentiles_usec_multi:p50=2.007,p99=13.055,p99.9=36.095
latency_percentiles_usec_zadd:p50=71.167,p99=167.935,p99.9=937.983
latency_percentiles_usec_ttl:p50=2.007,p99=20.095,p99.9=49.151
latency_percentiles_usec_hget:p50=13.055,p99=35.071,p99.9=62.207
latency_percentiles_usec_brpop:p50=14.015,p99=54.015,p99.9=671.743
latency_percentiles_usec_rpoplpush:p50=1.003,p99=7.007,p99.9=24.063
latency_percentiles_usec_rpop:p50=1.003,p99=5.023,p99.9=15.039
latency_percentiles_usec_mset:p50=2.007,p99=5.023,p99.9=20.095
latency_percentiles_usec_hscan:p50=47.103,p99=72.191,p99.9=72.191
latency_percentiles_usec_zpopmin:p50=2.007,p99=11.007,p99.9=26.111
latency_percentiles_usec_info:p50=46.079,p99=1011.711,p99.9=1564.671
latency_percentiles_usec_type:p50=0.001,p99=2.007,p99.9=11.007
latency_percentiles_usec_srem:p50=7.007,p99=34.047,p99.9=80.383
latency_percentiles_usec_evalsha:p50=12.031,p99=131.071,p99.9=323.583
latency_percentiles_usec_script|load:p50=17.023,p99=573.439,p99.9=573.439
latency_percentiles_usec_hlen:p50=10.047,p99=16.063,p99.9=16.063
latency_percentiles_usec_zcard:p50=7.007,p99=12.031,p99.9=15.039
latency_percentiles_usec_get:p50=6.015,p99=57.087,p99.9=387.071
latency_percentiles_usec_xadd:p50=1.003,p99=12.031,p99.9=62.207
latency_percentiles_usec_sadd:p50=11.007,p99=47.103,p99.9=234.495
latency_percentiles_usec_latency|latest:p50=3.007,p99=16.063,p99.9=47.103
latency_percentiles_usec_scard:p50=13.055,p99=37.119,p99.9=107.007
latency_percentiles_usec_pexpireat:p50=7.007,p99=37.119,p99.9=104.447
latency_percentiles_usec_client|list:p50=5079.039,p99=6422.527,p99.9=6422.527
latency_percentiles_usec_client|kill:p50=165.887,p99=303.103,p99.9=303.103
latency_percentiles_usec_client|setname:p50=6.015,p99=30.079,p99.9=59.135
latency_percentiles_usec_lpop:p50=3.007,p99=48.127,p99.9=108.031
latency_percentiles_usec_restore:p50=5.023,p99=55.039,p99.9=327.679
latency_percentiles_usec_getrange:p50=15.039,p99=17.023,p99.9=17.023
latency_percentiles_usec_incr:p50=0.001,p99=2.007,p99.9=30.079
latency_percentiles_usec_exists:p50=7.007,p99=32.127,p99.9=171.007
latency_percentiles_usec_hincrby:p50=4.015,p99=15.039,p99.9=58.111
latency_percentiles_usec_lrange:p50=1.003,p99=13.055,p99.9=150.527
latency_percentiles_usec_acl|list:p50=69.119,p99=69.119,p99.9=69.119
latency_percentiles_usec_hexists:p50=11.007,p99=29.055,p99.9=61.183
latency_percentiles_usec_setex:p50=27.007,p99=116.223,p99.9=802.815
latency_percentiles_usec_zrange:p50=0.001,p99=2.007,p99.9=10.047
latency_percentiles_usec_module|list:p50=9.023,p99=31.103,p99.9=31.103
latency_percentiles_usec_slaveof:p50=446.463,p99=659.455,p99.9=659.455
latency_percentiles_usec_incrby:p50=6.015,p99=25.087,p99.9=76.287
latency_percentiles_usec_psync:p50=221.183,p99=892.927,p99.9=1003.519
latency_percentiles_usec_ping:p50=3.007,p99=14.015,p99.9=39.167
latency_percentiles_usec_scan:p50=3964.927,p99=16383.999,p99.9=37748.735
latency_percentiles_usec_setnx:p50=7.007,p99=26.111,p99.9=69.119
latency_percentiles_usec_zrem:p50=30.079,p99=98.303,p99.9=98.303
latency_percentiles_usec_smembers:p50=4.015,p99=77.311,p99.9=518.143
latency_percentiles_usec_strlen:p50=3.007,p99=19.071,p99.9=47.103
latency_percentiles_usec_hmget:p50=5.023,p99=43.007,p99.9=851.967
latency_percentiles_usec_lrem:p50=23.039,p99=48.127,p99.9=50.175
latency_percentiles_usec_hello:p50=21.119,p99=33.023,p99.9=33.023
latency_percentiles_usec_httl:p50=26.111,p99=39.167,p99.9=39.167
latency_percentiles_usec_exec:p50=43.007,p99=138.239,p99.9=716.799
latency_percentiles_usec_hgetall:p50=11.007,p99=58.111,p99.9=110.079
latency_percentiles_usec_dbsize:p50=4.015,p99=35.071,p99.9=35.071
latency_percentiles_usec_zrangebyscore:p50=9.023,p99=30.079,p99.9=70.143
latency_percentiles_usec_time:p50=0.001,p99=2.007,p99.9=8.031
latency_percentiles_usec_command:p50=2392.063,p99=5799.935,p99.9=5799.935
latency_percentiles_usec_command|docs:p50=2097.151,p99=2326.527,p99.9=2326.527
latency_percentiles_usec_memory|usage:p50=2.007,p99=27.007,p99.9=219.135
latency_percentiles_usec_eval:p50=593.919,p99=1622.015,p99.9=1703.935
latency_percentiles_usec_replconf:p50=2.007,p99=10.047,p99.9=24.063
# Cluster
cluster_enabled:0
# Keyspace
db0:keys=25472026,expires=24849338,avg_ttl=672182855,subexpiry=0
db1:keys=18,expires=13,avg_ttl=145372776312,subexpiry=0
`
	e, _ := NewRedisExporter("unix:///tmp/doesnt.matter", Options{Namespace: "test"})

	chM := make(chan prometheus.Metric)
	go func() {
		e.extractInfoMetrics(chM, infoStr, 12)
		close(chM)
	}()

	want := map[string]bool{
		"test_latency_percentiles_usec":        false,
		"test_commands_duration_seconds_total": false,
		"test_commands_total":                  false,
	}

	for m := range chM {
		descString := m.Desc().String()
		t.Logf("d: %s", descString)
		for k := range want {
			if strings.Contains(descString, k) {
				want[k] = true
			}
		}
	}

	for k, found := range want {
		if !found {
			t.Errorf("didn't find metric: %s", k)
		}
	}
}

func init() {
	ll := strings.ToLower(os.Getenv("LOG_LEVEL"))
	if pl, err := log.ParseLevel(ll); err == nil {
		log.Printf("Setting log level to: %s", ll)
		log.SetLevel(pl)
	} else {
		log.SetLevel(log.InfoLevel)
	}

	testTimestamp := time.Now().Unix()

	for _, n := range []string{"john", "paul", "ringo", "george"} {
		testKeys = append(testKeys, fmt.Sprintf("key_%s_%d", n, testTimestamp))
	}

	TestKeyNameSingleString = fmt.Sprintf("key_string_%d", testTimestamp)
	testKeysList = append(testKeysList, "test_beatles_list")

	for _, n := range []string{"A.J.", "Howie", "Nick", "Kevin", "Brian"} {
		testKeysExpiring = append(testKeysExpiring, fmt.Sprintf("key_exp_%s_%d", n, testTimestamp))
	}

	AllTestKeys = append(AllTestKeys, TestKeyNameSingleString)
	AllTestKeys = append(AllTestKeys, testKeys...)
	AllTestKeys = append(AllTestKeys, testKeysList...)
	AllTestKeys = append(AllTestKeys, testKeysExpiring...)
}
