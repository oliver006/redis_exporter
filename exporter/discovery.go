package exporter

import (
	"encoding/csv"
	"os"
	"strings"

	"github.com/cloudfoundry-community/go-cfenv"
	log "github.com/sirupsen/logrus"
)

// loadRedisArgs loads the configuration for which redis hosts to monitor from either
// the environment or as passed from program arguments. Returns the list of host addrs,
// passwords, and their aliases.
func LoadRedisArgs(addr, password, alias, separator string) ([]string, []string, []string) {
	if addr == "" {
		addr = "redis://localhost:6379"
	}
	addrs := strings.Split(addr, separator)
	passwords := strings.Split(password, separator)
	for len(passwords) < len(addrs) {
		passwords = append(passwords, passwords[0])
	}
	aliases := strings.Split(alias, separator)
	for len(aliases) < len(addrs) {
		aliases = append(aliases, aliases[0])
	}
	return addrs, passwords, aliases
}

// loadRedisFile opens the specified file and loads the configuration for which redis
// hosts to monitor. Returns the list of hosts addrs, passwords, and their aliases.
func LoadRedisFile(fileName string) ([]string, []string, []string, error) {
	var addrs []string
	var passwords []string
	var aliases []string
	file, err := os.Open(fileName)
	if err != nil {
		return nil, nil, nil, err
	}
	r := csv.NewReader(file)
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, nil, nil, err
	}
	file.Close()
	// For each line, test if it contains an optional password and alias and provide them,
	// else give them empty strings
	for _, record := range records {
		length := len(record)
		switch length {
		case 3:
			addrs = append(addrs, record[0])
			passwords = append(passwords, record[1])
			aliases = append(aliases, record[2])
		case 2:
			addrs = append(addrs, record[0])
			passwords = append(passwords, record[1])
			aliases = append(aliases, "")
		case 1:
			addrs = append(addrs, record[0])
			passwords = append(passwords, "")
			aliases = append(aliases, "")
		}
	}
	return addrs, passwords, aliases, nil
}

func GetCloudFoundryRedisBindings() (addrs, passwords, aliases []string) {
	if !cfenv.IsRunningOnCF() {
		return
	}

	appEnv, err := cfenv.Current()
	if err != nil {
		log.Warnln("Unable to get current CF environment", err)
		return
	}

	redisServices, err := appEnv.Services.WithTag("redis")
	if err != nil {
		log.Warnln("Error while getting redis services", err)
		return
	}

	for _, redisService := range redisServices {
		credentials := redisService.Credentials
		host := getAlternative(credentials, "host", "hostname")
		port := getAlternative(credentials, "port")
		password := getAlternative(credentials, "password")

		addr := host + ":" + port
		alias := redisService.Name

		addrs = append(addrs, addr)
		passwords = append(passwords, password)
		aliases = append(aliases, alias)
	}

	return
}

func getAlternative(credentials map[string]interface{}, alternatives ...string) string {
	for _, key := range alternatives {
		if value, ok := credentials[key]; ok {
			return value.(string)
		}
	}
	return ""
}
