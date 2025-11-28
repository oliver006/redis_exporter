package exporter

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

func (e *Exporter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := e.verifyBasicAuth(r.BasicAuth()); err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="redis-exporter, charset=UTF-8"`)
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	e.mux.ServeHTTP(w, r)
}

func (e *Exporter) healthHandler(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte(`ok`))
}

func (e *Exporter) indexHandler(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte(`<html>
<head><title>Redis Exporter ` + e.buildInfo.Version + `</title></head>
<body>
<h1>Redis Exporter ` + e.buildInfo.Version + `</h1>
<p><a href='` + e.options.MetricsPath + `'>Metrics</a></p>
</body>
</html>
`))
}

func (e *Exporter) scrapeHandler(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("target")
	if target == "" {
		http.Error(w, "'target' parameter must be specified", http.StatusBadRequest)
		e.targetScrapeRequestErrors.Inc()
		return
	}

	if !strings.Contains(target, "://") {
		target = "redis://" + target
	}

	u, err := url.Parse(target)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid 'target' parameter, parse err: %ck ", err), http.StatusBadRequest)
		e.targetScrapeRequestErrors.Inc()
		return
	}

	opts := e.options

	if pwd, ok := e.lookupPasswordInPasswordMap(target); ok {
		opts.Password = pwd
	}

	// get rid of username/password info in "target" so users don't send them in plain text via http
	// and save "user" in options so we can use it later when connecting to the redis instance
	// the password will be looked up from the password file
	if u.User != nil {
		opts.User = u.User.Username()
		u.User = nil
	}
	target = u.String()

	if ck := r.URL.Query().Get("check-keys"); ck != "" {
		opts.CheckKeys = ck
	}

	if csk := r.URL.Query().Get("check-single-keys"); csk != "" {
		opts.CheckSingleKeys = csk
	}

	if cs := r.URL.Query().Get("check-streams"); cs != "" {
		opts.CheckStreams = cs
	}

	if css := r.URL.Query().Get("check-single-streams"); css != "" {
		opts.CheckSingleStreams = css
	}

	if cntk := r.URL.Query().Get("count-keys"); cntk != "" {
		opts.CountKeys = cntk
	}

	opts.Registry = prometheus.NewRegistry()

	_, err = NewRedisExporter(target, opts)
	if err != nil {
		http.Error(w, fmt.Sprintf("NewRedisExporter() error: %v", err), http.StatusBadRequest)
		e.targetScrapeRequestErrors.Inc()
		return
	}

	promhttp.HandlerFor(
		opts.Registry, promhttp.HandlerOpts{ErrorHandling: promhttp.ContinueOnError},
	).ServeHTTP(w, r)
}

func (e *Exporter) discoverClusterNodesHandler(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("target")
	var c redis.Conn
	var err error

	// Store the original scheme for output
	originalScheme := "redis" // Default to redis
	if strings.HasPrefix(target, "rediss://") {
		originalScheme = "rediss"
	} else if strings.HasPrefix(target, "valkey://") {
		originalScheme = "valkey"
	} else if strings.HasPrefix(target, "valkeys://") {
		originalScheme = "valkeys"
	}

	if target == "" {
		if !e.options.IsCluster {
			http.Error(w, "The discovery endpoint is only available on a redis cluster", http.StatusBadRequest)
			return
		}
		c, err = e.connectToRedisCluster()
	} else {
		c, err = e.connectToRedisClusterWithURI(target)
	}

	if err != nil {
		http.Error(w, fmt.Sprintf("Couldn't connect to redis cluster: %s", err), http.StatusInternalServerError)
		return
	}
	defer c.Close()

	nodes, err := e.getClusterNodes(c)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch cluster nodes: %s", err), http.StatusInternalServerError)
		return
	}

	if target != "" {
		if password, ok := e.lookupPasswordInPasswordMap(target); ok {
			e.DiscoveredNodesPasswordsMutex.Lock()
			for _, node := range nodes {
				nodeAddr := fmt.Sprintf("%s://%s", originalScheme, node)
				e.DiscoveredNodesPasswords[nodeAddr] = password
				log.Debugf("Cached password for discovered node: %s", nodeAddr)
			}
			e.DiscoveredNodesPasswordsMutex.Unlock()
		}
	}

	discovery := []struct {
		Targets []string          `json:"targets"`
		Labels  map[string]string `json:"labels"`
	}{
		{
			Targets: make([]string, len(nodes)),
			Labels:  make(map[string]string, 0),
		},
	}

	for i, node := range nodes {
		discovery[0].Targets[i] = fmt.Sprintf("%s://%s", originalScheme, node)
	}

	data, err := json.MarshalIndent(discovery, "", "  ")
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal discovery data: %s", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func (e *Exporter) reloadPwdFile(w http.ResponseWriter, r *http.Request) {
	if e.options.RedisPwdFile == "" {
		http.Error(w, "There is no pwd file specified", http.StatusBadRequest)
		return
	}
	log.Debugf("Reload redisPwdFile")
	passwordMap, err := LoadPwdFile(e.options.RedisPwdFile)
	if err != nil {
		log.Errorf("Error reloading redis passwords from file %s, err: %s", e.options.RedisPwdFile, err)
		http.Error(w, "failed to reload passwords file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	e.Lock()
	e.options.PasswordMap = passwordMap
	e.Unlock()
	_, _ = w.Write([]byte(`ok`))
}

func (e *Exporter) isBasicAuthConfigured() bool {
	return e.options.BasicAuthUsername != "" && e.options.BasicAuthPassword != ""
}

func (e *Exporter) verifyBasicAuth(user, password string, authHeaderSet bool) error {
	if !e.isBasicAuthConfigured() {
		return nil
	}

	if !authHeaderSet {
		return errors.New("Unauthorized")
	}

	userCorrect := subtle.ConstantTimeCompare([]byte(user), []byte(e.options.BasicAuthUsername))
	passCorrect := subtle.ConstantTimeCompare([]byte(password), []byte(e.options.BasicAuthPassword))

	if userCorrect == 0 || passCorrect == 0 {
		return errors.New("Unauthorized")
	}

	return nil
}
