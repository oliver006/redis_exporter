package exporter

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestLuaScript(t *testing.T) {
	for _, tst := range []struct {
		Name          string
		Script        string
		ExpectedKeys  int
		ExpectedError bool
		Wants         []string
	}{
		{
			Name:         "ok1",
			Script:       `return {"a", "11", "b", "12", "c", "13"}`,
			ExpectedKeys: 3,
			Wants:        []string{`test_exporter_last_scrape_error{err=""} 0`, `test_script_values{key="a"} 11`, `test_script_values{key="b"} 12`},
		},
		{
			Name:         "ok2",
			Script:       `return {"key1", "6389"}`,
			ExpectedKeys: 1,
			Wants:        []string{`test_exporter_last_scrape_error{err=""} 0`, `test_script_values{key="key1"} 6389`},
		},
		{
			Name:         "ok3",
			Script:       `return {} `,
			ExpectedKeys: 0,
		},
		{
			Name:          "borked1",
			Script:        `return {"key1"   BROKEN `,
			ExpectedKeys:  0,
			ExpectedError: true,
			Wants:         []string{`test_exporter_last_scrape_error{err="ERR Error compiling script`},
		},
		{
			Name:          "borked2",
			Script:        `return {"key1", "abc"}`,
			ExpectedKeys:  0,
			ExpectedError: true,
			Wants:         []string{`test_exporter_last_scrape_error{err="strconv.ParseFloat: parsing \"abc\": invalid syntax"} 1`},
		},
	} {
		t.Run(tst.Name, func(t *testing.T) {
			e, _ := NewRedisExporter(
				os.Getenv("TEST_REDIS_URI"),
				Options{
					Namespace: "test", Registry: prometheus.NewRegistry(),
					LuaScript: []byte(tst.Script),
				})
			ts := httptest.NewServer(e)
			defer ts.Close()

			chM := make(chan prometheus.Metric, 10000)
			go func() {
				e.Collect(chM)
				close(chM)
			}()

			body := downloadURL(t, ts.URL+"/metrics")

			for _, want := range tst.Wants {
				if !strings.Contains(body, want) {
					t.Errorf(`error, expected string "%s" in body, got body: \n\n%s`, want, body)
				}
			}
		})
	}
}
