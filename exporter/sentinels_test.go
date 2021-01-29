package exporter

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestExtractInfoMetricsSentinel(t *testing.T) {
	if os.Getenv("TEST_REDIS_SENTINEL_URI") == "" {
		t.Skipf("TEST_REDIS_SENTINEL_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS_SENTINEL_URI")
	e, _ := NewRedisExporter(
		addr,
		Options{Namespace: "test"},
	)
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}

	infoAll, err := redis.String(doRedisCmd(c, "INFO", "ALL"))
	if err != nil {
		t.Logf("Redis INFO ALL err: %s", err)
		infoAll, err = redis.String(doRedisCmd(c, "INFO"))
		if err != nil {
			t.Fatalf("Redis INFO err: %s", err)
		}
	}

	chM := make(chan prometheus.Metric)
	go func() {
		e.extractInfoMetrics(chM, infoAll, 0)
		close(chM)
	}()
	want := map[string]bool{
		"sentinel_tilt":                   false,
		"sentinel_running_scripts":        false,
		"sentinel_scripts_queue_length":   false,
		"sentinel_simulate_failure_flags": false,
		"sentinel_masters":                false,
		"sentinel_master_status":          false,
		"sentinel_master_slaves":          false,
		"sentinel_master_sentinels":       false,
	}

	for m := range chM {
		for k := range want {
			if strings.Contains(m.Desc().String(), k) {
				want[k] = true
			}
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("didn't find %s", k)
		}

	}
}

type sentinelData struct {
	k, v                  string
	name, status, address string
	slaves, sentinels     float64
	ok                    bool
}

func TestParseSentinelMasterString(t *testing.T) {
	tsts := []sentinelData{
		{k: "master0", v: "name=user03,status=sdown,address=192.169.2.52:6381,slaves=1,sentinels=5", name: "user03", status: "sdown", address: "192.169.2.52:6381", slaves: 1, sentinels: 5, ok: true},
		{k: "master1", v: "name=master,status=ok,address=127.0.0.1:6379,slaves=999,sentinels=500", name: "master", status: "ok", address: "127.0.0.1:6379", slaves: 999, sentinels: 500, ok: true},

		{k: "master", v: "name=user03", ok: false},
		{k: "masterA", v: "status=ko", ok: false},
		{k: "master0", v: "slaves=abc,sentinels=0", ok: false},
		{k: "master0", v: "slaves=0,sentinels=abc", ok: false},
	}

	for _, tst := range tsts {
		name := fmt.Sprintf("%s---%s", tst.k, tst.v)
		t.Run(name, func(t *testing.T) {
			if masterName, masterStatus, masterAddress, masterSlaves, masterSentinels, ok := parseSentinelMasterString(tst.k, tst.v); true {
				if ok != tst.ok {
					t.Errorf("failed for: master:%s data:%s", tst.k, tst.v)
					return
				}
				if masterName != tst.name || masterStatus != tst.status || masterAddress != tst.address || masterSlaves != tst.slaves || masterSentinels != tst.sentinels {
					t.Errorf("values not matching:\nstring:%s\ngot:%s %s %s %f %f", tst.v, masterName, masterStatus, masterAddress, masterSlaves, masterSentinels)
				}
			}
		})
	}
}

func TestExtractSentinelMetricsForRedis(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS_URI")
	e, _ := NewRedisExporter(
		addr,
		Options{Namespace: "test"},
	)
	c, err := redis.DialURL(addr)
	defer c.Close()

	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}
	chM := make(chan prometheus.Metric)
	go func() {
		e.extractSentinelMetrics(chM, c)
		close(chM)
	}()

	want := map[string]bool{
		"sentinel_master_ok_sentinels": false,
		"sentinel_master_ok_slaves":    false,
	}

	for m := range chM {
		for k := range want {
			if strings.Contains(m.Desc().String(), k) {
				want[k] = true
			}
		}
	}
	for k, found := range want {
		if found {
			t.Errorf("Found sentinel metric %s for redis instance", k)
		}
	}
}

func TestExtractSentinelMetricsForSentinel(t *testing.T) {
	if os.Getenv("TEST_REDIS_SENTINEL_URI") == "" {
		t.Skipf("TEST_REDIS_SENTINEL_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS_SENTINEL_URI")
	e, _ := NewRedisExporter(
		addr,
		Options{Namespace: "test"},
	)
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}
	defer c.Close()

	infoAll, err := redis.String(doRedisCmd(c, "INFO", "ALL"))
	if err != nil {
		t.Logf("Redis INFO ALL err: %s", err)
		infoAll, err = redis.String(doRedisCmd(c, "INFO"))
		if err != nil {
			t.Fatalf("Redis INFO err: %s", err)
		}
	}

	chM := make(chan prometheus.Metric)
	if strings.Contains(infoAll, "# Sentinel") {
		go func() {
			e.extractSentinelMetrics(chM, c)
			close(chM)
		}()
	} else {
		t.Fatalf("Couldn't find sentinel section in Redis INFO: %s", infoAll)
	}

	want := map[string]bool{
		"sentinel_master_ok_sentinels": false,
		"sentinel_master_ok_slaves":    false,
	}

	for m := range chM {
		for k := range want {
			if strings.Contains(m.Desc().String(), k) {
				want[k] = true
			}
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("didn't find metric %s", k)
		}
	}
}

