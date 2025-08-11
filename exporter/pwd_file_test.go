package exporter

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestLoadPwdFile(t *testing.T) {
	for _, tst := range []struct {
		name    string
		pwdFile string
		ok      bool
	}{
		{
			name:    "load-password-file-success",
			pwdFile: "../contrib/sample-pwd-file.json",
			ok:      true,
		},
		{
			name:    "load-password-file-missing",
			pwdFile: "non-existent.json",
			ok:      false,
		},
		{
			name:    "load-password-file-malformed",
			pwdFile: "../contrib/sample-pwd-file.json-malformed",
			ok:      false,
		},
	} {
		t.Run(tst.name, func(t *testing.T) {
			_, err := LoadPwdFile(tst.pwdFile)
			if err == nil && !tst.ok {
				t.Fatalf("Test Failed, result is not what we want")
			}
			if err != nil && tst.ok {
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
		t.Fatalf("Password map is empty - failing")
	}

	for _, tst := range []struct {
		name string
		addr string
		want string
	}{
		{name: "password-hit", addr: "redis://localhost:16380", want: "redis-password"},
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
	if os.Getenv("TEST_PWD_REDIS_URI") == "" {
		t.Skipf("Skipping TestHTTPScrapeWithPasswordFile, missing env variables")
	}

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
		{name: "scrape-pwd-file", addr: os.Getenv("TEST_PWD_REDIS_URI"), wants: []string{
			"uptime_in_seconds",
			"test_up 1",
		}},
		{name: "scrape-pwd-file-wrong-password", addr: "redis://localhost:16380", useWrongPassword: true, wants: []string{
			"test_up 0",
		}},
	} {
		if tst.useWrongPassword {
			passwordMap[tst.addr] = "wrong-password"
		}
		options := Options{
			Namespace:   "test",
			PasswordMap: passwordMap,
			LuaScript: map[string][]byte{
				"test.lua": []byte(`return {"a", "11", "b", "12", "c", "13"}`),
			},
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

func TestHTTPScrapeWithUsername(t *testing.T) {
	if os.Getenv("TEST_USER_PWD_REDIS_URI") == "" {
		t.Skipf("Skipping TestHTTPScrapeWithPasswordFile, missing env variables")
	}

	pwdFile := "../contrib/sample-pwd-file.json"
	passwordMap, err := LoadPwdFile(pwdFile)
	if err != nil {
		t.Fatalf("Test Failed, error: %v", err)
	}

	if len(passwordMap) == 0 {
		t.Fatalf("Password map is empty!")
	}

	// use provided uri but remove password before sending it over the wire
	// after all, we want to test the lookup in the password map
	u, err := url.Parse(os.Getenv("TEST_USER_PWD_REDIS_URI"))
	u.User = url.User(u.User.Username())
	uriWithUser := u.String()
	uriWithUser = strings.Replace(uriWithUser, fmt.Sprintf(":@%s", u.Host), fmt.Sprintf("@%s", u.Host), 1)

	for _, tst := range []struct {
		name           string
		addr           string
		wants          []string
		wantStatusCode int
	}{
		{
			name:           "scrape-pwd-file",
			wantStatusCode: http.StatusOK,
			addr:           uriWithUser, wants: []string{
				"uptime_in_seconds",
				"test_up 1",
			}},
	} {
		options := Options{
			Namespace:   "test",
			PasswordMap: passwordMap,
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

			gotStatusCode, body := downloadURLWithStatusCode(t, u)

			if gotStatusCode != tst.wantStatusCode {
				t.Fatalf("got status code: %d   wanted: %d", gotStatusCode, tst.wantStatusCode)
				return
			}

			// we can stop here if we expected a non-200 response
			if tst.wantStatusCode != http.StatusOK {
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
