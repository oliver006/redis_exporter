package exporter

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func TestHTTPScrapeMetricsEndpoints(t *testing.T) {
	setupDBKeys(t, os.Getenv("TEST_REDIS_URI"))
	defer deleteKeysFromDB(t, os.Getenv("TEST_REDIS_URI"))
	setupDBKeys(t, os.Getenv("TEST_PWD_REDIS_URI"))
	defer deleteKeysFromDB(t, os.Getenv("TEST_PWD_REDIS_URI"))

	csk := dbNumStrFull + "=" + url.QueryEscape(keys[0]) // check-single-keys
	css := dbNumStrFull + "=" + TestStreamName           // check-single-streams
	cntk := dbNumStrFull + "=" + keys[0] + "*"           // count-keys

	testRedisIPAddress := ""
	testRedisHostname := ""
	if u, err := url.Parse(os.Getenv("TEST_REDIS_URI")); err == nil {
		testRedisHostname = u.Hostname()
		ips, err := net.LookupIP(testRedisHostname)
		if err != nil {
			t.Fatalf("Could not get IP address: %s", err)
		}
		if len(ips) == 0 {
			t.Fatal("No IP addresses found")
		}
		testRedisIPAddress = ips[0].String()
		t.Logf("testRedisIPAddress: %s", testRedisIPAddress)
		t.Logf("testRedisHostname: %s", testRedisHostname)
	}

	for _, tst := range []struct {
		name     string
		addr     string
		ck       string
		csk      string
		cs       string
		scrapeCs string
		css      string
		cntk     string
		pwd      string
		scrape   bool
		target   string

		wantStatusCode int
	}{
		{name: "ip-addr", addr: testRedisIPAddress, csk: csk, css: css, cntk: cntk},
		{name: "hostname", addr: testRedisHostname, csk: csk, css: css, cntk: cntk},

		{name: "check-keys", addr: os.Getenv("TEST_REDIS_URI"), ck: csk, cs: css, cntk: cntk},
		{name: "check-single-keys", addr: os.Getenv("TEST_REDIS_URI"), csk: csk, css: css, cntk: cntk},

		{name: "addr-no-prefix", addr: strings.TrimPrefix(os.Getenv("TEST_REDIS_URI"), "redis://"), csk: csk, css: css, cntk: cntk},

		{name: "scrape-target-no-prefix", pwd: "", scrape: true, target: strings.TrimPrefix(os.Getenv("TEST_REDIS_URI"), "redis://"), ck: csk, cs: css, cntk: cntk},
		{name: "scrape-broken-target", wantStatusCode: http.StatusBadRequest, scrape: true, target: "://nope"},
		{name: "scrape-broken-target2", wantStatusCode: http.StatusBadRequest, scrape: true, target: os.Getenv("TEST_REDIS_URI") + "-", csk: csk, css: css, cntk: cntk},
		{name: "scrape-broken-cs", wantStatusCode: http.StatusBadRequest, scrape: true, target: os.Getenv("TEST_REDIS_URI"), scrapeCs: "1=2=3=4"},

		{name: "scrape-ck", pwd: "", scrape: true, target: os.Getenv("TEST_REDIS_URI"), ck: csk, scrapeCs: css, cntk: cntk},
		{name: "scrape-csk", pwd: "", scrape: true, target: os.Getenv("TEST_REDIS_URI"), csk: csk, css: css, cntk: cntk},

		{name: "scrape-pwd-ck", pwd: "redis-password", scrape: true, target: os.Getenv("TEST_PWD_REDIS_URI"), ck: csk, scrapeCs: css, cntk: cntk},
		{name: "scrape-pwd-csk", pwd: "redis-password", scrape: true, target: os.Getenv("TEST_PWD_REDIS_URI"), csk: csk, scrapeCs: css, cntk: cntk},

		{name: "error-scrape-no-target", wantStatusCode: http.StatusBadRequest, scrape: true, target: ""},
	} {
		t.Run(tst.name, func(t *testing.T) {
			options := Options{
				Namespace: "test",
				Password:  tst.pwd,
				LuaScript: []byte(`return {"a", "11", "b", "12", "c", "13"}`),
				Registry:  prometheus.NewRegistry(),
			}

			options.CheckSingleKeys = tst.csk
			options.CheckKeys = tst.ck
			options.CheckSingleStreams = tst.css
			options.CheckStreams = tst.cs
			options.CountKeys = tst.cntk
			options.CheckKeysBatchSize = 1000

			e, _ := NewRedisExporter(tst.addr, options)
			ts := httptest.NewServer(e)

			u := ts.URL
			if tst.scrape {
				u += "/scrape"
				v := url.Values{}
				v.Add("target", tst.target)
				v.Add("check-single-keys", tst.csk)
				v.Add("check-keys", tst.ck)
				v.Add("check-streams", tst.scrapeCs)
				v.Add("check-single-streams", tst.css)
				v.Add("count-keys", tst.cntk)

				up, _ := url.Parse(u)
				up.RawQuery = v.Encode()
				u = up.String()
			} else {
				u += "/metrics"
			}

			wantStatusCode := http.StatusOK
			if tst.wantStatusCode != 0 {
				wantStatusCode = tst.wantStatusCode
			}

			gotStatusCode, body := downloadURLWithStatusCode(t, u)

			if gotStatusCode != wantStatusCode {
				t.Fatalf("got status code: %d   wanted: %d", gotStatusCode, wantStatusCode)
				return
			}

			// we can stop here if we expected a non-200 response
			if wantStatusCode != http.StatusOK {
				return
			}

			wants := []string{
				// metrics
				`test_connected_clients`,
				`test_commands_processed_total`,
				`test_instance_info`,

				"db_keys",
				"db_avg_ttl_seconds",
				"cpu_sys_seconds_total",
				"loading_dump_file", // testing renames
				"config_maxmemory",  // testing config extraction
				"config_maxclients", // testing config extraction
				"slowlog_length",
				"slowlog_last_id",
				"start_time_seconds",
				"uptime_in_seconds",

				// labels and label values
				`redis_mode`,
				`standalone`,
				`cmd="config`,
				"maxmemory_policy",

				`test_script_value`, // lua script

				`test_key_size{db="db11",key="` + keys[0] + `"} 7`,
				`test_key_value{db="db11",key="` + keys[0] + `"} 1234.56`,

				`test_keys_count{db="db11",key="` + keys[0] + `*"} 1`,

				`test_db_keys{db="db11"} `,
				`test_db_keys_expiring{db="db11"} `,
				// streams
				`stream_length`,
				`stream_groups`,
				`stream_radix_tree_keys`,
				`stream_radix_tree_nodes`,
				`stream_group_consumers`,
				`stream_group_messages_pending`,
				`stream_group_consumer_messages_pending`,
				`stream_group_consumer_idle_seconds`,
				`test_up 1`,
			}

			for _, want := range wants {
				if !strings.Contains(body, want) {
					t.Errorf("url: %s    want metrics to include %q, have:\n%s", u, want, body)
					break
				}
			}
			ts.Close()
		})
	}
}