type sentinelSentinelsData struct {
	name                string
	sentinelDetails     []interface{}
	labels              []string
	expectedMetricValue map[string]int
}

func TestProcessSentinelSentinels(t *testing.T) {
	if os.Getenv("TEST_REDIS_SENTINEL_URI") == "" {
		t.Skipf("TEST_REDIS_SENTINEL_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS_SENTINEL_URI")
	e, _ := NewRedisExporter(
		addr,
		Options{Namespace: "test"},
	)

	oneOkSentinelExpectedMetricValue := map[string]int{
		"sentinel_master_ok_sentinels": 1,
	}
	twoOkSentinelExpectedMetricValue := map[string]int{
		"sentinel_master_ok_sentinels": 2,
	}
	tsts := []sentinelSentinelsData{
		{"1/1 okay sentinel", []interface{}{[]interface{}{[]byte("")}}, []string{"mymaster", "172.17.0.7:26379"}, oneOkSentinelExpectedMetricValue},
		{"1/3 okay sentinel", []interface{}{[]interface{}{[]byte("name"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("ip"), []byte("172.17.0.8"), []byte("port"), []byte("26379"), []byte("runid"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("flags"), []byte("o_down,s_down,sentinel"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823816"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}, []interface{}{[]byte("name"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("ip"), []byte("172.17.0.7"), []byte("port"), []byte("26379"), []byte("runid"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("flags"), []byte("s_down,sentinel"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823815"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}}, []string{"mymaster", "172.17.0.7:26379"}, oneOkSentinelExpectedMetricValue},
		{"2/3 okay sentinel(string is not byte slice)", []interface{}{[]interface{}{[]byte("name"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("ip"), []byte("172.17.0.8"), []byte("port"), []byte("26379"), []byte("runid"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("flags"), []byte("sentinel"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823816"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}, []interface{}{[]byte("name"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("ip"), []byte("172.17.0.7"), []byte("port"), []byte("26379"), []byte("runid"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("flags"), ("sentinel"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823815"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}}, []string{"mymaster", "172.17.0.7:26379"}, twoOkSentinelExpectedMetricValue},
		{"2/3 okay sentinel", []interface{}{[]interface{}{[]byte("name"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("ip"), []byte("172.17.0.8"), []byte("port"), []byte("26379"), []byte("runid"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("flags"), []byte("sentinel"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823816"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}, []interface{}{[]byte("name"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("ip"), []byte("172.17.0.7"), []byte("port"), []byte("26379"), []byte("runid"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("flags"), []byte("s_down,sentinel"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823815"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}}, []string{"mymaster", "172.17.0.7:26379"}, twoOkSentinelExpectedMetricValue},
		{"2/3 okay sentinel(missing flags)", []interface{}{[]interface{}{[]byte("name"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("ip"), []byte("172.17.0.8"), []byte("port"), []byte("26379"), []byte("runid"), []byte("284bc2ef46881bd71e81610152cb96031d211d28"), []byte("flags"), []byte("sentinel"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823816"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}, []interface{}{[]byte("name"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("ip"), []byte("172.17.0.7"), []byte("port"), []byte("26379"), []byte("runid"), []byte("c3ab3cdcaeb193bb49b16d4d3da88def984ab3bf"), []byte("link-pending-commands"), []byte("38"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("11828891"), []byte("last-ok-ping-reply"), []byte("11829539"), []byte("last-ping-reply"), []byte("11829539"), []byte("s-down-time"), []byte("11823815"), []byte("down-after-milliseconds"), []byte("5000"), []byte("last-hello-message"), []byte("11829434"), []byte("voted-leader"), []byte("?"), []byte("voted-leader-epoch"), []byte("0")}}, []string{"mymaster", "172.17.0.7:26379"}, twoOkSentinelExpectedMetricValue},
	}
	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {
			chM := make(chan prometheus.Metric)
			go func() {
				e.processSentinelSentinels(chM, tst.sentinelDetails, tst.labels...)
				close(chM)
			}()
			want := map[string]bool{
				"sentinel_master_ok_sentinels": false,
			}

			for m := range chM {
				for k := range want {
					if strings.Contains(m.Desc().String(), k) {
						want[k] = true
						got := &dto.Metric{}
						m.Write(got)

						val := got.GetGauge().GetValue()
						if int(val) != tst.expectedMetricValue[k] {
							t.Errorf("Expected metric value %d didn't match to reported value %d for test %s", tst.expectedMetricValue[k], int(val), tst.name)
						}
					}
				}
			}
			for k, found := range want {
				if !found {
					t.Errorf("didn't find metric %s", k)
				}
			}
		})
	}
}

type sentinelSlavesData struct {
	name                string
	slaveDetails        []interface{}
	labels              []string
	expectedMetricValue map[string]int
}

func TestProcessSentinelSlaves(t *testing.T) {
	if os.Getenv("TEST_REDIS_SENTINEL_URI") == "" {
		t.Skipf("TEST_REDIS_SENTINEL_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS_SENTINEL_URI")
	e, _ := NewRedisExporter(
		addr,
		Options{Namespace: "test"},
	)
	zeroOkSlaveExpectedMetricValue := map[string]int{
		"sentinel_master_ok_slaves": 0,
	}
	oneOkSlaveExpectedMetricValue := map[string]int{
		"sentinel_master_ok_slaves": 1,
	}
	twoOkSlaveExpectedMetricValue := map[string]int{
		"sentinel_master_ok_slaves": 2,
	}

	tsts := []sentinelSlavesData{
		{"0/1 okay slave(string is not byte slice)", []interface{}{[]interface{}{[]string{"name"}, []byte("172.17.0.3:6379"), []byte("ip"), []byte("172.17.0.3"), []byte("port"), []byte("6379"), []byte("runid"), []byte("42ebb784f2bd560903de9fb7d4533263d5db558a"), []byte("flags"), []byte("slave"), []byte("link-pending-commands"), []byte("0"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("0"), []byte("last-ok-ping-reply"), []byte("490"), []byte("last-ping-reply"), []byte("490"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("2636"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("48279581"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("765829")}}, []string{"mymaster", "172.17.0.7:26379"}, zeroOkSlaveExpectedMetricValue},
		{"1/1 okay slave", []interface{}{[]interface{}{[]byte("name"), []byte("172.17.0.3:6379"), []byte("ip"), []byte("172.17.0.3"), []byte("port"), []byte("6379"), []byte("runid"), []byte("42ebb784f2bd560903de9fb7d4533263d5db558a"), []byte("flags"), []byte("slave"), []byte("link-pending-commands"), []byte("0"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("0"), []byte("last-ok-ping-reply"), []byte("490"), []byte("last-ping-reply"), []byte("490"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("2636"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("48279581"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("765829")}}, []string{"mymaster", "172.17.0.7:26379"}, oneOkSlaveExpectedMetricValue},
		{"1/3 okay slave", []interface{}{[]interface{}{[]byte("name"), []byte("172.17.0.6:6379"), []byte("ip"), []byte("172.17.0.6"), []byte("port"), []byte("6379"), []byte("runid"), []byte("254576b435fcd73121a6497d3b03f3a464de9a10"), []byte("flags"), []byte("slave"), []byte("link-pending-commands"), []byte("0"), []byte("last-ok-ping-reply"), []byte("1021"), []byte("last-ping-reply"), []byte("1021"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("6293"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("36490"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1316759")}, []interface{}{[]byte("name"), []byte("172.17.0.3:6379"), []byte("ip"), []byte("172.17.0.3"), []byte("port"), []byte("6379"), []byte("runid"), []byte("42ebb784f2bd560903de9fb7d4533263d5db558a"), []byte("flags"), []byte("s_down,slave"), []byte("link-pending-commands"), []byte("0"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("0"), []byte("last-ok-ping-reply"), []byte("655"), []byte("last-ping-reply"), []byte("655"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("6394"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("56525539"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1316759")}, []interface{}{[]byte("name"), []byte("172.17.0.5:6379"), []byte("ip"), []byte("172.17.0.5"), []byte("port"), []byte("6379"), []byte("runid"), []byte("8f4b14e820fab7b38cad640208803dfb9fa225ca"), []byte("flags"), []byte("o_down,s_down,slave"), []byte("link-pending-commands"), []byte("100"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("23792"), []byte("last-ok-ping-reply"), []byte("23902"), []byte("last-ping-reply"), []byte("23902"), []byte("s-down-time"), []byte("18785"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("26352"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("36493"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("redis-master"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1315493")}}, []string{"mymaster", "172.17.0.7:26379"}, oneOkSlaveExpectedMetricValue},
		{"2/3 okay slave", []interface{}{[]interface{}{[]byte("name"), []byte("172.17.0.6:6379"), []byte("ip"), []byte("172.17.0.6"), []byte("port"), []byte("6379"), []byte("runid"), []byte("254576b435fcd73121a6497d3b03f3a464de9a10"), []byte("flags"), []byte("slave"), []byte("link-pending-commands"), []byte("0"), []byte("last-ok-ping-reply"), []byte("1021"), []byte("last-ping-reply"), []byte("1021"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("6293"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("36490"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1316759")}, []interface{}{[]byte("name"), []byte("172.17.0.3:6379"), []byte("ip"), []byte("172.17.0.3"), []byte("port"), []byte("6379"), []byte("runid"), []byte("42ebb784f2bd560903de9fb7d4533263d5db558a"), []byte("flags"), []byte("slave"), []byte("link-pending-commands"), []byte("0"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("0"), []byte("last-ok-ping-reply"), []byte("655"), []byte("last-ping-reply"), []byte("655"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("6394"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("56525539"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1316759")}, []interface{}{[]byte("name"), []byte("172.17.0.5:6379"), []byte("ip"), []byte("172.17.0.5"), []byte("port"), []byte("6379"), []byte("runid"), []byte("8f4b14e820fab7b38cad640208803dfb9fa225ca"), []byte("flags"), []byte("s_down,slave"), []byte("link-pending-commands"), []byte("100"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("23792"), []byte("last-ok-ping-reply"), []byte("23902"), []byte("last-ping-reply"), []byte("23902"), []byte("s-down-time"), []byte("18785"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("26352"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("36493"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("redis-master"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1315493")}}, []string{"mymaster", "172.17.0.7:26379"}, twoOkSlaveExpectedMetricValue},
		{"2/3 okay slave(missing flags)", []interface{}{[]interface{}{[]byte("name"), []byte("172.17.0.6:6379"), []byte("ip"), []byte("172.17.0.6"), []byte("port"), []byte("6379"), []byte("runid"), []byte("254576b435fcd73121a6497d3b03f3a464de9a10"), []byte("flags"), []byte("slave"), []byte("link-pending-commands"), []byte("0"), []byte("last-ok-ping-reply"), []byte("1021"), []byte("last-ping-reply"), []byte("1021"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("6293"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("36490"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1316759")}, []interface{}{[]byte("name"), []byte("172.17.0.3:6379"), []byte("ip"), []byte("172.17.0.3"), []byte("port"), []byte("6379"), []byte("runid"), []byte("42ebb784f2bd560903de9fb7d4533263d5db558a"), []byte("flags"), []byte("slave"), []byte("link-pending-commands"), []byte("0"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("0"), []byte("last-ok-ping-reply"), []byte("655"), []byte("last-ping-reply"), []byte("655"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("6394"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("56525539"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("172.17.0.2"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1316759")}, []interface{}{[]byte("name"), []byte("172.17.0.5:6379"), []byte("ip"), []byte("172.17.0.5"), []byte("port"), []byte("6379"), []byte("runid"), []byte("8f4b14e820fab7b38cad640208803dfb9fa225ca"), []byte("link-pending-commands"), []byte("100"), []byte("link-refcount"), []byte("1"), []byte("last-ping-sent"), []byte("23792"), []byte("last-ok-ping-reply"), []byte("23902"), []byte("last-ping-reply"), []byte("23902"), []byte("s-down-time"), []byte("18785"), []byte("down-after-milliseconds"), []byte("5000"), []byte("info-refresh"), []byte("26352"), []byte("role-reported"), []byte("slave"), []byte("role-reported-time"), []byte("36493"), []byte("master-link-down-time"), []byte("0"), []byte("master-link-status"), []byte("ok"), []byte("master-host"), []byte("redis-master"), []byte("master-port"), []byte("6379"), []byte("slave-priority"), []byte("100"), []byte("slave-repl-offset"), []byte("1315493")}}, []string{"mymaster", "172.17.0.7:26379"}, twoOkSlaveExpectedMetricValue},
	}
	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {
			chM := make(chan prometheus.Metric)
			go func() {
				e.processSentinelSlaves(chM, tst.slaveDetails, tst.labels...)
				close(chM)
			}()
			want := map[string]bool{
				"sentinel_master_ok_slaves": false,
			}

			for m := range chM {
				for k := range want {
					if strings.Contains(m.Desc().String(), k) {
						want[k] = true
						got := &dto.Metric{}
						m.Write(got)

						val := got.GetGauge().GetValue()
						if int(val) != tst.expectedMetricValue[k] {
							t.Errorf("Expected metric value %d didn't match to reported value %d for test %s", tst.expectedMetricValue[k], int(val), tst.name)
						}
					}
				}
			}
			for k, found := range want {
				if !found {
					t.Errorf("didn't find metric %s", k)
				}
			}
		})
	}
}
