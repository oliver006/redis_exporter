package exporter

import (
	"log"
	"os"
	"testing"
)

func TestLoadPwdFile(t *testing.T) {
	if os.Getenv("TEST_REDIS_PWD_FILE") == "" {
		t.Fatalf("TEST_REDIS_PWD_FILE not set!")
	}
	passwordMap, err := LoadPwdFile(os.Getenv("TEST_REDIS_PWD_FILE"))
	if err != nil {
		t.Fatalf("Test Failed, error: %v", err)
	}

	if len(passwordMap) == 0 {
		t.Skipf("Password map is empty -skipping")
	}

	for host, password := range passwordMap {
		if password != "" {
			log.Printf("%s has a password", host)
		} else {
			log.Printf("%s password is empty", host)
		}
	}
}
