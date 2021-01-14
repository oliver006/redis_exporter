package exporter

import (
	"encoding/json"
	"io/ioutil"

	log "github.com/sirupsen/logrus"
)

// LoadPwdFile reads the redis password file and returns the password map
func LoadPwdFile(passwordFile string) (map[string]string, error) {
	passwordMap := make(map[string]string)

	log.Debugf("start load password file: %s", passwordFile)
	bytes, err := ioutil.ReadFile(passwordFile)
	if err != nil {
		log.Warnf("load password file failed: %s", err)
		return nil, err
	}
	err = json.Unmarshal(bytes, &passwordMap)
	if err != nil {
		log.Warnf("password file format error: %s", err)
		return nil, err
	}
	return passwordMap, nil
}
