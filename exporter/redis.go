package exporter

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/mna/redisc"
	log "github.com/sirupsen/logrus"
)

func (e *Exporter) configureOptions(uri string, useTLS bool) ([]redis.DialOption, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("could not parse redis uri: %w", err)
	}
	tlsConfig, err := e.CreateClientTLSConfig(u.Hostname())
	if err != nil {
		return nil, err
	}

	options := []redis.DialOption{
		redis.DialConnectTimeout(e.options.ConnectionTimeouts),
		redis.DialReadTimeout(e.options.ConnectionTimeouts),
		redis.DialWriteTimeout(e.options.ConnectionTimeouts),
		redis.DialTLSConfig(tlsConfig),
		redis.DialUseTLS(useTLS),
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

	e.DiscoveredNodesPasswordsMutex.Lock()
	defer e.DiscoveredNodesPasswordsMutex.Unlock()
	if pwd, ok := e.DiscoveredNodesPasswords[uri]; ok && pwd != "" {
		log.Debugf("found password for discovered node %s", uri)
		return pwd, true
	}

	return "", false
}

func (e *Exporter) connectToRedis() (redis.Conn, error) {
	uri := e.redisAddr
	if !strings.Contains(uri, "://") {
		uri = "redis://" + uri
	}

	options, err := e.configureOptions(uri, strings.HasPrefix(uri, "rediss://") || strings.HasPrefix(uri, "valkeys://"))
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
	uri := e.redisAddr
	if !strings.Contains(uri, "://") {
		uri = "redis://" + uri
	}

	options, err := e.configureOptions(uri, strings.HasPrefix(uri, "rediss://") || strings.HasPrefix(uri, "valkeys://"))
	if err != nil {
		return nil, err
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

func (e *Exporter) connectToRedisClusterWithURI(uri string) (redis.Conn, error) {
	if !strings.Contains(uri, "://") {
		uri = "redis://" + uri
	}

	options, err := e.configureOptions(uri, strings.HasPrefix(uri, "rediss://") || strings.HasPrefix(uri, "valkeys://"))
	if err != nil {
		return nil, err
	}

	// remove url scheme for redis.Cluster.StartupNodes
	startupNode := uri
	if strings.Contains(startupNode, "://") {
		u, _ := url.Parse(startupNode)
		if u.Port() == "" {
			startupNode = u.Host + ":6379"
		} else {
			startupNode = u.Host
		}
	} else {
		if frags := strings.Split(startupNode, ":"); len(frags) != 2 {
			startupNode = startupNode + ":6379"
		}
	}

	log.Debugf("Creating cluster object")
	cluster := redisc.Cluster{
		StartupNodes: []string{startupNode},
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

func doRedisCmd(c redis.Conn, cmd string, args ...interface{}) (interface{}, error) {
	log.Debugf("c.Do() - running command: %s args: [%v]", cmd, args)
	res, err := c.Do(cmd, args...)
	if err != nil {
		log.Debugf("c.Do() - err: %s", err)
	}
	log.Debugf("c.Do() - done")
	return res, err
}