func TestSimultaneousMetricsHttpRequests(t *testing.T) {
	setupDBKeys(t, os.Getenv("TEST_REDIS_URI"))
	defer deleteKeysFromDB(t, os.Getenv("TEST_REDIS_URI"))

	e, _ := NewRedisExporter("", Options{Namespace: "test", InclSystemMetrics: false, Registry: prometheus.NewRegistry()})
	ts := httptest.NewServer(e)
	defer ts.Close()

	uris := []string{
		os.Getenv("TEST_REDIS_URI"),
		os.Getenv("TEST_REDIS_2_8_URI"),

		os.Getenv("TEST_KEYDB01_URI"),
		os.Getenv("TEST_KEYDB02_URI"),

		os.Getenv("TEST_REDIS5_URI"),
		os.Getenv("TEST_REDIS6_URI"),

		// tile38 & Cluster need to be last in this list so we can identify them when selected, down in line 229
		os.Getenv("TEST_REDIS_CLUSTER_MASTER_URI"),
		os.Getenv("TEST_REDIS_CLUSTER_SLAVE_URI"),
		os.Getenv("TEST_TILE38_URI"),
	}

	t.Logf("uris: %#v", uris)

	goroutines := 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for ; goroutines > 0; goroutines-- {
		go func() {
			requests := 100
			for ; requests > 0; requests-- {
				v := url.Values{}

				uriIdx := rand.Intn(len(uris))
				target := uris[uriIdx]
				v.Add("target", target)

				// not appending this param for Tile38 and cluster (the last two in the list)
				// Tile38 & cluster don't support the SELECT command so this test will fail and spam the logs
				if uriIdx < len(uris)-3 {
					v.Add("check-single-keys", dbNumStrFull+"="+url.QueryEscape(keys[0]))
				}
				up, _ := url.Parse(ts.URL + "/scrape")
				up.RawQuery = v.Encode()
				fullURL := up.String()

				body := downloadURL(t, fullURL)
				wants := []string{
					`test_connected_clients`,
					`test_commands_processed_total`,
					`test_instance_info`,
					`test_up 1`,
				}
				for _, want := range wants {
					if !strings.Contains(body, want) {
						t.Errorf("fullURL: %s    - want metrics to include %q, have:\n%s", fullURL, want, body)
						break
					}
				}
			}
			wg.Done()
		}()
	}
	wg.Wait()
}

func TestHttpHandlers(t *testing.T) {
	if os.Getenv("TEST_PWD_REDIS_URI") == "" {
		t.Skipf("TEST_PWD_REDIS_URI not set - skipping")
	}

	e, _ := NewRedisExporter(os.Getenv("TEST_PWD_REDIS_URI"), Options{Namespace: "test", Registry: prometheus.NewRegistry()})
	ts := httptest.NewServer(e)
	defer ts.Close()

	for _, tst := range []struct {
		path string
		want string
	}{
		{
			path: "/",
			want: `<head><title>Redis Exporter `,
		},
		{
			path: "/health",
			want: `ok`,
		},
	} {
		t.Run(fmt.Sprintf("path: %s", tst.path), func(t *testing.T) {
			body := downloadURL(t, ts.URL+tst.path)
			if !strings.Contains(body, tst.want) {
				t.Fatalf(`error, expected string "%s" in body, got body: \n\n%s`, tst.want, body)
			}
		})
	}
}

func downloadURL(t *testing.T, u string) string {
	_, res := downloadURLWithStatusCode(t, u)
	return res
}

func downloadURLWithStatusCode(t *testing.T, u string) (int, string) {
	log.Debugf("downloadURL() %s", u)

	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	return resp.StatusCode, string(body)
}
