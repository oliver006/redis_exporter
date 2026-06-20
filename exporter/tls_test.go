package exporter

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestCreateClientTLSConfig(t *testing.T) {
	for _, test := range []struct {
		name          string
		options       Options
		expectSuccess bool
		serverName    string
	}{
		// positive tests
		{"no_options", Options{}, true, ""},
		{"skip_verificaton", Options{
			SkipTLSVerification: true}, true, ""},
		{"server_name", Options{
			TLSServerName: "redis.example.test"}, true, "redis.example.test"},
		{"load_client_keypair", Options{
			ClientCertFile: "../contrib/tls/redis.crt",
			ClientKeyFile:  "../contrib/tls/redis.key"}, true, ""},
		{"load_ca_cert", Options{
			CaCertFile: "../contrib/tls/ca.crt"}, true, ""},
		{"load_system_certs", Options{}, true, ""},

		// negative tests
		{"nonexisting_client_files", Options{
			ClientCertFile: "/nonexisting/file",
			ClientKeyFile:  "/nonexisting/file"}, false, ""},
		{"nonexisting_ca_file", Options{
			CaCertFile: "/nonexisting/file"}, false, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			e, err := NewRedisExporter("", test.options)
			if err != nil {
				t.Fatalf("NewRedisExporter() err: %s", err)
			}

			tlsConfig, err := e.CreateClientTLSConfig()
			if test.expectSuccess && err != nil {
				t.Errorf("Expected success for test: %s, got err: %s", test.name, err)
				return
			}
			if !test.expectSuccess && err == nil {
				t.Errorf("Expected failure for test: %s", test.name)
				return
			}
			if test.serverName != "" && tlsConfig.ServerName != test.serverName {
				t.Errorf("CreateClientTLSConfig() ServerName = %q, want %q", tlsConfig.ServerName, test.serverName)
			}
		})
	}
}

func TestTLSServerNameUsedForMetricsScrape(t *testing.T) {
	addr, sniCh := startSNICaptureRedisTLSServer(t)
	want := "redis.example.test"

	e, err := NewRedisExporter("rediss://"+addr, Options{
		Namespace:                      "test",
		SkipTLSVerification:            true,
		TLSServerName:                  want,
		SetClientName:                  false,
		ConfigCommandName:              "-",
		ExcludeLatencyHistogramMetrics: true,
	})
	if err != nil {
		t.Fatalf("NewRedisExporter() err: %s", err)
	}

	ts := httptest.NewServer(e)
	defer ts.Close()

	statusCode, _ := downloadURLWithStatusCode(t, ts.URL+"/metrics")
	if statusCode != http.StatusOK {
		t.Fatalf("got status code %d, want %d", statusCode, http.StatusOK)
	}

	if got := waitForCapturedSNI(t, sniCh); got != want {
		t.Fatalf("TLS ServerName = %q, want %q", got, want)
	}
}

func TestTLSServerNameScrapeEndpointOverride(t *testing.T) {
	for _, test := range []struct {
		name  string
		param string
	}{
		{name: "prometheus-friendly-query-param", param: "tls_server_name"},
		{name: "flag-style-query-param", param: "tls-server-name"},
	} {
		t.Run(test.name, func(t *testing.T) {
			addr, sniCh := startSNICaptureRedisTLSServer(t)
			want := "dynamic.redis.example.test"

			e, err := NewRedisExporter("", Options{
				Namespace:                      "test",
				SkipTLSVerification:            true,
				TLSServerName:                  "default.redis.example.test",
				SetClientName:                  false,
				ConfigCommandName:              "-",
				ExcludeLatencyHistogramMetrics: true,
			})
			if err != nil {
				t.Fatalf("NewRedisExporter() err: %s", err)
			}

			ts := httptest.NewServer(e)
			defer ts.Close()

			v := url.Values{}
			v.Add("target", "rediss://"+addr)
			v.Add(test.param, want)
			u, err := url.Parse(ts.URL + "/scrape")
			if err != nil {
				t.Fatalf("url.Parse() err: %s", err)
			}
			u.RawQuery = v.Encode()

			statusCode, _ := downloadURLWithStatusCode(t, u.String())
			if statusCode != http.StatusOK {
				t.Fatalf("got status code %d, want %d", statusCode, http.StatusOK)
			}

			if got := waitForCapturedSNI(t, sniCh); got != want {
				t.Fatalf("TLS ServerName = %q, want %q", got, want)
			}
		})
	}
}

