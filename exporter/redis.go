package exporter

import (
	"net/url"
	"strings"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/mna/redisc"
	log "github.com/sirupsen/logrus"
)

func (e *Exporter) connectToRedis() (redis.Conn, error) {
	tlsConfig, err := e.CreateClientTLSConfig()
	if err != nil {
		return nil, err
	}

	options := []redis.DialOption{
		redis.DialConnectTimeout(e.options.ConnectionTimeouts),
		redis.DialReadTimeout(e.options.ConnectionTimeouts),
		redis.DialWriteTimeout(e.options.ConnectionTimeouts),
		redis.DialTLSConfig(tlsConfig),
	}

	if e.options.User != "" {
		options = append(options, redis.DialUsername(e.options.User))
	}

	if e.options.Password != "" {
		options = append(options, redis.DialPassword(e.options.Password))
	}

	uri := e.redisAddr
	if !strings.Contains(uri, "://") {
		uri = "redis://" + uri
	}
	log.Debugf("Trying DialURL(): %s", uri)

	if e.options.PasswordMap[uri] != "" {
		options = append(options, redis.DialPassword(e.options.PasswordMap[uri]))
	}

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
	tlsConfig, err := e.CreateClientTLSConfig()
	if err != nil {
		return nil, err
	}

	options := []redis.DialOption{
		redis.DialConnectTimeout(e.options.ConnectionTimeouts),
		redis.DialReadTimeout(e.options.ConnectionTimeouts),
		redis.DialWriteTimeout(e.options.ConnectionTimeouts),
		redis.DialTLSConfig(tlsConfig),
	}

	if e.options.User != "" {
		options = append(options, redis.DialUsername(e.options.User))
	}

	if e.options.Password != "" {
		options = append(options, redis.DialPassword(e.options.Password))
	}

	uri := e.redisAddr
	if strings.Contains(uri, "://") {
		url, _ := url.Parse(uri)
		if url.Port() == "" {
			uri = url.Host + ":6379"
		} else {
			uri = url.Host
		}
	} else {
		if frags := strings.Split(uri, ":"); len(frags) != 2 {
			uri = uri + ":6379"
		}
	}
	log.Debugf("Trying DialURL(): %s", uri)

	if e.options.PasswordMap[uri] != "" {
		options = append(options, redis.DialPassword(e.options.PasswordMap[uri]))
	}

	log.Debugf("Creating cluster object")
	cluster := redisc.Cluster{
		StartupNodes: []string{uri},
		DialOptions:  options,
	}
	log.Debugf("Running refresh on cluster object")
	if err := cluster.Refresh(); err != nil {
		log.Debugf("Cluster refresh failed: %v", err)
	}

	log.Debugf("Creating redis connection object")
	conn, err := cluster.Dial()
	if err != nil {
		log.Debugf("Dial failed: %v", err)
	}

	c, err := redisc.RetryConn(conn, 10, 100*time.Millisecond)
	if err != nil {
		log.Debugf("RetryConn failed: %v", err)
	}

	return c, err
}

func doRedisCmd(c redis.Conn, cmd string, args ...interface{}) (interface{}, error) {
	log.Debugf("c.Do() - running command: %s %s", cmd, args)
	res, err := c.Do(cmd, args...)
	if err != nil {
		log.Debugf("c.Do() - err: %s", err)
	}
	log.Debugf("c.Do() - done")
	return res, err
}
