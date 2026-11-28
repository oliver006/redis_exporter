package exporter

import (
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
)

func connectionTestExporter(t *testing.T, uri string) *Exporter {
	t.Helper()
	e, err := NewRedisExporter(uri, Options{Namespace: "test", ConnectionTimeouts: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func requireConnectionTestPing(t *testing.T, e *Exporter) {
	t.Helper()
	c, err := e.connectToRedis()
	if err != nil {
		t.Fatalf("connectToRedis(): %v", err)
	}
	defer c.Close()
	if pong, err := redis.String(c.Do("PING")); err != nil || pong != "PONG" {
		t.Fatalf("PING = %q, %v; want PONG", pong, err)
	}
}

func requireConnectionTestScrape(t *testing.T, e *Exporter, up, scrapeError string) {
	t.Helper()
	ts := httptest.NewServer(e)
	defer ts.Close()
	body := downloadURL(t, ts.URL+"/metrics")
	for _, want := range []string{"\ntest_up " + up + "\n", "\n" + scrapeError + "\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q:\n%s", want, body)
		}
	}
}

func TestConnectToRedisUnixSocket(t *testing.T) {
	uri := os.Getenv("TEST_REDIS_UNIX_URI")
	if uri == "" {
		t.Skip("TEST_REDIS_UNIX_URI not set")
	}
	e := connectionTestExporter(t, uri)
	requireConnectionTestPing(t, e)
	requireConnectionTestScrape(t, e, "1", `test_exporter_last_scrape_error{err=""} 0`)
}

func TestConnectToRedisURLCredentials(t *testing.T) {
	uri := os.Getenv("TEST_REDIS_CONNECTION_URI")
	if uri == "" {
		t.Skip("TEST_REDIS_CONNECTION_URI not set")
	}
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}

	// The fixture allows anonymous access, so discarding credentials would succeed.
	requireConnectionTestPing(t, connectionTestExporter(t, u.String()))
	u.User = url.UserPassword("exporter", "exporter-password")
	requireConnectionTestPing(t, connectionTestExporter(t, u.String()))
	u.User = url.UserPassword("exporter", "wrong-password")
	e := connectionTestExporter(t, u.String())
	c, err := e.connectToRedis()
	if c != nil {
		c.Close()
	}
	var redisErr redis.Error
	if !errors.As(err, &redisErr) || !strings.HasPrefix(string(redisErr), "WRONGPASS ") {
		t.Fatalf("connectToRedis() = %v; want Redis WRONGPASS error", err)
	}
	requireConnectionTestScrape(t, e, "0",
		`test_exporter_last_scrape_error{err="`+err.Error()+`"} 1`)
}

func TestConnectToRedisURLDatabase(t *testing.T) {
	uri := os.Getenv("TEST_REDIS_CONNECTION_URI")
	if uri == "" {
		t.Skip("TEST_REDIS_CONNECTION_URI not set")
	}
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	// The fixture has only DB0. A retry that discards /1 would succeed on DB0.
	u.Path = "/0"
	requireConnectionTestPing(t, connectionTestExporter(t, u.String()))
	for _, tst := range []struct {
		name string
		path string
		want string
	}{
		{name: "unavailable", path: "/1", want: "ERR DB index is out of range"},
		{name: "invalid", path: "/not-a-db", want: "invalid database: not-a-db"},
	} {
		t.Run(tst.name, func(t *testing.T) {
			u.Path = tst.path
			e := connectionTestExporter(t, u.String())
			c, err := e.connectToRedis()
			if c != nil {
				c.Close()
			}
			if err == nil || err.Error() != tst.want {
				t.Fatalf("connectToRedis() = %v; want %q", err, tst.want)
			}
			requireConnectionTestScrape(t, e, "0",
				`test_exporter_last_scrape_error{err="`+tst.want+`"} 1`)
		})
	}
}

