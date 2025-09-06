package exporter

import (
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/mna/redisc"
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

	slog.Debug("Looking up URI in pwd map", "uri", uri)
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

	slog.Debug("Trying to dial URL", "uri", uri)
	c, err := redis.DialURL(uri, options...)
	if err != nil {
		slog.Debug("Failed to dial URL", "error", err)
		if frags := strings.Split(e.redisAddr, "://"); len(frags) == 2 {
			slog.Debug("Trying to dial", "protocol", frags[0], "address", frags[1])
			c, err = redis.Dial(frags[0], frags[1], options...)
		} else {
			slog.Debug("Trying to dial TCP", "address", e.redisAddr)
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

	options, err := e.configureOptions(uri)
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

	slog.Debug("Creating cluster object")
	cluster := redisc.Cluster{
		StartupNodes: []string{uri},
		DialOptions:  options,
	}
	slog.Debug("Running refresh on cluster object")
	if err := cluster.Refresh(); err != nil {
		slog.Error("Cluster refresh failed", "error", err)
		return nil, fmt.Errorf("cluster refresh failed: %w", err)
	}

	slog.Debug("Creating redis connection object")
	conn, err := cluster.Dial()
	if err != nil {
		slog.Error("Dial failed", "error", err)
		return nil, fmt.Errorf("dial failed: %w", err)
	}

	c, err := redisc.RetryConn(conn, 10, 100*time.Millisecond)
	if err != nil {
		slog.Error("RetryConn failed", "error", err)
		return nil, fmt.Errorf("retryConn failed: %w", err)
	}

	return c, err
}

func doRedisCmd(c redis.Conn, cmd string, args ...interface{}) (interface{}, error) {
	slog.Debug("c.Do() - running command", "cmd", cmd, "args", args)
	res, err := c.Do(cmd, args...)
	if err != nil {
		slog.Debug("Redis command failed", "error", err)
	}
	slog.Debug("c.Do() - done")
	return res, err
}
