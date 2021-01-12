package exporter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestLoadPwdFile(t *testing.T) {
	for _, tst := range []struct {
		name    string
		pwdFile string
		want    error
	}{
		{name: "load-password-file-success", pwdFile: "../contrib/sample-pwd-file.json"},
		{name: "load-password-file-failed", pwdFile: "non-existent.json"},
	} {
		_, tst.want = os.Open(tst.pwdFile)
		t.Run(tst.name, func(t *testing.T) {
			_, err := LoadPwdFile(tst.pwdFile)
			if err == nil && err != tst.want {
				t.Fatalf("Test Failed, result is not what we want")
			}
			if err != nil && err.Error() != tst.want.Error() {
				t.Fatalf("Test Failed, result is not what we want")
			}
		})
	}
}

func TestPasswordMap(t *testing.T) {
	pwdFile := "../contrib/sample-pwd-file.json"
	passwordMap, err := LoadPwdFile(pwdFile)
	if err != nil {
		t.Fatalf("Test Failed, error: %v", err)
	}

	if len(passwordMap) == 0 {
		t.Fatalf("Password map is empty -skipping")
	}

	for _, tst := range []struct {
		name string
		addr string
		want string
	}{
		{name: "password-hit", addr: "redis://pwd-redis5:6380", want: "redis-password"},
		{name: "password-missed", addr: "Non-existent-redis-host", want: ""},
	} {
		t.Run(tst.name, func(t *testing.T) {
			pwd := passwordMap[tst.addr]
			if !strings.Contains(pwd, tst.want) {
				t.Errorf("redis host: %s    password is not what we want", tst.addr)
			}
		})
	}
}

func TestHTTPScrapeWithPasswordFile(t *testing.T) {
	pwdFile := "../contrib/sample-pwd-file.json"
	passwordMap, err := LoadPwdFile(pwdFile)
	if err != nil {
		t.Fatalf("Test Failed, error: %v", err)
	}

	if len(passwordMap) == 0 {
		t.Fatalf("Password map is empty!")
	}
	for _, tst := range []struct {
		name             string
		addr             string
		wants            []string
		useWrongPassword bool
		wantStatusCode   int
	}{
		{name: "scrape-pwd-file", addr: "redis://pwd-redis5:6380", wants: []string{
			"uptime_in_seconds",
			"test_up 1",
		}},
		{name: "scrape-pwd-file-wrong-password", addr: "redis://pwd-redis5:6380", useWrongPassword: true, wants: []string{
			"test_up 0",
		}},
	} {
		if tst.useWrongPassword {
			passwordMap[tst.addr] = "wrong-password"
		}
		options := Options{
			Namespace:   "test",
			PasswordMap: passwordMap,
			LuaScript:   []byte(`return {"a", "11", "b", "12", "c", "13"}`),
			Registry:    prometheus.NewRegistry(),
		}
		t.Run(tst.name, func(t *testing.T) {
			e, _ := NewRedisExporter(tst.addr, options)
			ts := httptest.NewServer(e)

			u := ts.URL
			u += "/scrape"
			v := url.Values{}
			v.Add("target", tst.addr)

			up, _ := url.Parse(u)
			up.RawQuery = v.Encode()
			u = up.String()

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

			for _, want := range tst.wants {
				if !strings.Contains(body, want) {
					t.Errorf("url: %s    want metrics to include %q, have:\n%s", u, want, body)
					break
				}
			}
			ts.Close()
		})

	}

}
