package exporter

import (
	"encoding/json"
	"io/ioutil"

	log "github.com/sirupsen/logrus"
)

type PasswordMap struct {
	RedisPwd map[string]string
}

func NewPasswordMap() *PasswordMap {
	return &PasswordMap{}
}

func (p *PasswordMap) LoadPwdFile(pwdFile string) {
	log.Debugf("start load password file: %s", pwdFile)
	bytes, err := ioutil.ReadFile(pwdFile)
	if err != nil {
		log.Fatalf("load password file failed: %s", err)
	}
	err = json.Unmarshal(bytes, &p.RedisPwd)
	if err != nil {
		log.Fatalf("password file format error: %s", err)
	}
}
