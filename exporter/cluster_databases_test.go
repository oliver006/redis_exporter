package exporter

import (
	"bufio"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/mna/redisc"
)

func TestClusterDatabaseCount(t *testing.T) {
	for _, test := range []struct {
		name, info string
		config     map[string]string
		want       int
	}{
		{"redis", "redis_version:8.10.2\r\n", map[string]string{"databases": "16"}, 1},
		{"older-valkey", "valkey_version:8.1.0\r\n", nil, 1},
		{"configured", "valkey_version:9.0.0\r\n", map[string]string{"databases": "16", "cluster-databases": "4"}, 4},
		{"config-denied", "valkey_version:9.1.2\r\n# Keyspace\r\ndb3:keys=1,expires=0\r\n", nil, 4},
		{"empty", "valkey_version:9.1.2\r\n", nil, 1},
		{"future", "valkey_version:10.0.0\r\n", map[string]string{"cluster-databases": "8"}, 8},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := clusterDatabaseCount(test.info, test.config)
			if err != nil || got != test.want {
				t.Fatalf("clusterDatabaseCount() = %d, %v; want %d", got, err, test.want)
			}
		})
	}
	for _, value := range []string{"0", "-1", "invalid"} {
		if _, err := clusterDatabaseCount("valkey_version:9.0.0", map[string]string{"cluster-databases": value}); err == nil {
			t.Errorf("accepted invalid cluster-databases %q", value)
		}
	}
}

// Exercise the real redisc redirect path, including SELECT on the replacement
// socket. A successful initial SELECT alone is insufficient for cluster DBs.
func TestClusterDatabaseRedirects(t *testing.T) {
	for _, redirect := range []string{"MOVED", "ASK"} {
		for _, denied := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/denied=%t", redirect, denied), func(t *testing.T) {
				target := startClusterDatabaseServer(t, func(args []string, db *int, asking *bool, _ string) string {
					switch args[0] {
					case "SELECT":
						if denied {
							return "-NOPERM database access denied\r\n"
						}
						*db, _ = strconv.Atoi(args[1])
						return "+OK\r\n"
					case "ASKING":
						*asking = true
						return "+OK\r\n"
					case "GET":
						if redirect == "ASK" && !*asking {
							return "-ERR missing ASKING\r\n"
						}
						return fmt.Sprintf(":%d\r\n", *db)
					default:
						return "*0\r\n"
					}
				})
				source := startClusterDatabaseServer(t, func(args []string, db *int, _ *bool, addr string) string {
					switch args[0] {
					case "SELECT":
						*db, _ = strconv.Atoi(args[1])
						return "+OK\r\n"
					case "GET":
						if *db != 3 {
							return "-ERR wrong source database\r\n"
						}
						return fmt.Sprintf("-%s %d %s\r\n", redirect, redisc.Slot("key"), target)
					default:
						// Advertise this node so GET must follow a redirect.
						_, port, _ := net.SplitHostPort(addr)
						return fmt.Sprintf("*1\r\n*3\r\n:0\r\n:16383\r\n*2\r\n$9\r\n127.0.0.1\r\n:%s\r\n", port)
					}
				})
				e := connectionTestExporter(t, "redis://"+source)
				c, err := e.connectToRedisClusterDatabase(3)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				got, err := redis.Int(c.Do("GET", "key"))
				if denied {
					if err == nil || !strings.Contains(err.Error(), "NOPERM") {
						t.Fatalf("GET error = %v; want NOPERM", err)
					}
				} else if err != nil || got != 3 {
					t.Fatalf("redirected GET = %d, %v; want database 3", got, err)
				}
			})
		}
	}
}

func startClusterDatabaseServer(t *testing.T, reply func([]string, *int, *bool, string) string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				reader := bufio.NewReader(c)
				db, asking := 0, false
				for {
					args, err := readRESPArray(reader)
					if err != nil {
						return
					}
					if _, err := c.Write([]byte(reply(args, &db, &asking, listener.Addr().String()))); err != nil {
						return
					}
				}
			}()
		}
	}()
	return listener.Addr().String()
}

