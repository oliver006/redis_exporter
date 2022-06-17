package exporter

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"

	log "github.com/sirupsen/logrus"
)

// CreateClientTLSConfig verifies configured files and return a prepared tls.Config
func (e *Exporter) CreateClientTLSConfig() (*tls.Config, error) {
	tlsConfig := tls.Config{
		InsecureSkipVerify: e.options.SkipTLSVerification,
	}

	if e.options.ClientCertFile != "" && e.options.ClientKeyFile != "" {
		cert, err := LoadKeyPair(e.options.ClientCertFile, e.options.ClientKeyFile)
		if err != nil {
			return nil, err
		}
		tlsConfig.Certificates = []tls.Certificate{*cert}
	}

	if e.options.CaCertFile != "" {
		certificates, err := LoadCAFile(e.options.CaCertFile)
		if err != nil {
			return nil, err
		}
		tlsConfig.RootCAs = certificates
	}

	return &tlsConfig, nil
}

var tlsVersions = map[string]uint16{
	"TLS1.3": tls.VersionTLS13,
	"TLS1.2": tls.VersionTLS12,
	"TLS1.1": tls.VersionTLS11,
	"TLS1.0": tls.VersionTLS10,
}

// CreateServerTLSConfig verifies configuration and return a prepared tls.Config
func (e *Exporter) CreateServerTLSConfig(certFile, keyFile, caCertFile, minVersionString string) (*tls.Config, error) {
	// Verify that the initial key pair is accepted
	_, err := LoadKeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	// Get minimum acceptable TLS version from the config string
	minVersion, ok := tlsVersions[minVersionString]
	if !ok {
		return nil, fmt.Errorf("configured minimum TLS version unknown: '%s'", minVersionString)
	}

	tlsConfig := tls.Config{
		MinVersion:     minVersion,
		GetCertificate: GetServerCertificateFunc(certFile, keyFile),
	}

	if caCertFile != "" {
		// Verify that the initial CA file is accepted when configured
		_, err := LoadCAFile(caCertFile)
		if err != nil {
			return nil, err
		}
		tlsConfig.GetConfigForClient = GetConfigForClientFunc(certFile, keyFile, caCertFile)
	}

	return &tlsConfig, nil
}

// GetServerCertificateFunc returns a function for tls.Config.GetCertificate
func GetServerCertificateFunc(certFile, keyFile string) func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		return LoadKeyPair(certFile, keyFile)
	}
}

// GetConfigForClientFunc returns a function for tls.Config.GetConfigForClient
func GetConfigForClientFunc(certFile, keyFile, caCertFile string) func(*tls.ClientHelloInfo) (*tls.Config, error) {
	return func(*tls.ClientHelloInfo) (*tls.Config, error) {
		certificates, err := LoadCAFile(caCertFile)
		if err != nil {
			return nil, err
		}

		tlsConfig := tls.Config{
			ClientAuth:     tls.RequireAndVerifyClientCert,
			ClientCAs:      certificates,
			GetCertificate: GetServerCertificateFunc(certFile, keyFile),
		}
		return &tlsConfig, nil
	}
}

// LoadKeyPair reads and parses a public/private key pair from a pair of files.
// The files must contain PEM encoded data.
func LoadKeyPair(certFile, keyFile string) (*tls.Certificate, error) {
	log.Debugf("Load key pair: %s %s", certFile, keyFile)
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &cert, nil
}

// LoadCAFile reads and parses CA certificates from a file into a pool.
// The file must contain PEM encoded data.
func LoadCAFile(caFile string) (*x509.CertPool, error) {
	log.Debugf("Load CA cert file: %s", caFile)
	pemCerts, err := ioutil.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pemCerts)
	return pool, nil
}
