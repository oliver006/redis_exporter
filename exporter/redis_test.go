package exporter

import (
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
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

func TestValkeyScheme(t *testing.T) {
	host := os.Getenv("TEST_VALKEY8_URI")

	e, _ := NewRedisExporter(host, Options{SkipTLSVerification: true})
	c, err := e.connectToRedis()
	if err != nil {
		t.Fatalf("connectToRedis() err: %s", err)
	}

	if _, err := c.Do("PING", ""); err != nil {
		t.Errorf("PING err: %s", err)
	}

	c.Close()
}

func TestPasswordProtectedInstance(t *testing.T) {
	userAddr := os.Getenv("TEST_USER_PWD_REDIS_URI")
	if userAddr == "" {
		t.Skipf("Skipping TestHTTPScrapeWithPasswordFile, missing env variables")
	}

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
					User:      tst.user,
					Password:  tst.pwd,
				})
			ts := httptest.NewServer(e)
			defer ts.Close()

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

	e, _ := NewRedisExporter(uri, Options{Namespace: "test"})
	ts := httptest.NewServer(e)
	defer ts.Close()

	want := `test_exporter_last_scrape_error{err="dial redis: unknown network redis"} 1`
	body := downloadURL(t, ts.URL+"/metrics")
	if !strings.Contains(body, want) {
		t.Errorf(`error, expected string "%s" in body, got body: \n\n%s`, want, body)
	}
}

func TestConnectToClusterUsingPasswordFile(t *testing.T) {
	clusterUri := os.Getenv("TEST_VALKEY_CLUSTER_PASSWORD_URI")
	if clusterUri == "" {
		t.Skipf("TEST_VALKEY_CLUSTER_PASSWORD_URI is not set")
	}
	passMap := map[string]string{clusterUri: "redis-password"}
	wrongPassMap := map[string]string{"redis://redis-cluster-password-wrong:7006": "redis-password"}

	tsts := []struct {
		name         string
		isCluster    bool
		passMap      map[string]string
		refreshError bool
	}{
		{name: "ConnectToCluster using password file with cluster mode", isCluster: true, passMap: passMap, refreshError: false},
		{name: "ConnectToCluster using password file without cluster mode", isCluster: false, passMap: passMap, refreshError: false},
		{name: "ConnectToCluster using password file with cluster mode failed", isCluster: false, passMap: wrongPassMap, refreshError: true},
	}
	for _, tst := range tsts {
		t.Run(tst.name, func(t *testing.T) {
			e, _ := NewRedisExporter(clusterUri, Options{
				SkipTLSVerification: true,
				PasswordMap:         tst.passMap,
				IsCluster:           tst.isCluster,
			})
			_, err := e.connectToRedisCluster()
			t.Logf("connectToRedisCluster() err: %s", err)
			if err != nil && strings.Contains(err.Error(), "Cluster refresh failed:") && !tst.refreshError {
				t.Fatalf("Test Cluster connection Failed error")
			}
			if !tst.refreshError && err != nil {
				t.Fatalf("Test Cluster connection Failed, err: %s", err)
			}
		})
	}
}

func TestSchemeFromURI(t *testing.T) {
	for _, tst := range []struct {
		uri  string
		want string
	}{
		{"redis://localhost:6379", "redis"},
		{"rediss://localhost:6379", "rediss"},
		{"valkey://localhost:6379", "valkey"},
		{"valkeys://localhost:6379", "valkeys"},
		{"localhost:6379", "redis"},        // no scheme -> default
		{"http://localhost:6379", "redis"}, // unrecognised -> default
	} {
		if got := schemeFromURI(tst.uri); got != tst.want {
			t.Errorf("schemeFromURI(%q) = %q, want %q", tst.uri, got, tst.want)
		}
	}
}

func TestSchemeIsTLS(t *testing.T) {
	for _, tst := range []struct {
		uri  string
		want bool
	}{
		{"redis://localhost:6379", false},
		{"rediss://localhost:6379", true},
		{"valkey://localhost:6379", false},
		{"valkeys://localhost:6379", true},
		{"localhost:6379", false},
	} {
		if got := schemeIsTLS(tst.uri); got != tst.want {
			t.Errorf("schemeIsTLS(%q) = %v, want %v", tst.uri, got, tst.want)
		}
	}
}

func TestStartupNodeFromURI(t *testing.T) {
	for _, tst := range []struct {
		name    string
		uri     string
		want    string
		wantErr bool
	}{
		{name: "with port", uri: "redis://localhost:7000", want: "localhost:7000"},
		{name: "default port", uri: "redis://localhost", want: "localhost:6379"},
		{name: "tls scheme keeps host:port", uri: "rediss://10.0.0.5:6380", want: "10.0.0.5:6380"},
		{name: "parse error", uri: "redis://%zz", wantErr: true},
	} {
		t.Run(tst.name, func(t *testing.T) {
			got, err := startupNodeFromURI(tst.uri)
			if tst.wantErr {
				if err == nil {
					t.Errorf("startupNodeFromURI(%q) expected error, got %q", tst.uri, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("startupNodeFromURI(%q) err: %s", tst.uri, err)
			}
			if got != tst.want {
				t.Errorf("startupNodeFromURI(%q) = %q, want %q", tst.uri, got, tst.want)
			}
		})
	}
}