func TestValkeyClusterDatabases(t *testing.T) {
	uri := os.Getenv("TEST_VALKEY_CLUSTER_DATABASES_URI")
	if uri == "" {
		t.Skip("TEST_VALKEY_CLUSTER_DATABASES_URI is not set")
	}
	e := connectionTestExporter(t, uri)
	local, err := e.connectToRedis()
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()

	// Same names, different values and cardinalities in each DB, on every shard.
	keys := make([]string, 3)
	for i := 0; keys[0] == "" || keys[1] == "" || keys[2] == ""; i++ {
		key := fmt.Sprintf("cluster-db-test:{%d}:key", i)
		bucket := redisc.Slot(key) * len(keys) / redisc.HashSlots
		keys[bucket] = key
	}
	for _, db := range []int{0, 3} {
		c, err := e.connectToRedisClusterDatabase(db)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		for _, key := range keys {
			for _, name := range []string{key, key + ":stream"} {
				if _, err := c.Do("DEL", name); err != nil {
					t.Fatal(err)
				}
				defer c.Do("DEL", name)
			}
			if _, err := c.Do("SET", key, db+10); err != nil {
				t.Fatal(err)
			}
			for range db + 1 {
				if _, err := c.Do("XADD", key+":stream", "*", "field", "value"); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	var checks, streams []string
	for _, db := range []int{0, 3} {
		for _, key := range keys {
			checks = append(checks, fmt.Sprintf("db%d=%s", db, key))
			streams = append(streams, fmt.Sprintf("db%d=%s:stream", db, key))
		}
	}
	for _, configCommand := range []string{"CONFIG", "-"} {
		t.Run(configCommand, func(t *testing.T) {
			exporter, err := NewRedisExporter(uri, Options{
				Namespace: "test", IsCluster: true, ConfigCommandName: configCommand,
				CheckSingleKeys: strings.Join(checks, ","), CheckSingleStreams: strings.Join(streams, ","),
				CountKeys:      "db0=cluster-db-test:*,db3=cluster-db-test:*",
				CheckKeyGroups: `^(cluster%-db%-test):`, MaxDistinctKeyGroups: 10, CheckKeysBatchSize: 10,
				InclMetricsForEmptyDatabases: true,
				LuaScript:                    map[string][]byte{"db.lua": []byte(fmt.Sprintf(`return {"value", redis.call("GET", %q)}`, keys[0]))},
			})
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(exporter)
			defer server.Close()
			body := downloadURL(t, server.URL+"/metrics")
			for _, db := range []int{0, 3} {
				for _, key := range keys {
					for _, want := range []string{
						fmt.Sprintf(`test_key_value{db="db%d",key="%s"} %d`, db, key, db+10),
						fmt.Sprintf(`test_stream_length{db="db%d",stream="%s:stream"} %d`, db, key, db+1),
					} {
						if !strings.Contains(body, want+"\n") {
							t.Errorf("missing %s", want)
						}
					}
				}
				// Each node owns two of the six keys. Whole-cluster counts would be 6.
				for _, want := range []string{
					fmt.Sprintf(`test_keys_count{db="db%d",key="cluster-db-test:*"} 2`, db),
					fmt.Sprintf(`test_key_group_count{db="db%d",key_group="cluster-db-test"} 2`, db),
				} {
					if !strings.Contains(body, want+"\n") {
						t.Errorf("missing node-local metric %s", want)
					}
				}
			}
			if !strings.Contains(body, `test_db_keys{db="db2"} 0`+"\n") {
				t.Error("missing empty database 2")
			}
			// Scans must not change the DB used by later collectors.
			if !strings.Contains(body, `test_script_values{filename="db.lua",key="value"} 10`+"\n") {
				t.Error("main scrape connection did not remain in database 0")
			}
		})
	}
	t.Run("node-local-patterns", func(t *testing.T) {
		exporter, err := NewRedisExporter(uri, Options{
			Namespace: "test", IsCluster: true, CheckKeysBatchSize: 1,
			// End key reads on a remote shard before stream-pattern scans.
			CheckKeys:    "db0=cluster-db-test:*:key,db3=cluster-db-test:*:key,db3=" + keys[2],
			CheckStreams: "db0=cluster-db-test:*,db3=cluster-db-test:*",
		})
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(exporter)
		defer server.Close()
		body := downloadURL(t, server.URL+"/metrics")
		for _, db := range []int{0, 3} {
			if want := fmt.Sprintf(`test_key_value{db="db%d",key="%s"} %d`, db, keys[0], db+10); !strings.Contains(body, want+"\n") {
				t.Errorf("missing local key %s", want)
			}
			for i, key := range keys {
				want := fmt.Sprintf(`test_stream_length{db="db%d",stream="%s:stream"} %d`, db, key, db+1)
				if got := strings.Contains(body, want+"\n"); got != (i == 0) {
					t.Errorf("stream %s present = %t; want %t", want, got, i == 0)
				}
			}
		}
	})

	t.Run("database-acl-and-config-denied", func(t *testing.T) {
		slots, err := redis.Values(local.Do("CLUSTER", "SLOTS"))
		if err != nil {
			t.Fatal(err)
		}
		for _, slot := range slots {
			fields, _ := redis.Values(slot, nil)
			node, _ := redis.Values(fields[2], nil)
			host, _ := redis.String(node[0], nil)
			port, _ := redis.Int(node[1], nil)
			admin, err := redis.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = admin.Do("ACL", "DELUSER", "cluster-db-test")
				_ = admin.Close()
			})
			if _, err := admin.Do("ACL", "SETUSER", "cluster-db-test", "reset", "on", ">test-password", "+@all", "~*", "resetdbs", "db=3", "-config"); err != nil {
				t.Fatal(err)
			}
		}
		exporter, err := NewRedisExporter(uri, Options{
			Namespace: "test", IsCluster: true, User: "cluster-db-test", Password: "test-password",
			CheckSingleKeys:    "db3=" + keys[0] + ",db0=" + keys[0] + ",db3=" + keys[2],
			CheckSingleStreams: "db3=" + keys[2] + ":stream",
			CheckKeyGroups:     `^(cluster%-db%-test):`, MaxDistinctKeyGroups: 10, CheckKeysBatchSize: 10,
		})
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(exporter)
		defer server.Close()
		body := downloadURL(t, server.URL+"/metrics")
		for _, want := range []string{
			fmt.Sprintf(`test_key_value{db="db3",key="%s"} 13`, keys[0]),
			fmt.Sprintf(`test_key_value{db="db3",key="%s"} 13`, keys[2]),
			fmt.Sprintf(`test_stream_length{db="db3",stream="%s:stream"} 4`, keys[2]),
			`test_key_group_count{db="db3",key_group="cluster-db-test"} 2`,
		} {
			if !strings.Contains(body, want+"\n") {
				t.Errorf("missing permitted metric %s", want)
			}
		}
		if strings.Contains(body, `test_key_value{db="db0"`) {
			t.Error("exported a key from a denied database")
		}
	})

	// A failed selection must not read the previously selected database or
	// emit a metric with a misleading DB label; a later valid DB must still work.
	e.options.IsCluster = true
	e.options.CheckSingleKeys = "db3=" + keys[0] + ",db4=" + keys[0] + ",db0=" + keys[0]
	server := httptest.NewServer(e)
	defer server.Close()
	body := downloadURL(t, server.URL+"/metrics")
	if strings.Contains(body, `db="db4"`) || !strings.Contains(body, fmt.Sprintf(`test_key_value{db="db0",key="%s"} 10`, keys[0])) {
		t.Fatalf("failed database selection leaked or prevented subsequent reads:\n%s", body)
	}
}
