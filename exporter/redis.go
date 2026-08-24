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

type clusterKeyConn struct {
	connect     func(int) (redis.Conn, error)
	connections map[int]redis.Conn
	db          int
	clusterScan bool
	err         error
}

func newClusterKeyConn(connect func(int) (redis.Conn, error), clusterScan bool) (*clusterKeyConn, error) {
	c := &clusterKeyConn{
		connect:     connect,
		connections: make(map[int]redis.Conn),
		clusterScan: clusterScan,
	}
	if _, err := c.connection(0); err != nil {
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
		c.err = err
		return nil, err
	}
	c.connections[db] = conn
	return conn, nil
}

func (c *clusterKeyConn) Do(cmd string, args ...any) (any, error) {
	if strings.EqualFold(cmd, "SELECT") {
		if len(args) != 1 {
			return nil, fmt.Errorf("SELECT expects one database argument")
		}
		db, err := strconv.Atoi(fmt.Sprint(args[0]))
		if err != nil || db < 0 {
			return nil, fmt.Errorf("invalid database index %q", args[0])
		}
		if _, err := c.connection(db); err != nil {
			return nil, err
		}
		c.db = db
		return "OK", nil
	}

	conn, err := c.connection(c.db)
	if err != nil {
		return nil, err
	}
	return conn.Do(cmd, args...)
}

func (c *clusterKeyConn) Send(string, ...any) error {
	return fmt.Errorf("cluster key connection does not support pipelining")
}

func (c *clusterKeyConn) Flush() error {
	return fmt.Errorf("cluster key connection does not support pipelining")
}

func (c *clusterKeyConn) Receive() (any, error) {
	return nil, fmt.Errorf("cluster key connection does not support pipelining")
}

func (c *clusterKeyConn) Err() error {
	return c.err
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
	if c.clusterScan {
		return "CLUSTERSCAN"
	}
	return "SCAN"
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
