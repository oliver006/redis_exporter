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

// schemeIsTLS reports whether a redis URI uses a TLS scheme.
func schemeIsTLS(uri string) bool {
	return strings.HasPrefix(uri, "rediss://") || strings.HasPrefix(uri, "valkeys://")
}

// schemeFromURI returns the URI scheme (redis, rediss, valkey, valkeys),
// defaulting to "redis" when no recognised scheme prefix is present.
func schemeFromURI(uri string) string {
	// "valkeys" before "valkey" and "rediss" before "redis" so the longer
	// (TLS) prefixes win.
	for _, s := range []string{"rediss", "valkeys", "valkey", "redis"} {
		if strings.HasPrefix(uri, s+"://") {
			return s
		}
	}
	return "redis"
}

// startupNodeFromURI strips the scheme from a redis URI and returns a
// host:port string suitable for redisc.Cluster.StartupNodes, defaulting the
// port to 6379 when absent. Callers must pass a URI that includes a scheme.
func startupNodeFromURI(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("failed to parse cluster URI: %w", err)
	}
	if u.Port() == "" {
		return u.Host + ":6379", nil
	}
	return u.Host, nil
}

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
		redis.DialUseTLS(schemeIsTLS(uri)),
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

// canonicalPasswordKey normalises a redis URI to the form used as a key in the
// password maps: the user from options is applied and a bare ":" left by a
// username-without-password is stripped. The same normalisation must be used
// when caching and looking up passwords so the keys match.
func (e *Exporter) canonicalPasswordKey(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}

	if e.options.User != "" {
		u.User = url.User(e.options.User)
	}
	key := u.String()

	// strip solo ":" if present in uri that has a username (and no pwd)
	key = strings.Replace(key, fmt.Sprintf(":@%s", u.Host), fmt.Sprintf("@%s", u.Host), 1)
	return key, nil
}

func (e *Exporter) lookupPasswordInPasswordMap(uri string) (string, bool) {
	key, err := e.canonicalPasswordKey(uri)
	if err != nil {
		return "", false
	}

	log.Debugf("looking up in pwd map, uri: %s", key)

	// Guards both PasswordMap and discoveredNodesPasswordCache against a concurrent
	// reloadPwdFile. Not the embedded e.Mutex: Collect holds that across a whole
	// scrape, and the scrape path reaches this lookup.
	e.passwordUpdateMutex.Lock()
	defer e.passwordUpdateMutex.Unlock()

	if pwd, ok := e.options.PasswordMap[key]; ok && pwd != "" {
		return pwd, true
	}

	if pwd, ok := e.discoveredNodesPasswordCache[key]; ok && pwd != "" {
		log.Debugf("found password for discovered node %s", key)
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
	return e.connectToRedisClusterWithURI(e.redisAddr)
}

func (e *Exporter) connectToRedisClusterWithURI(uri string) (redis.Conn, error) {
	if !strings.Contains(uri, "://") {
		uri = "redis://" + uri
	}

	options, err := e.configureOptions(uri)
	if err != nil {
		return nil, err
	}

	// remove url scheme for redis.Cluster.StartupNodes
	startupNode, err := startupNodeFromURI(uri)
	if err != nil {
		return nil, err
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

func doRedisCmd(c redis.Conn, cmd string, args ...any) (any, error) {
	log.Debugf("c.Do() - running command: %s args: [%v]", cmd, args)
	res, err := c.Do(cmd, args...)
	if err != nil {
		log.Debugf("c.Do() - err: %s", err)
	}
	log.Debugf("c.Do() - done")
	return res, err
}
