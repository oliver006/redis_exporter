package exporter

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
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

func TestTLSServerNameAllowsVerifiedMetricsScrapeByIP(t *testing.T) {
	addr, caCertFile, sniCh := startTinyRedisTLSServer(t)
	want := "localhost"

	e, err := NewRedisExporter("rediss://"+addr, Options{
		Namespace:                      "test",
		CaCertFile:                     caCertFile,
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

	statusCode, body := downloadURLWithStatusCode(t, ts.URL+"/metrics")
	if statusCode != http.StatusOK {
		t.Fatalf("got status code %d, want %d", statusCode, http.StatusOK)
	}
	if !strings.Contains(body, `test_up 1`) {
		t.Fatalf("expected successful scrape, body:\n%s", body)
	}
	if !strings.Contains(body, `test_exporter_last_scrape_error{err=""} 0`) {
		t.Fatalf("expected empty last scrape error, body:\n%s", body)
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
			addr, caCertFile, sniCh := startTinyRedisTLSServer(t)
			want := "localhost"

			e, err := NewRedisExporter("", Options{
				Namespace:                      "test",
				CaCertFile:                     caCertFile,
				TLSServerName:                  "wrong.redis.example.test",
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

			statusCode, body := downloadURLWithStatusCode(t, u.String())
			if statusCode != http.StatusOK {
				t.Fatalf("got status code %d, want %d", statusCode, http.StatusOK)
			}
			if !strings.Contains(body, `test_up 1`) {
				t.Fatalf("expected successful scrape, body:\n%s", body)
			}
			if !strings.Contains(body, `test_exporter_last_scrape_error{err=""} 0`) {
				t.Fatalf("expected empty last scrape error, body:\n%s", body)
			}

			if got := waitForCapturedSNI(t, sniCh); got != want {
				t.Fatalf("TLS ServerName = %q, want %q", got, want)
			}
		})
	}
}

func TestVerifiedIPTargetFailsWithoutTLSServerName(t *testing.T) {
	addr, caCertFile, _ := startTinyRedisTLSServer(t)
	redisAddr := "rediss://" + addr

	e, err := NewRedisExporter(redisAddr, Options{
		CaCertFile:         caCertFile,
		ConnectionTimeouts: time.Second,
	})
	if err != nil {
		t.Fatalf("NewRedisExporter() err: %s", err)
	}

	options, err := e.configureOptions(redisAddr)
	if err != nil {
		t.Fatalf("configureOptions() err: %s", err)
	}

	c, err := redis.DialURL(redisAddr, options...)
	if err == nil {
		c.Close()
		t.Fatal("redis.DialURL() succeeded without tls server name for an IP target")
	}
	if !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("redis.DialURL() error = %q, want certificate verification error", err)
	}
}

func startTinyRedisTLSServer(t *testing.T) (string, string, <-chan string) {
	t.Helper()

	cert, caCertFile := newLocalhostTestCertificate(t)

	sniCh := make(chan string, 10)
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
					if err := tlsConn.Handshake(); err != nil {
						return
					}
				}
				serveTinyRedisConnection(conn)
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

	return tlsListener.Addr().String(), caCertFile, sniCh
}

func newLocalhostTestCertificate(t *testing.T) (tls.Certificate, string) {
	t.Helper()

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey(ca) err: %s", err)
	}
	caSerial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("rand.Int(ca serial) err: %s", err)
	}
	caTemplate := x509.Certificate{
		SerialNumber:          caSerial,
		Subject:               pkix.Name{Organization: []string{"redis_exporter"}, CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("x509.CreateCertificate(ca) err: %s", err)
	}

	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey(server) err: %s", err)
	}
	serverSerial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("rand.Int(server serial) err: %s", err)
	}
	serverTemplate := x509.Certificate{
		SerialNumber: serverSerial,
		Subject:      pkix.Name{Organization: []string{"redis_exporter"}, CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, &serverTemplate, &caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("x509.CreateCertificate(server) err: %s", err)
	}

	serverCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER})
	serverKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(serverKey)})
	cert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatalf("tls.X509KeyPair() err: %s", err)
	}

	caCertFile := filepath.Join(t.TempDir(), "ca.crt")
	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	if err := os.WriteFile(caCertFile, caCertPEM, 0600); err != nil {
		t.Fatalf("os.WriteFile(ca cert) err: %s", err)
	}

	return cert, caCertFile
}

const tinyRedisInfo = "# Server\r\n" +
	"redis_version:7.2.0\r\n" +
	"redis_build_id:test-build\r\n" +
	"redis_mode:standalone\r\n" +
	"os:Linux\r\n" +
	"tcp_port:6379\r\n" +
	"process_id:1\r\n" +
	"uptime_in_seconds:123\r\n" +
	"# Clients\r\n" +
	"connected_clients:1\r\n" +
	"# Memory\r\n" +
	"used_memory:100\r\n" +
	"maxmemory_policy:noeviction\r\n" +
	"# Stats\r\n" +
	"total_connections_received:1\r\n" +
	"total_commands_processed:1\r\n" +
	"# Replication\r\n" +
	"role:master\r\n" +
	"master_replid:test-replid\r\n" +
	"# Keyspace\r\n" +
	"db0:keys=1,expires=0,avg_ttl=0\r\n"

func serveTinyRedisConnection(conn net.Conn) {
	reader := bufio.NewReader(conn)
	for {
		args, err := readRESPArray(reader)
		if err != nil {
			return
		}
		if len(args) == 0 {
			_, _ = conn.Write([]byte("-ERR empty command\r\n"))
			continue
		}

		switch strings.ToUpper(args[0]) {
		case "INFO":
			_, _ = conn.Write([]byte("$" + strconv.Itoa(len(tinyRedisInfo)) + "\r\n" + tinyRedisInfo + "\r\n"))
		case "SLOWLOG":
			if len(args) > 1 && strings.EqualFold(args[1], "LEN") {
				_, _ = conn.Write([]byte(":0\r\n"))
			} else {
				_, _ = conn.Write([]byte("*0\r\n"))
			}
		default:
			_, _ = conn.Write([]byte("-ERR unknown command\r\n"))
		}
	}
}

func readRESPArray(reader *bufio.Reader) ([]string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "*") {
		return nil, io.ErrUnexpectedEOF
	}

	count, err := strconv.Atoi(strings.TrimPrefix(line, "*"))
	if err != nil {
		return nil, err
	}

	args := make([]string, 0, count)
	for range count {
		line, err = reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "$") {
			return nil, io.ErrUnexpectedEOF
		}

		argLen, err := strconv.Atoi(strings.TrimPrefix(line, "$"))
		if err != nil {
			return nil, err
		}

		buf := make([]byte, argLen+2)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:argLen]))
	}

	return args, nil
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
			if host == "" {
				t.Skip("missing TLS Redis test URI")
			}

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
	e, err := NewRedisExporter("", Options{Namespace: "test"})
	if err != nil {
		t.Fatalf("NewRedisExporter() err: %s", err)
	}

	// positive tests
	_, err = e.CreateServerTLSConfig("../contrib/tls/redis.crt", "../contrib/tls/redis.key", "", "TLS1.1")
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
