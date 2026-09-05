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
