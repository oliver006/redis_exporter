package exporter

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/mna/redisc"
	log "github.com/sirupsen/logrus"
)

func (e *Exporter) configureOptions(uri string) ([]redis.DialOption, error) {
	tlsConfig, err := e.CreateClientTLSConfig()
	if err != nil {
		return nil, err
	}

	options := []redis.DialOption{
		redis.DialConnectTimeout(e.options.ConnectionTimeouts),
		redis.DialReadTimeout(e.options.ConnectionTimeouts),
		redis.DialWriteTimeout(e.options.ConnectionTimeouts),
		redis.DialTLSConfig(tlsConfig),
		redis.DialUseTLS(strings.HasPrefix(e.redisAddr, "rediss://")),
	}

	if e.options.User != "" {
		options = append(options, redis.DialUsername(e.options.User))
	}

	if e.options.Password != "" {
		options = append(options, redis.DialPassword(e.options.Password))
	}

	if pwd, ok := e.lookupPasswordInPasswordMap(uri); ok && pwd != "" {
		options = append(options, redis.DialPassword(pwd))
	}

	return options, nil
}

func (e *Exporter) lookupPasswordInPasswordMap(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", false
	}

	if e.options.User != "" {
		u.User = url.User(e.options.User)
	}
	uri = u.String()

	// strip solo ":" if present in uri that has a username (and no pwd)
	uri = strings.Replace(uri, fmt.Sprintf(":@%s", u.Host), fmt.Sprintf("@%s", u.Host), 1)

	log.Debugf("looking up in pwd map, uri: %s", uri)
	if pwd, ok := e.options.PasswordMap[uri]; ok && pwd != "" {
		return pwd, true
	}
	return "", false
}

func (e *Exporter) connectToRedis() (redis.Conn, error) {
	uri := e.redisAddr
	if !strings.Contains(uri, "://") {
		uri = "redis://" + uri
	}

	options, err := e.configureOptions(uri)
	if err != nil {
		return nil, err
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

func (e *Exporter) connectToRedisCluster() (redis.Conn, error) {
	return e.connectToRedisClusterDatabase(0)
}

func (e *Exporter) connectToRedisClusterDatabase(db int) (redis.Conn, error) {
	if db < 0 {
		return nil, fmt.Errorf("invalid database index %d", db)
	}
	uri := e.redisAddr
	if !strings.Contains(uri, "://") {
		uri = "redis://" + uri
	}

	options, err := e.configureOptions(uri)
	if err != nil {
		return nil, err
	}
	if db > 0 {
		options = append(options, redis.DialDatabase(db))
	}

	// remove url scheme for redis.Cluster.StartupNodes
	if strings.Contains(uri, "://") {
		u, _ := url.Parse(uri)
		if u.Port() == "" {
			uri = u.Host + ":6379"
		} else {
			uri = u.Host
		}
	} else {
		if frags := strings.Split(uri, ":"); len(frags) != 2 {
			uri = uri + ":6379"
		}
	}

	log.Debugf("Creating cluster object")
	cluster := redisc.Cluster{
		StartupNodes: []string{uri},
		DialOptions:  options,
	}
	log.Debugf("Running refresh on cluster object")
	if err := cluster.Refresh(); err != nil {
		log.Errorf("Cluster refresh failed: %v", err)
		return nil, fmt.Errorf("cluster refresh failed: %w", err)
	}

	log.Debugf("Creating redis connection object")
	conn, err := cluster.Dial()
	if err != nil {
		log.Errorf("Dial failed: %v", err)
		return nil, fmt.Errorf("dial failed: %w", err)
	}

	c, err := redisc.RetryConn(conn, 10, 100*time.Millisecond)
	if err != nil {
		log.Errorf("RetryConn failed: %v", err)
		return nil, fmt.Errorf("retryConn failed: %w", err)
	}

	return c, err
}

// clusterKeyConn owns one redirect-aware cluster connection per logical database.
// Database selection must happen while dialing so redisc replacements after
// MOVED/ASK redirects stay in the same database.
type clusterKeyConn struct {
	redis.Conn
	connect         func(int) (redis.Conn, error)
	connections     map[int]redis.Conn
	supportsMultiDB bool
	useClusterScan  bool
}

func newClusterKeyConn(connect func(int) (redis.Conn, error), supportsMultiDB, useClusterScan bool) (*clusterKeyConn, error) {
	c := &clusterKeyConn{
		connect:         connect,
		connections:     make(map[int]redis.Conn),
		supportsMultiDB: supportsMultiDB,
		useClusterScan:  useClusterScan,
	}
	if _, err := c.selectDatabase("0"); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *clusterKeyConn) connection(db int) (redis.Conn, error) {
	if conn, ok := c.connections[db]; ok {
		return conn, nil
	}
	conn, err := c.connect(db)
	if err != nil {
		return nil, err
	}
	c.connections[db] = conn
	return conn, nil
}

func (c *clusterKeyConn) selectDatabase(db string) (string, error) {
	if !c.supportsMultiDB {
		db = "0"
	}
	dbNumber, err := strconv.Atoi(db)
	if err != nil || dbNumber < 0 {
		return db, fmt.Errorf("invalid database index %q", db)
	}
	conn, err := c.connection(dbNumber)
	if err != nil {
		return db, err
	}
	c.Conn = conn
	return db, nil
}

func (c *clusterKeyConn) Close() error {
	var firstErr error
	for _, conn := range c.connections {
		if err := conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (c *clusterKeyConn) scanCommand() string {
	if c.useClusterScan {
		return "CLUSTERSCAN"
	}
	return "SCAN"
}

type databaseSelector interface {
	selectDatabase(string) (string, error)
}

func selectRedisDatabase(c redis.Conn, db string) (string, error) {
	if selector, ok := c.(databaseSelector); ok {
		return selector.selectDatabase(db)
	}
	_, err := doRedisCmd(c, "SELECT", db)
	return db, err
}

func doRedisCmd(c redis.Conn, cmd string, args ...any) (any, error) {
	log.Debugf("c.Do() - running command: %s args: [%v]", cmd, args)
	res, err := c.Do(cmd, args...)
	if err != nil {
		log.Debugf("c.Do() - err: %s", err)
	}
	log.Debugf("c.Do() - done")
	return res, err
}
