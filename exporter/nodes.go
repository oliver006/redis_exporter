package exporter

import (
	"log/slog"
	"regexp"
	"strings"

	"github.com/gomodule/redigo/redis"
)

var reNodeAddress = regexp.MustCompile(`^(?P<ip>.+):(?P<port>\d+)@(?P<cport>\d+)(?:,(?P<hostname>.+))?`)

func (e *Exporter) getClusterNodes(c redis.Conn) ([]string, error) {
	output, err := redis.String(doRedisCmd(c, "CLUSTER", "NODES"))
	if err != nil {
		slog.Error("Error getting cluster nodes", "error", err)
		return nil, err
	}

	lines := strings.Split(output, "\n")
	nodes := []string{}

	for _, line := range lines {
		if node, ok := parseClusterNodeString(line); ok {
			nodes = append(nodes, node)
		}
	}

	return nodes, nil
}

/*
<id> <ip:port@cport[,hostname]> <flags> <master> <ping-sent> <pong-recv> <config-epoch> <link-state> <slot> <slot> ... <slot>
eaf69c70d876558a948ba62af0884a37d42c9627 127.0.0.1:7002@17002 master - 0 1742836359057 3 connected 10923-16383
*/
func parseClusterNodeString(node string) (string, bool) {
	slog.Debug("parseClusterNodeString node", "node", node)

	fields := strings.Fields(node)
	if len(fields) < 2 {
		slog.Debug("Invalid field count for node", "node", node)
		return "", false
	}

	address := reNodeAddress.FindStringSubmatch(fields[1])
	if len(address) < 3 {
		slog.Debug("Invalid format for node address", "address", fields[1])
		return "", false
	}

	return address[1] + ":" + address[2], true
}
