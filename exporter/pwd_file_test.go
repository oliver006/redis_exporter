package exporter

import (
	"strings"
	"testing"
)

func TestLoadPwdFile(t *testing.T) {
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
