package exporter

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gomodule/redigo/redis"
)

// clusterKeyConn keeps database selection in the dial options of each cluster
// connection. SELECT on an existing connection would be lost after a redirect.
// local is a separate connection to the scraped node for scans. Selecting its
// database must not change the main scrape connection (e.g. for Search metrics).
type clusterKeyConn struct {
	redis.Conn
	local           redis.Conn
	connect         func(int) (redis.Conn, error)
	connections     map[int]redis.Conn
	supportsMultiDB bool
}

func newClusterKeyConn(connectLocal func() (redis.Conn, error), connect func(int) (redis.Conn, error), supportsMultiDB bool) (*clusterKeyConn, error) {
	local, err := connectLocal()
	if err != nil {
		return nil, err
	}
	c := &clusterKeyConn{local: local, connect: connect, connections: make(map[int]redis.Conn), supportsMultiDB: supportsMultiDB}
	if _, err := c.selectDatabase("0"); err != nil {
		_ = local.Close()
		return nil, err
	}
	return c, nil
}

func (c *clusterKeyConn) selectDatabase(db string) (string, error) {
	if !c.supportsMultiDB {
		// Preserve the existing DB-0 behavior for Redis and older Valkey clusters.
		db = "0"
	}
	n, err := strconv.Atoi(db)
	if err != nil || n < 0 {
		return "", fmt.Errorf("invalid database index %q", db)
	}
	conn, ok := c.connections[n]
	if !ok {
		conn, err = c.connect(n)
		if err != nil {
			return "", err
		}
		c.connections[n] = conn
	}
	c.Conn = conn
	return strconv.Itoa(n), nil
}

func (c *clusterKeyConn) Close() error {
	firstErr := c.local.Close()
	for _, conn := range c.connections {
		if err := conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func selectRedisDatabase(c redis.Conn, db string) (string, error) {
	if cluster, ok := c.(*clusterKeyConn); ok {
		return cluster.selectDatabase(db)
	}
	_, err := doRedisCmd(c, "SELECT", db)
	return db, err
}

func supportsValkeyClusterDatabases(info string) bool {
	for line := range strings.SplitSeq(info, "\n") {
		if version, ok := strings.CutPrefix(strings.TrimSpace(line), "valkey_version:"); ok {
			major, _, ok := strings.Cut(version, ".")
			n, err := strconv.Atoi(major)
			return ok && err == nil && n >= 9
		}
	}
	return false
}

func clusterDatabaseCount(info string, config map[string]string) (int, error) {
	if !supportsValkeyClusterDatabases(info) {
		return 1, nil
	}
	if value, ok := config["cluster-databases"]; ok {
		count, err := strconv.Atoi(value)
		if err != nil || count < 1 {
			return 0, fmt.Errorf("invalid config value for key cluster-databases: %q", value)
		}
		return count, nil
	}
	// CONFIG may be disabled or denied. Include databases visible in INFO;
	// the number of empty databases beyond the highest visible DB is unknown.
	count := 1
	for line := range strings.SplitSeq(info, "\n") {
		name, _, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || !strings.HasPrefix(name, "db") {
			continue
		}
		if db, err := strconv.Atoi(name[2:]); err == nil && db >= 0 {
			count = max(count, db+1)
		}
	}
	return count, nil
}
