package exporter

import (
	"fmt"
	"testing"
)

func TestLoadPwdFile(t *testing.T) {
	confFile := "../sample-pwd-file.json"
	p := NewPasswordMap()
	err := p.LoadPwdFile(confFile)
	if err != nil {
		t.Fatalf("Test Failed, error: %v", err)
	}

	fmt.Println(len(p.RedisPwd))
	fmt.Println("192.168.1.1 pwd is:", p.RedisPwd["192.168.1.1"])
	fmt.Println("192.168.1.11 pwd is:", p.RedisPwd["192.168.1.11"])
}
