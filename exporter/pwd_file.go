package exporter

import (
	"encoding/json"
	"log/slog"
	"os"
)

// LoadPwdFile reads the redis password file and returns the password map
func LoadPwdFile(passwordFile string) (map[string]string, error) {
	res := make(map[string]string)

	slog.Debug("start load password file", "file", passwordFile)
	bytes, err := os.ReadFile(passwordFile)
	if err != nil {
		slog.Warn("load password file failed", "error", err)
		return nil, err
	}
	err = json.Unmarshal(bytes, &res)
	if err != nil {
		slog.Warn("password file format error", "error", err)
		return nil, err
	}

	slog.Info("Loaded entries from password file", "count", len(res), "file", passwordFile)
	for k := range res {
		slog.Debug("password entry", "key", k)
	}

	return res, nil
}
