package exporter

import (
	"fmt"
	"strconv"
	"strings"
)

func supportsValkeyClusterScan(info string) bool {
	version, ok := infoField(info, "valkey_version")
	if !ok {
		return false
	}
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr != nil || minorErr != nil {
		return false
	}
	return major > 9 || major == 9 && minor >= 1
}

func infoField(info, name string) (string, bool) {
	for line := range strings.SplitSeq(info, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && key == name {
			return value, true
		}
	}
	return "", false
}

func clusterDatabaseCount(config map[string]string) (int, error) {
	value, ok := config["cluster-databases"]
	if !ok {
		return 0, nil
	}
	count, err := strconv.Atoi(value)
	if err != nil || count < 1 {
		return 0, fmt.Errorf("invalid config value for key cluster-databases: %q", value)
	}
	return count, nil
}
