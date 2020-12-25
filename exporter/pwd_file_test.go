package exporter

import (
	"log"
	"testing"
)

func TestLoadPwdFile(t *testing.T) {
	confFile := "../contrib/sample-pwd-file.json"
	passwordMap, err := LoadPwdFile(confFile)
	if err != nil {
		t.Fatalf("Test Failed, error: %v", err)
	}

	redisHostList := []string{
		"redis://192.168.1.1",
		// Make sure it does not exist in sample-pwd-file.json
		"redis://192.168.1.11",
	}

	for _, v := range redisHostList {
		if passwordMap[v] != "" {
			log.Printf("%s password found", v)
		} else {
			log.Printf("%s password Not found", v)
		}
	}
}
