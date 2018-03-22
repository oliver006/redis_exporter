package exporter

import (
	"log"
	"testing"
)

func cmpStringArrays(a1, a2 []string) bool {
	if len(a1) != len(a2) {
		return false
	}
	for n := range a1 {
		if a1[n] != a2[n] {
			return false
		}
	}
	return true
}

func TestLoadRedisArgs(t *testing.T) {
	log.Println("TestLoadRedisArgs()")
	tests := []struct {
		addr, pwd, alias, sep           string
		wantAddr, wantPwds, wantAliases []string
	}{
		{
			addr:        "",
			sep:         ",",
			wantAddr:    []string{"redis://localhost:6379"},
			wantPwds:    []string{""},
			wantAliases: []string{""},
		},
		{
			addr:        "redis://localhost:6379",
			sep:         ",",
			wantAddr:    []string{"redis://localhost:6379"},
			wantPwds:    []string{""},
			wantAliases: []string{""},
		},
		{
			addr:        "redis://localhost:6379,redis://localhost:7000",
			sep:         ",",
			wantAddr:    []string{"redis://localhost:6379", "redis://localhost:7000"},
			wantPwds:    []string{"", ""},
			wantAliases: []string{"", ""},
		},
		{
			addr:        "redis://localhost:6379,redis://localhost:7000,redis://localhost:7001",
			sep:         ",",
			wantAddr:    []string{"redis://localhost:6379", "redis://localhost:7000", "redis://localhost:7001"},
			wantPwds:    []string{"", "", ""},
			wantAliases: []string{"", "", ""},
		},
		{
			alias:       "host-1",
			sep:         ",",
			wantAddr:    []string{"redis://localhost:6379"},
			wantPwds:    []string{""},
			wantAliases: []string{"host-1"},
		},
	}

	for _, test := range tests {
		sep := test.sep
		addrs, pwds, aliases := LoadRedisArgs(test.addr, test.pwd, test.alias, sep)
		if !cmpStringArrays(addrs, test.wantAddr) {
			t.Errorf("addrs not matching wantAliases, got: %v   want: %v", addrs, test.wantAddr)
		}
		if !cmpStringArrays(pwds, test.wantPwds) {
			t.Errorf("pwds not matching wantAliases, got: %v   want: %v", pwds, test.wantPwds)
		}
		if !cmpStringArrays(aliases, test.wantAliases) {
			t.Errorf("aliases not matching wantAliases, got: %v   want: %v", aliases, test.wantAliases)
		}
	}
}

func TestLoadRedisFile(t *testing.T) {
	if _, _, _, err := LoadRedisFile("doesnt-exist.txt"); err == nil {
		t.Errorf("should have failed opening non existing file")
		return
	}

	addrs, pwds, aliases, err := LoadRedisFile("../contrib/sample_redis_hosts_file.txt")
	if err != nil {
		t.Errorf("LoadRedisFile() failed, err: %s", err)
		return
	}
	log.Printf("aliases: %v \n", aliases)
	if !cmpStringArrays(addrs, []string{"redis://localhost:6379", "redis://localhost:7000", "redis://localhost:7000"}) {
		t.Errorf("addrs not matching want")
	}
	if !cmpStringArrays(pwds, []string{"", "password", "second-pwd"}) {
		t.Errorf("pwds not matching want")
	}
	if !cmpStringArrays(aliases, []string{"", "alias", ""}) {
		t.Errorf("aliases not matching want")
	}
}

func TestGetCloudFoundryRedisBindings(t *testing.T) {
	GetCloudFoundryRedisBindings()
}
