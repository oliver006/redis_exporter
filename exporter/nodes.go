package exporter

import (
	"regexp"
	"strings"

	"github.com/gomodule/redigo/redis"
	log "github.com/sirupsen/logrus"
)

var reNodeAddress = regexp.MustCompile(`^(?P<ip>.+):(?P<port>\d+)@(?P<cport>\d+)(?:,(?P<hostname>.+))?`)

func (e *Exporter) getClusterNodes(c redis.Conn) ([]string, error) {
	output, err := redis.String(doRedisCmd(c, "CLUSTER", "NODES"))
	if err != nil {
		log.Errorf("Error getting cluster nodes: %s", err)
		return nil, err
	}

	lines := strings.Split(output, "\n")
	nodes := []string{}

	for _, line := range lines {
		if node, ok := parseClusterNodeString(line, e.options.ClusterDiscoverHostnames); ok {
			nodes = append(nodes, node)
		}
	}

	return nodes, nil
}

/*
<id> <ip:port@cport[,hostname]> <flags> <master> <ping-sent> <pong-recv> <config-epoch> <link-state> <slot> <slot> ... <slot>
eaf69c70d876558a948ba62af0884a37d42c9627 127.0.0.1:7002@17002 master - 0 1742836359057 3 connected 10923-16383
*/
func parseClusterNodeString(node string, resolveHostname bool) (string, bool) {
	log.Debugf("parseClusterNodeString node: [%s]", node)

	fields := strings.Fields(node)
	if len(fields) < 2 {
		log.Debugf("Invalid field count for node: %s", node)
		return "", false
	}

	address := reNodeAddress.FindStringSubmatch(fields[1])
	if len(address) < 3 {
		log.Debugf("Invalid format for node address, got: %s", fields[1])
		return "", false
	}

	// address[1] = ip, address[2] = port, address[4] = hostname (may be empty)
	if resolveHostname && len(address) >= 5 && address[4] != "" {
		return address[4] + ":" + address[2], true
	}
	return address[1] + ":" + address[2], true
}
