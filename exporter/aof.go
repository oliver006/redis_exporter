package exporter

import (
	"os"
	"strings"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func getAofFilePath(c redis.Conn, configCommandName string) (string, error) {

	dir_response, err := redis.StringMap(doRedisCmd(c, configCommandName, "GET", "dir"))
	if err != nil {
		return "", err
	}
	dir := dir_response["dir"]
	appenddirname_response, err := redis.StringMap(doRedisCmd(c, "CONFIG", "GET", "appenddirname"))
	appenddirname := appenddirname_response["appenddirname"]
	if err != nil {
		return "", err
	}

	return dir + "/" + appenddirname, nil
}

func listAofIncrFiles(dir string) ([]string, error) {
	// List all aof incr files from appendonly dir

	files, err := os.ReadDir(dir)

	if err != nil {
		return nil, err
	}

	var incrFiles []string
	for _, file := range files {
		if strings.Contains(file.Name(), "incr.aof") {
			incrFiles = append(incrFiles, file.Name())
		}
	}

	return incrFiles, nil
}

func getFileSize(dir string, file string) (float64, error) {

	info, err := os.Stat(dir + string(os.PathSeparator) + file)
	if err != nil {
		return 0, err
	}

	return float64(info.Size()), nil

}

func (e *Exporter) extractAofFileSizeMetrics(ch chan<- prometheus.Metric, c redis.Conn, configCommandName string, overrideAofFilePath string) {

	log.Debug("extractAofFileSizeMetrics()")

	var dir string
	var err error

	if overrideAofFilePath != "" {
		dir = overrideAofFilePath
	} else {
		dir, err = getAofFilePath(c, configCommandName)
		if err != nil {
			log.Errorf("extractAofFileSizeMetrics() err: %s", err)
			return
		}
	}

	incrFiles, err := listAofIncrFiles(dir)
	log.Debug("incrFiles: ", incrFiles)
	if err != nil {
		log.Errorf("extractAofFileSizeMetrics() err: %s", err)
		return
	}

	for _, file := range incrFiles {
		size, err := getFileSize(dir, file)
		log.Debug("file: ", file, " size: ", size)
		if err != nil {
			log.Errorf("extractAofFileSizeMetrics() err: %s", err)
			continue
		}
		filename := strings.ReplaceAll(file, ".", "_")
		e.registerConstMetricGauge(ch, "aof_file_size_bytes", size, filename)
	}

}
