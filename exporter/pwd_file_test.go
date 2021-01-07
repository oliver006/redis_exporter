package exporter

import (
	"os"
	"strings"
	"testing"
)

func TestLoadPwdFile(t *testing.T) {
	if os.Getenv("TEST_REDIS_URI") == "" {
		t.Fatalf("TEST_REDIS_URI not set!")
	}
	if os.Getenv("TEST_REDIS_PWD_FILE") == "" {
		t.Fatalf("TEST_REDIS_PWD_FILE not set!")
	}
	passwordMap, err := LoadPwdFile(os.Getenv("TEST_REDIS_PWD_FILE"))
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
		{name: "password-hit", addr: os.Getenv("TEST_REDIS_URI"), want: os.Getenv("TEST_PWD_REDIS_URI")},
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
