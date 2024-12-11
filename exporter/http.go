package exporter

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

func (e *Exporter) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	err := e.verifyBasicAuth(r.BasicAuth())
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="redis-exporter, charset=UTF-8"`)
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

	// get rid of username/password info in "target" so users don't send them in plain text via http
	u.User = nil
	target = u.String()

	opts := e.options

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

	registry := prometheus.NewRegistry()
	opts.Registry = registry

	_, err = NewRedisExporter(target, opts)
	if err != nil {
		http.Error(w, "NewRedisExporter() err: err", http.StatusBadRequest)
		e.targetScrapeRequestErrors.Inc()
		return
	}

	promhttp.HandlerFor(
		registry, promhttp.HandlerOpts{ErrorHandling: promhttp.ContinueOnError},
	).ServeHTTP(w, r)
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
