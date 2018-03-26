// Package cfenv provides information about the current app deployed on Cloud Foundry, including any bound service(s).
package cfenv

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"

	"github.com/mitchellh/mapstructure"
)

// New creates a new App with the provided environment.
func New(env map[string]string) (*App, error) {
	var app App
	appVar := env["VCAP_APPLICATION"]
	if err := json.Unmarshal([]byte(appVar), &app); err != nil {
		return nil, err
	}
	// duplicate the InstanceID to the previously named ID field for backwards
	// compatibility
	app.ID = app.InstanceID

	app.Home = env["HOME"]
	app.MemoryLimit = env["MEMORY_LIMIT"]
	if port, err := strconv.Atoi(env["PORT"]); err == nil {
		app.Port = port
	}
	app.WorkingDir = env["PWD"]
	app.TempDir = env["TMPDIR"]
	app.User = env["USER"]

	var rawServices map[string]interface{}
	servicesVar := env["VCAP_SERVICES"]
	if err := json.Unmarshal([]byte(servicesVar), &rawServices); err != nil {
		return nil, err
	}

	services := make(map[string][]Service)
	for k, v := range rawServices {
		var serviceInstances []Service
		if err := mapstructure.WeakDecode(v, &serviceInstances); err != nil {
			return nil, err
		}
		services[k] = serviceInstances
	}
	app.Services = services
	return &app, nil
}

// Current creates a new App with the current environment; returns an error if the current environment is not a Cloud Foundry environment
func Current() (*App, error) {
	return New(CurrentEnv())
}

// IsRunningOnCF returns true if the current environment is Cloud Foundry and false if it is not Cloud Foundry
func IsRunningOnCF() bool {
	return strings.TrimSpace(os.Getenv("VCAP_APPLICATION")) != ""
}
