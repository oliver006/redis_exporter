package exporter

import (
	"fmt"
	"testing"
)

func TestLoadPwdFile(*testing.T) {
	confFile := "../pwd.json"
	err := LoadPwdFile(confFile)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(len(RedisPwd))
	fmt.Println("192.168.1.1 pwd is:", RedisPwd["192.168.1.1"])
	fmt.Println("192.168.1.11 pwd is:", RedisPwd["192.168.1.11"])
}
