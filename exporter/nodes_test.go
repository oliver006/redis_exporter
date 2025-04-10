package exporter

import (
	"os"
	"slices"
	"testing"
)

func TestNodesGetClusterNodes(t *testing.T) {
	if os.Getenv("TEST_REDIS_CLUSTER_MASTER_URI") == "" {
		t.Skipf("TEST_REDIS_CLUSTER_MASTER_URI not set - skipping")
	}

	host := os.Getenv("TEST_REDIS_CLUSTER_MASTER_URI")
	e, _ := NewRedisExporter(host, Options{})
	c, err := e.connectToRedisCluster()
	if err != nil {
		t.Fatalf("connectToRedisCluster() err: %s", err)
	}
	defer c.Close()

	nodes, err := e.getClusterNodes(c)
	if err != nil {
		t.Fatalf("getClusterNodes() err: %s", err)
	}

	tsts := []struct {
		node string
		ok   bool
	}{
		{node: "127.0.0.1:7003", ok: true},
		{node: "127.0.0.1:7002", ok: true},
		{node: "127.0.0.1:7005", ok: true},
		{node: "127.0.0.1:7001", ok: true},
		{node: "127.0.0.1:7004", ok: true},
		{node: "127.0.0.1:7000", ok: true},

		{node: "", ok: false},
		{node: " ", ok: false},
		{node: "127.0.0.1", ok: false},
		{node: "127.0.0.1:8000", ok: false},
	}

	for _, tst := range tsts {
		t.Run(tst.node, func(t *testing.T) {
			found := slices.Contains(nodes, tst.node)
			if found != tst.ok {
				t.Errorf("Test failed for node: %s expected: %t, got: %t", tst.node, tst.ok, found)
			}
		})
	}
}

func TestParseClusterNodeString(t *testing.T) {
	tsts := []struct {
		line string
		node string
		ok   bool
	}{
		// The following are examples of the output of the CLUSTER NODES command.
		// https://redis.io/docs/latest/commands/cluster-nodes/
		{line: "07c37dfeb235213a872192d90877d0cd55635b91 127.0.0.1:30004@31004,hostname4 slave e7d1eecce10fd6bb5eb35b9f99a514335d9ba9ca 0 1426238317239 4 connected", node: "127.0.0.1:30004", ok: true},
		{line: "67ed2db8d677e59ec4a4cefb06858cf2a1a89fa1 127.0.0.1:30002@31002,hostname2 master - 0 1426238316232 2 connected 5461-10922", node: "127.0.0.1:30002", ok: true},
		{line: "292f8b365bb7edb5e285caf0b7e6ddc7265d2f4f 127.0.0.1:30003@31003,hostname3 master - 0 1426238318243 3 connected 10923-16383", node: "127.0.0.1:30003", ok: true},
		{line: "6ec23923021cf3ffec47632106199cb7f496ce01 127.0.0.1:30005@31005,hostname5 slave 67ed2db8d677e59ec4a4cefb06858cf2a1a89fa1 0 1426238316232 5 connected", node: "127.0.0.1:30005", ok: true},
		{line: "824fe116063bc5fcf9f4ffd895bc17aee7731ac3 127.0.0.1:30006@31006,hostname6 slave 292f8b365bb7edb5e285caf0b7e6ddc7265d2f4f 0 1426238317741 6 connected", node: "127.0.0.1:30006", ok: true},
		{line: "e7d1eecce10fd6bb5eb35b9f99a514335d9ba9ca 127.0.0.1:30001@31001,hostname1 myself,master - 0 0 1 connected 0-5460", node: "127.0.0.1:30001", ok: true},
		{line: "e7d1eecce10fd6bb5eb35b9f99a514335d9ba9ca 127.0.0.1:30001@31001 myself,master - 0 0 1 connected 0-5460", node: "127.0.0.1:30001", ok: true},

		{line: "07c37dfeb235213a872192d90877d0cd55635b91", ok: false},
		{line: "07c37dfeb235213a872192d90877d0cd55635b91 127.0.0.1:30004,hostname4 slave", ok: false},
		{line: "127.0.0.1:30005,hostname5", ok: false},
	}

	for _, tst := range tsts {
		t.Run(tst.line, func(t *testing.T) {
			node, ok := parseClusterNodeString(tst.line)

			if ok != tst.ok {
				t.Errorf("Test failed for line: %s", tst.line)
				return
			}
			if node != tst.node {
				t.Errorf("Node not matching, expected: %s, got: %s", tst.node, node)
				return
			}
		})
	}
}
