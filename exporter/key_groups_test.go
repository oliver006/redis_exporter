package exporter

import (
	"errors"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestGatherKeyGroupMetricsWithoutRedis(t *testing.T) {
	call := 0
	conn := &stubRedisConn{do: func(command string, _ ...any) (any, error) {
		if command != "EVALSHA" {
			return nil, errors.New("unexpected command")
		}
		call++
		if call == 1 {
			return []any{int64(7), []any{[]any{"group", int64(1), int64(32)}}}, nil
		}
		return []any{int64(0), []any{[]any{"group", int64(2), int64(64)}}}, nil
	}}

	groups, err := gatherKeyGroupMetrics(conn, 10, []string{"(group)"})
	if err != nil {
		t.Fatal(err)
	}
	if call != 2 {
		t.Fatalf("script calls = %d, want 2", call)
	}
	if got := groups["group"]; got == nil || got.count != 3 || got.memoryUsage != 96 {
		t.Fatalf("group metrics = %#v", got)
	}
}

func TestGatherKeyGroupMetricsErrors(t *testing.T) {
	testErr := errors.New("script failed")
	tests := []struct {
		name  string
		reply any
		err   error
	}{
		{name: "script", err: testErr},
		{name: "response length", reply: []any{int64(0)}},
		{name: "groups", reply: []any{int64(0), "invalid"}},
		{name: "group metrics", reply: []any{int64(0), []any{[]any{"group", "invalid", int64(1)}}}},
		{name: "cursor", reply: []any{struct{}{}, []any{}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conn := &stubRedisConn{do: func(string, ...any) (any, error) {
				return test.reply, test.err
			}}
			if _, err := gatherKeyGroupMetrics(conn, 10, []string{"(group)"}); err == nil {
				t.Fatal("gatherKeyGroupMetrics() unexpectedly succeeded")
			}
		})
	}
}

func TestGatherClusterKeyGroupMetricsErrors(t *testing.T) {
	testErr := errors.New("script failed")
	keys := []any{"0", []any{"{slot}:key"}}
	tests := []struct {
		name        string
		scanReply   any
		scriptReply any
		scriptErr   error
	}{
		{name: "keys", scanReply: []any{"0", "invalid"}},
		{name: "script", scanReply: keys, scriptErr: testErr},
		{name: "response length", scanReply: keys, scriptReply: []any{int64(0)}},
		{name: "groups", scanReply: keys, scriptReply: []any{int64(0), "invalid"}},
		{name: "group metrics", scanReply: keys, scriptReply: []any{int64(0), []any{[]any{"group", "invalid", int64(1)}}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conn := &clusterScanStub{&stubRedisConn{do: func(command string, _ ...any) (any, error) {
				switch command {
				case "CLUSTERSCAN":
					return test.scanReply, nil
				case "EVALSHA":
					return test.scriptReply, test.scriptErr
				default:
					return nil, errors.New("unexpected command")
				}
			}}}
			if _, err := gatherClusterKeyGroupMetrics(conn, 10, []string{"(group)"}); err == nil {
				t.Fatal("gatherClusterKeyGroupMetrics() unexpectedly succeeded")
			}
		})
	}
}

func TestMergeKeyGroupMetricsErrors(t *testing.T) {
	tests := []struct {
		name   string
		groups []any
		want   string
	}{
		{name: "shape", groups: []any{"invalid"}, want: "response"},
		{name: "name", groups: []any{[]any{struct{}{}, int64(1), int64(1)}}, want: "name"},
		{name: "count", groups: []any{[]any{"group", "invalid", int64(1)}}, want: "count"},
		{name: "memory", groups: []any{[]any{"group", int64(1), "invalid"}}, want: "memory usage"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := mergeKeyGroupMetrics(make(map[string]*keyGroupMetrics), test.groups)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("mergeKeyGroupMetrics() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestGatherKeyGroupsMetricsUsesClusterScan(t *testing.T) {
	conn := &clusterScanStub{&stubRedisConn{do: func(command string, _ ...any) (any, error) {
		switch command {
		case "SELECT":
			return "OK", nil
		case "CLUSTERSCAN":
			return []any{"0", []any{"{slot}:key"}}, nil
		case "EVALSHA":
			return []any{int64(0), []any{[]any{"slot", int64(1), int64(64)}}}, nil
		default:
			return nil, errors.New("unexpected command")
		}
	}}}
	e := &Exporter{options: Options{
		CheckKeyGroups:       "{(.*)}",
		CheckKeysBatchSize:   10,
		MaxDistinctKeyGroups: 100,
	}}

	result := e.gatherKeyGroupsMetricsForAllDatabases(conn, 1)
	if got := result.metrics[0]["slot"]; got == nil || got.count != 1 || got.memoryUsage != 64 {
		t.Fatalf("group metrics = %#v", got)
	}
}

func getDBCount(c redis.Conn) (dbCount int, err error) {
	dbCount = 16
	var config []string
	if config, err = redis.Strings(doRedisCmd(c, "CONFIG", "GET", "*")); err != nil {
		return
	}

	for pos := 0; pos < len(config)/2; pos++ {
		strKey := config[pos*2]
		strVal := config[pos*2+1]

		if strKey == "databases" {
			if dbCount, err = strconv.Atoi(strVal); err != nil {
				dbCount = 16
			}
			return
		}
	}
	return
}

type keyGroupData struct {
	name                   string
	checkKeyGroups         string
	maxDistinctKeyGroups   int64
	wantedCount            map[string]int
	wantedMemory           map[string]bool
	wantedDistintKeyGroups int
}

func TestKeyGroupMetrics(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Skipf("TEST_REDIS_URI not set - skipping")
	}
	addr := os.Getenv("TEST_REDIS_URI")
	c, err := redis.DialURL(addr)
	if err != nil {
		t.Fatalf("Couldn't connect to %#v: %#v", addr, err)
	}

	var dbCount int
	if dbCount, err = getDBCount(c); err != nil {
		t.Fatalf("Couldn't get dbCount: %#v", err)
	}
	setupTestKeys(t, addr)
	defer deleteTestKeys(t, addr)

	tsts := []keyGroupData{
		{
			name:                 "synchronous with unclassified keys",
			checkKeyGroups:       "^(key_ringo)_[0-9]+$,^(key_paul)_[0-9]+$,^(key_exp)_.+$",
			maxDistinctKeyGroups: 100,
			// The actual counts are a function of keys (all types) being set up in the init() function
			// and the CheckKeyGroups regexes for initializing the Redis exporter above. The count below
			// will need to be updated if either of the aforementioned things have changed.
			wantedCount: map[string]int{
				"key_ringo":    1,
				"key_paul":     1,
				"unclassified": 9,
				"key_exp":      5,
			},
			wantedMemory: map[string]bool{
				"key_ringo":    true,
				"key_paul":     true,
				"unclassified": true,
				"key_exp":      true,
			},
			wantedDistintKeyGroups: 4,
		},
		{
			name:                 "synchronous with overflow keys",
			checkKeyGroups:       "^(.*)$", // Each key is a distinct key group
			maxDistinctKeyGroups: 1,
			// The actual counts depend on the largest key being set up in the init()
			// function (test-stream at the time this code was written) and the total
			// of keys (all types). This will need to be updated to match future
			// updates of the init() function
			wantedCount: map[string]int{
				"overflow": 15, "test-stream": 1,
			},
			wantedMemory: map[string]bool{
				"overflow": true, "test-stream": true,
			},
			wantedDistintKeyGroups: 16,
		},
	}

	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {
			e, _ := NewRedisExporter(
				addr,
				Options{
					Namespace:            "test",
					CheckKeyGroups:       tst.checkKeyGroups,
					CheckKeysBatchSize:   1000,
					MaxDistinctKeyGroups: tst.maxDistinctKeyGroups,
				},
			)
			for {
				chM := make(chan prometheus.Metric)
				go func() {
					e.extractKeyGroupMetrics(chM, c, dbCount)
					close(chM)
				}()

				actualCount := make(map[string]int)
				actualMemory := make(map[string]bool)
				actualDistinctKeyGroups := 0

				receivedMetrics := false
				for m := range chM {
					receivedMetrics = true
					got := &dto.Metric{}
					m.Write(got)

					if strings.Contains(m.Desc().String(), "test_key_group_count") {
						for _, label := range got.GetLabel() {
							if *label.Name == "key_group" {
								actualCount[*label.Value] = int(*got.Gauge.Value)
							}
						}
					} else if strings.Contains(m.Desc().String(), "test_key_group_memory_usage_bytes") {
						for _, label := range got.GetLabel() {
							if *label.Name == "key_group" {
								actualMemory[*label.Value] = true
							}
						}
					} else if strings.Contains(m.Desc().String(), "test_number_of_distinct_key_groups") {
						for _, label := range got.GetLabel() {
							if *label.Name == "db" && *label.Value == "db"+dbNumStr {
								actualDistinctKeyGroups = int(*got.Gauge.Value)
							}
						}
					}
				}

				if !receivedMetrics {
					time.Sleep(100 * time.Millisecond)
					continue
				}
				if !reflect.DeepEqual(tst.wantedCount, actualCount) {
					t.Errorf("Key group count metrics are not expected:\n Expected: %#v\nActual: %#v\n", tst.wantedCount, actualCount)
				}

				// It's a little fragile to anticipate how much memory
				// will be allocated for specific key groups, so we
				// are only going to check for presence of memory usage
				// metrics for expected key groups here.
				if !reflect.DeepEqual(tst.wantedMemory, actualMemory) {
					t.Errorf("Key group memory usage metrics are not expected:\n Expected: %#v\nActual: %#v\n", tst.wantedMemory, actualMemory)
				}

				if actualDistinctKeyGroups != tst.wantedDistintKeyGroups {
					t.Errorf("Unexpected number of distinct key groups, expected: %d, actual: %d", tst.wantedDistintKeyGroups, actualDistinctKeyGroups)
				}
				break
			}
		})
	}
}
