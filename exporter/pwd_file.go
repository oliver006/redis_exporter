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

func (p *PasswordMap) LoadPwdFile(pwdFile string) error {
	log.Debugf("start load password file: %s", pwdFile)
	bytes, err := ioutil.ReadFile(pwdFile)
	if err != nil {
		log.Warnf("load password file failed: %s", err)
		return err
	}
	err = json.Unmarshal(bytes, &p.RedisPwd)
	if err != nil {
		log.Warnf("password file format error: %s", err)
		return err
	}
	return nil
}
