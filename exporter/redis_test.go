package exporter

import (
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestHostVariations(t *testing.T) {
	host := strings.ReplaceAll(os.Getenv("TEST_REDIS_URI"), "redis://", "")

	for _, prefix := range []string{"", "redis://", "tcp://", ""} {
		e, _ := NewRedisExporter(prefix+host, Options{SkipTLSVerification: true})
		c, err := e.connectToRedis()
		if err != nil {
			t.Errorf("connectToRedis() err: %s", err)
			continue
		}

		if _, err := c.Do("PING", ""); err != nil {
			t.Errorf("PING err: %s", err)
		}

		c.Close()
	}
}

func TestPasswordProtectedInstance(t *testing.T) {
	userAddr := os.Getenv("TEST_USER_PWD_REDIS_URI")

	parsedPassword := ""
	parsed, err := url.Parse(userAddr)
	if err == nil && parsed.User != nil {
		parsedPassword, _ = parsed.User.Password()
	}

	tsts := []struct {
		name string
		addr string
		user string
		pwd  string
	}{
		{
			name: "TEST_PWD_REDIS_URI",
			addr: os.Getenv("TEST_PWD_REDIS_URI"),
		},
		{
			name: "TEST_USER_PWD_REDIS_URI",
			addr: userAddr,
		},
		{
			name: "parsed-TEST_USER_PWD_REDIS_URI",
			addr: parsed.Host,
			user: parsed.User.Username(),
			pwd:  parsedPassword,
		},
	}

	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {
			e, _ := NewRedisExporter(
				tst.addr,
				Options{
					Namespace: "test",
					Registry:  prometheus.NewRegistry(),
					User:      tst.user,
					Password:  tst.pwd,
				})
			ts := httptest.NewServer(e)
			defer ts.Close()

			chM := make(chan prometheus.Metric, 10000)
			go func() {
				e.Collect(chM)
				close(chM)
			}()

			body := downloadURL(t, ts.URL+"/metrics")
			if !strings.Contains(body, "test_up 1") {
				t.Errorf(`%s - response to /metric doesn't contain "test_up 1"`, tst)
			}
		})
	}
}

func TestPasswordInvalid(t *testing.T) {
	if os.Getenv("TEST_PWD_REDIS_URI") == "" {
		t.Skipf("TEST_PWD_REDIS_URI not set - skipping")
	}

	testPwd := "redis-password"
	uri := strings.Replace(os.Getenv("TEST_PWD_REDIS_URI"), testPwd, "wrong-pwd", -1)

	e, _ := NewRedisExporter(uri, Options{Namespace: "test", Registry: prometheus.NewRegistry()})
	ts := httptest.NewServer(e)
	defer ts.Close()

	chM := make(chan prometheus.Metric, 10000)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	want := `test_exporter_last_scrape_error{err="dial redis: unknown network redis"} 1`
	body := downloadURL(t, ts.URL+"/metrics")
	if !strings.Contains(body, want) {
		t.Errorf(`error, expected string "%s" in body, got body: \n\n%s`, want, body)
	}
}
