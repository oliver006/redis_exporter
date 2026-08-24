package exporter

import (
	"fmt"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/gomodule/redigo/redis"
	"github.com/mna/redisc"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestSupportsValkeyClusterScan(t *testing.T) {
	tests := []struct {
		name string
		info string
		want bool
	}{
		{name: "Valkey 9.1", info: "# Server\r\nvalkey_version:9.1.0\r\n", want: true},
		{name: "later Valkey", info: "valkey_version:10.0.0\n", want: true},
		{name: "Valkey 9.0", info: "valkey_version:9.0.5\n", want: false},
		{name: "Redis", info: "redis_version:9.1.0\n", want: false},
		{name: "invalid", info: "valkey_version:dev\n", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := supportsValkeyClusterScan(test.info); got != test.want {
				t.Fatalf("supportsValkeyClusterScan() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestSupportsValkeyClusterDatabases(t *testing.T) {
	for _, test := range []struct {
		info string
		want bool
	}{
		{info: "valkey_version:9.0.0\n", want: true},
		{info: "valkey_version:9.1.0\n", want: true},
		{info: "valkey_version:8.1.0\n", want: false},
		{info: "redis_version:9.0.0\n", want: false},
	} {
		if got := supportsValkeyClusterDatabases(test.info); got != test.want {
			t.Errorf("supportsValkeyClusterDatabases(%q) = %t, want %t", test.info, got, test.want)
		}
	}
}

func TestClusterDatabaseCount(t *testing.T) {
	if got, err := clusterDatabaseCount(map[string]string{"cluster-databases": "8"}); err != nil || got != 8 {
		t.Fatalf("clusterDatabaseCount() = %d, %v; want 8, nil", got, err)
	}
	if got, err := clusterDatabaseCount(map[string]string{"databases": "16"}); err != nil || got != 0 {
		t.Fatalf("clusterDatabaseCount() = %d, %v; want 0, nil", got, err)
	}
	for _, value := range []string{"0", "invalid"} {
		if _, err := clusterDatabaseCount(map[string]string{"cluster-databases": value}); err == nil {
			t.Fatalf("clusterDatabaseCount(%q) unexpectedly succeeded", value)
		}
	}
}

func TestClusterKeyConnOwnsOneConnectionPerDatabase(t *testing.T) {
	var connected []int
	var commands []string
	connections := make(map[int]*stubRedisConn)
	connect := func(db int) (redis.Conn, error) {
		connected = append(connected, db)
		conn := &stubRedisConn{do: func(command string, _ ...any) (any, error) {
			commands = append(commands, fmt.Sprintf("db%d:%s", db, command))
			return "value", nil
		}}
		connections[db] = conn
		return conn, nil
	}

	conn, err := newClusterKeyConn(connect, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Do("GET", "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Do("SELECT", "3"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Do("GET", "second"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Do("SELECT", 3); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(connected, []int{0, 3}) {
		t.Fatalf("connected databases = %v, want [0 3]", connected)
	}
	if !reflect.DeepEqual(commands, []string{"db0:GET", "db3:GET"}) {
		t.Fatalf("commands = %v", commands)
	}
	if got := conn.scanCommand(); got != "CLUSTERSCAN" {
		t.Fatalf("scanCommand() = %q", got)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	for db, connection := range connections {
		if !connection.closed {
			t.Errorf("database %d connection was not closed", db)
		}
	}
}

func TestScanKeysUsesOpaqueClusterScanCursor(t *testing.T) {
	var commands []string
	var cursors []string
	call := 0
	conn := &clusterScanStub{&stubRedisConn{do: func(command string, args ...any) (any, error) {
		commands = append(commands, command)
		cursors = append(cursors, fmt.Sprint(args[0]))
		call++
		if call == 1 {
			return []any{[]byte("abc123-{slot}-42"), []any{[]byte("key:1")}}, nil
		}
		return []any{[]byte("0"), []any{[]byte("key:2")}}, nil
	}}}

	keys, err := redis.Strings(scanKeys(conn, "key:*", 10))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(keys, []string{"key:1", "key:2"}) {
		t.Fatalf("keys = %v", keys)
	}
	if !reflect.DeepEqual(commands, []string{"CLUSTERSCAN", "CLUSTERSCAN"}) {
		t.Fatalf("commands = %v", commands)
	}
	if !reflect.DeepEqual(cursors, []string{"0", "abc123-{slot}-42"}) {
		t.Fatalf("cursors = %v", cursors)
	}
}

func TestClusterCheckKeyDatabase(t *testing.T) {
	tests := []struct {
		name          string
		multiDB       bool
		requestedDB   string
		wantDB        string
		wantConnected []int
	}{
		{name: "Valkey 9 keeps database", multiDB: true, requestedDB: "3", wantDB: "3", wantConnected: []int{0, 3}},
		{name: "legacy cluster uses database zero", requestedDB: "11", wantDB: "0", wantConnected: []int{0}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var connected []int
			connect := func(db int) (redis.Conn, error) {
				connected = append(connected, db)
				return &stubRedisConn{do: func(command string, _ ...any) (any, error) {
					switch command {
					case "TYPE":
						return "list", nil
					case "MEMORY":
						return int64(64), nil
					case "LLEN":
						return int64(2), nil
					default:
						return nil, fmt.Errorf("unexpected command %s", command)
					}
				}}, nil
			}
			conn, err := newClusterKeyConn(connect, test.multiDB, true)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()

			exporter, err := NewRedisExporter("redis://unused:6379", Options{Namespace: "test", IsCluster: true})
			if err != nil {
				t.Fatal(err)
			}
			metrics := make(chan prometheus.Metric, 2)
			exporter.extractCheckKeyMetricsNotPipelined(metrics, conn, []dbKeyPair{{db: test.requestedDB, key: "jobs"}})
			close(metrics)

			if !reflect.DeepEqual(connected, test.wantConnected) {
				t.Fatalf("connected databases = %v, want %v", connected, test.wantConnected)
			}
			for metric := range metrics {
				got := &dto.Metric{}
				if err := metric.Write(got); err != nil {
					t.Fatal(err)
				}
				labels := make(map[string]string)
				for _, label := range got.Label {
					labels[label.GetName()] = label.GetValue()
				}
				if want := "db" + test.wantDB; labels["db"] != want {
					t.Errorf("metric has database label %q, want %q", labels["db"], want)
				}
			}
		})
	}
}

func TestGatherClusterKeyGroupMetrics(t *testing.T) {
	var commands []string
	conn := &clusterScanStub{&stubRedisConn{do: func(command string, args ...any) (any, error) {
		commands = append(commands, command)
		switch command {
		case "CLUSTERSCAN":
			return []any{[]byte("0"), []any{[]byte("{same}:1"), []byte("{same}:2")}}, nil
		case "EVALSHA":
			if got := fmt.Sprint(args[1]); got != "2" {
				return nil, fmt.Errorf("script key count = %s, want 2", got)
			}
			return []any{int64(0), []any{
				[]any{[]byte("same"), int64(1), int64(32)},
				[]any{[]byte("same"), int64(1), int64(64)},
			}}, nil
		default:
			return nil, fmt.Errorf("unexpected command %s", command)
		}
	}}}

	groups, err := gatherClusterKeyGroupMetrics(conn, 10, []string{"{(.*)}"})
	if err != nil {
		t.Fatal(err)
	}
	if got := groups["same"]; got == nil || got.count != 2 || got.memoryUsage != 96 {
		t.Fatalf("group metrics = %#v", got)
	}
	if !reflect.DeepEqual(commands, []string{"CLUSTERSCAN", "EVALSHA"}) {
		t.Fatalf("commands = %v", commands)
	}
}

func TestValkey91ClusterDatabasesAndScan(t *testing.T) {
	uri := os.Getenv("TEST_VALKEY91_CLUSTER_URI")
	if uri == "" {
		t.Skip("TEST_VALKEY91_CLUSTER_URI is not set")
	}

	keys := make([]string, 3)
	for i := 0; ; i++ {
		tag := fmt.Sprintf("exporter-slot-%d", i)
		bucket := redisc.Slot("{"+tag+"}") * len(keys) / redisc.HashSlots
		if keys[bucket] == "" {
			keys[bucket] = fmt.Sprintf("valkey91:{%s}:key", tag)
		}
		if keys[0] != "" && keys[1] != "" && keys[2] != "" {
			break
		}
	}

	fixtureExporter, err := NewRedisExporter(uri, Options{IsCluster: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, db := range []int{0, 3} {
		conn, err := fixtureExporter.connectToRedisClusterDatabase(db)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		for _, key := range keys {
			if _, err := conn.Do("SET", key, "value"); err != nil {
				t.Fatal(err)
			}
		}
		defer func(conn redis.Conn) {
			for _, key := range keys {
				_, _ = conn.Do("DEL", key)
			}
		}(conn)
	}

	exporter, err := NewRedisExporter(uri, Options{
		Namespace:                 "valkey91",
		IsCluster:                 true,
		CheckKeys:                 "db0=valkey91:*,db3=valkey91:*",
		CountKeys:                 "db0=valkey91:*,db3=valkey91:*",
		CheckKeyGroups:            `^valkey91:{(.-)}:`,
		CheckKeysBatchSize:        10,
		MaxDistinctKeyGroups:      100,
		DisableExportingKeyValues: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(exporter)
	defer server.Close()
	body := downloadURL(t, server.URL+"/metrics")

	for _, db := range []string{"db0", "db3"} {
		if want := fmt.Sprintf(`valkey91_keys_count{db="%s",key="valkey91:*"} 3`, db); !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
		for _, key := range keys {
			want := fmt.Sprintf(`valkey91_key_size{db="%s",key="%s"} 5`, db, key)
			if !strings.Contains(body, want) {
				t.Errorf("missing %s", want)
			}
		}
	}
	if !strings.Contains(body, `valkey91_key_group_count{db="db3",key_group="exporter-slot-`) {
		t.Error("missing database 3 cluster key-group metrics")
	}
}