func TestConnectToRedisTLSBadPassword(t *testing.T) {
	uri := os.Getenv("TEST_VALKEY9_TLS_URI")
	if uri == "" {
		t.Skip("TEST_VALKEY9_TLS_URI not set")
	}
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}

	for _, tst := range []struct {
		name          string
		password      string
		urlPassword   bool
		wantWrongPass bool
	}{
		{name: "valid-url", password: "exporter-password", urlPassword: true},
		{name: "invalid-url", password: "wrong-password", urlPassword: true, wantWrongPass: true},
		{name: "valid-options", password: "exporter-password"},
		{name: "invalid-options", password: "wrong-password", wantWrongPass: true},
	} {
		t.Run(tst.name, func(t *testing.T) {
			target := *u
			opts := Options{
				Namespace:           "test",
				ConnectionTimeouts:  time.Second,
				SkipTLSVerification: true,
				ClientCertFile:      "../contrib/tls/redis.crt",
				ClientKeyFile:       "../contrib/tls/redis.key",
			}
			if tst.urlPassword {
				target.User = url.UserPassword("exporter", tst.password)
			} else {
				opts.User = "exporter"
				opts.Password = tst.password
			}
			e, err := NewRedisExporter(target.String(), opts)
			if err != nil {
				t.Fatal(err)
			}
			if !tst.wantWrongPass {
				requireConnectionTestPing(t, e)
				return
			}

			c, err := e.connectToRedis()
			if c != nil {
				c.Close()
			}
			var redisErr redis.Error
			if !errors.As(err, &redisErr) || !strings.HasPrefix(string(redisErr), "WRONGPASS ") {
				t.Fatalf("connectToRedis() = %v; want Redis WRONGPASS error", err)
			}
			requireConnectionTestScrape(t, e, "0",
				`test_exporter_last_scrape_error{err="`+err.Error()+`"} 1`)
		})
	}
}

// The address can contain the Redis password. When it can't be parsed, the
// connection error ends up in the err label of exporter_last_scrape_error, so
// it must not quote the address or any part of it. No Redis server is needed.
func TestInvalidAddressErrorDoesNotLeakPassword(t *testing.T) {
	for _, tst := range []struct {
		name string
		addr string
		want string
	}{
		{name: "invalid escape", addr: "redis://:s3cret%1@localhost:6379", want: "invalid redis address: invalid URL escape"},
		{name: "invalid escape followed by password", addr: "redis://:%zzs3cret@localhost:6379", want: "invalid redis address: invalid URL escape"},
		{name: "invalid port", addr: "redis://:s3cret@localhost:abc", want: "invalid redis address: invalid port"},
		{name: "missing host", addr: "redis://user:s3cret", want: "invalid redis address: invalid port"},
		{name: "unescaped slash in password", addr: "redis://:s3cret/pw@localhost:6379", want: "invalid redis address: invalid port"},
		{name: "invalid host character", addr: "redis://:s3cret@local host:6379", want: "invalid redis address: invalid character in host name"},
		{name: "invalid userinfo", addr: "redis://:s3cret pw@localhost:6379", want: "invalid redis address: malformed URL"},
		{name: "invalid IP literal", addr: "redis://[s3cret]:6379", want: "invalid redis address: malformed URL"},
	} {
		t.Run(tst.name, func(t *testing.T) {
			e := connectionTestExporter(t, tst.addr)

			c, err := e.connectToRedis()
			if c != nil {
				c.Close()
			}
			if err == nil || err.Error() != tst.want {
				t.Fatalf("connectToRedis() = %v; want %q", err, tst.want)
			}

			c, err = e.connectToRedisClusterDatabase(3)
			if c != nil {
				c.Close()
				t.Fatal("connectToRedisClusterDatabase returned a connection for an invalid address")
			}
			if err == nil || err.Error() != tst.want {
				t.Fatalf("connectToRedisClusterDatabase(3) = %v; want %q", err, tst.want)
			}

			ts := httptest.NewServer(e)
			defer ts.Close()
			body := downloadURL(t, ts.URL+"/metrics")
			if strings.Contains(body, "s3cret") {
				t.Errorf("metrics contain the password:\n%s", body)
			}
			for _, want := range []string{"\ntest_up 0\n", "\ntest_exporter_last_scrape_error{err=\"" + tst.want + "\"} 1\n"} {
				if !strings.Contains(body, want) {
					t.Errorf("metrics missing %q:\n%s", want, body)
				}
			}
		})
	}
}

func TestRedactURLErrorKeepsOtherErrors(t *testing.T) {
	if err := redactURLError(nil); err != nil {
		t.Errorf("redactURLError(nil) = %v; want nil", err)
	}
	other := errors.New("dial tcp 127.0.0.1:6379: connect: connection refused")
	if err := redactURLError(other); err != other {
		t.Errorf("redactURLError(%v) = %v; want the error unchanged", other, err)
	}
}
