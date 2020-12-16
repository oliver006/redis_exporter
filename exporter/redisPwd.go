package exporter

import (
	"encoding/json"
	"io/ioutil"

	log "github.com/sirupsen/logrus"
)

var RedisPwd map[string]string

func LoadPwdFile(pwdFile string) error {
	log.Debugf("start load password file: %s", pwdFile)
	bytes, err := ioutil.ReadFile(pwdFile)
	if err != nil {
		log.Debugf("load password file failed: %s", err)
		return err
	}
	err = json.Unmarshal(bytes, &RedisPwd)
	if err != nil {
		log.Debugf("password file format error: %s", err)
		return err
	}
	return nil
}