func startSNICaptureRedisTLSServer(t *testing.T) (string, <-chan string) {
	t.Helper()

	cert, err := tls.LoadX509KeyPair("../contrib/tls/redis.crt", "../contrib/tls/redis.key")
	if err != nil {
		t.Fatalf("tls.LoadX509KeyPair() err: %s", err)
	}

	sniCh := make(chan string, 1)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() err: %s", err)
	}

	tlsListener := tls.NewListener(listener, &tls.Config{
		Certificates: []tls.Certificate{cert},
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			select {
			case sniCh <- hello.ServerName:
			default:
			}
			return nil, nil
		},
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := tlsListener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				if tlsConn, ok := conn.(*tls.Conn); ok {
					_ = tlsConn.Handshake()
				}
			}(conn)
		}
	}()

	t.Cleanup(func() {
		_ = tlsListener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for TLS test server to stop")
		}
	})

	return tlsListener.Addr().String(), sniCh
}

func waitForCapturedSNI(t *testing.T, sniCh <-chan string) string {
	t.Helper()

	select {
	case sni := <-sniCh:
		return sni
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for TLS SNI")
		return ""
	}
}

func TestValkeyTLSScheme(t *testing.T) {
	for _, host := range []string{
		os.Getenv("TEST_REDIS7_TLS_URI"),
		os.Getenv("TEST_VALKEY8_TLS_URI"),
	} {
		t.Run(host, func(t *testing.T) {

			e, _ := NewRedisExporter(host,
				Options{
					SkipTLSVerification: true,
					ClientCertFile:      "../contrib/tls/redis.crt",
					ClientKeyFile:       "../contrib/tls/redis.key",
				},
			)
			c, err := e.connectToRedis()
			if err != nil {
				t.Fatalf("connectToRedis() err: %s", err)
			}

			if _, err := c.Do("PING", ""); err != nil {
				t.Errorf("PING err: %s", err)
			}

			c.Close()

			chM := make(chan prometheus.Metric)
			go func() {
				e.Collect(chM)
				close(chM)
			}()

			tsts := []struct {
				in    string
				found bool
			}{
				{in: "db_keys"},
				{in: "commands_total"},
				{in: "total_connections_received"},
				{in: "used_memory"},
			}
			for m := range chM {
				desc := m.Desc().String()
				for i := range tsts {
					if strings.Contains(desc, tsts[i].in) {
						tsts[i].found = true
					}
				}
			}

		})
	}
}

func TestCreateServerTLSConfig(t *testing.T) {
	e := getTestExporter()

	// positive tests
	_, err := e.CreateServerTLSConfig("../contrib/tls/redis.crt", "../contrib/tls/redis.key", "", "TLS1.1")
	if err != nil {
		t.Errorf("CreateServerTLSConfig() err: %s", err)
	}
	_, err = e.CreateServerTLSConfig("../contrib/tls/redis.crt", "../contrib/tls/redis.key", "../contrib/tls/ca.crt", "TLS1.0")
	if err != nil {
		t.Errorf("CreateServerTLSConfig() err: %s", err)
	}

	// negative tests
	_, err = e.CreateServerTLSConfig("/nonexisting/file", "/nonexisting/file", "", "TLS1.1")
	if err == nil {
		t.Errorf("Expected CreateServerTLSConfig() to fail")
	}
	_, err = e.CreateServerTLSConfig("/nonexisting/file", "/nonexisting/file", "/nonexisting/file", "TLS1.2")
	if err == nil {
		t.Errorf("Expected CreateServerTLSConfig() to fail")
	}
	_, err = e.CreateServerTLSConfig("../contrib/tls/redis.crt", "../contrib/tls/redis.key", "/nonexisting/file", "TLS1.3")
	if err == nil {
		t.Errorf("Expected CreateServerTLSConfig() to fail")
	}
	_, err = e.CreateServerTLSConfig("../contrib/tls/redis.crt", "../contrib/tls/redis.key", "../contrib/tls/ca.crt", "TLSX")
	if err == nil {
		t.Errorf("Expected CreateServerTLSConfig() to fail")
	}
}

func TestGetServerCertificateFunc(t *testing.T) {
	// positive test
	_, err := GetServerCertificateFunc("../contrib/tls/ca.crt", "../contrib/tls/ca.key")(nil)
	if err != nil {
		t.Errorf("GetServerCertificateFunc() err: %s", err)
	}

	// negative test
	_, err = GetServerCertificateFunc("/nonexisting/file", "/nonexisting/file")(nil)
	if err == nil {
		t.Errorf("Expected GetServerCertificateFunc() to fail")
	}
}

func TestGetConfigForClientFunc(t *testing.T) {
	// positive test
	_, err := GetConfigForClientFunc("../contrib/tls/redis.crt", "../contrib/tls/redis.key", "../contrib/tls/ca.crt")(nil)
	if err != nil {
		t.Errorf("GetConfigForClientFunc() err: %s", err)
	}

	// negative test
	_, err = GetConfigForClientFunc("/nonexisting/file", "/nonexisting/file", "/nonexisting/file")(nil)
	if err == nil {
		t.Errorf("Expected GetConfigForClientFunc() to fail")
	}
}
