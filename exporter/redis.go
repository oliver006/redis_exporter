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

// RedactURI removes any embedded credentials from a redis address so it is safe
// to write to logs. It clears the userinfo (user:pass@) and any credential-
// bearing query parameters (e.g. ?password=). It accepts both full URIs
// (redis://user:pass@host:port) and scheme-less authorities
// (user:pass@host:port). If the address embeds credentials but cannot be
// parsed, a fixed placeholder is returned rather than risk leaking the secret.
func RedactURI(uri string) string {
	hasScheme := strings.Contains(uri, "://")
	toParse := uri
	if !hasScheme {
		toParse = "redis://" + uri
	}

	u, err := url.Parse(toParse)
	if err != nil {
		if strings.Contains(uri, "@") {
			return "<redacted>"
		}
		return uri
	}

	changed := false
	if u.User != nil {
		u.User = nil
		changed = true
	}
	if q := u.Query(); len(q) > 0 {
		queryChanged := false
		for key := range q {
			if isAlwaysSecretConfigKey(strings.ToLower(strings.TrimSpace(key))) {
				q.Set(key, "<redacted>")
				queryChanged = true
			}
		}
		if queryChanged {
			u.RawQuery = q.Encode()
			changed = true
		}
	}

	if !changed {
		return uri
	}

	redacted := u.String()
	if !hasScheme {
		redacted = strings.TrimPrefix(redacted, "redis://")
	}
	return redacted
}

// redactedURI lazily redacts a redis address for logging: the (parsing,
// allocating) RedactURI call is deferred until the log entry is actually
// emitted - i.e. only when debug logging is enabled - keeping it off the hot
// path in production.
type redactedURI string

func (r redactedURI) String() string {
	return RedactURI(string(r))
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

	// log the redacted form so credentials embedded in the address are not written to the logs
	log.Debugf("looking up in pwd map, uri: %s", redactedURI(uri))
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

	log.Debugf("Trying DialURL(): %s", redactedURI(uri))
	c, err := redis.DialURL(uri, options...)
	if err != nil {
		log.Debugf("DialURL() failed, err: %s", err)
		// The Dial() fallback passes the address straight to net.Dial, which
		// cannot parse credentials (a user:pass@host userinfo block or a
		// ?password= query parameter). When the address embeds any credential
		// the fallback is guaranteed to fail with an error that echoes the
		// secret - an error later surfaced via the exporter_last_scrape_error
		// metric. Only attempt the fallback when the address is credential-free,
		// i.e. identical to its redacted form, in which case it is also safe to
		// log verbatim.
		if RedactURI(e.redisAddr) == e.redisAddr {
			if frags := strings.Split(e.redisAddr, "://"); len(frags) == 2 {
				log.Debugf("Trying: Dial(): %s %s", frags[0], frags[1])
				c, err = redis.Dial(frags[0], frags[1], options...)
			} else {
				log.Debugf("Trying: Dial(): tcp %s", e.redisAddr)
				c, err = redis.Dial("tcp", e.redisAddr, options...)
			}
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

func doRedisCmd(c redis.Conn, cmd string, args ...any) (any, error) {
	log.Debugf("c.Do() - running command: %s args: [%v]", cmd, args)
	res, err := c.Do(cmd, args...)
	if err != nil {
		log.Debugf("c.Do() - err: %s", err)
	}
	log.Debugf("c.Do() - done")
	return res, err
}
