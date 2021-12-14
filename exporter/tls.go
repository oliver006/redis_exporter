package exporter

import (
	"crypto/tls"
	"crypto/x509"
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

// CreateServerTLSConfig verifies configured files and return a prepared tls.Config
func (e *Exporter) CreateServerTLSConfig(certFile, keyFile, caCertFile string) (*tls.Config, error) {
	// Verify that the initial key pair is accepted
	_, err := LoadKeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	tlsConfig := tls.Config{
		GetCertificate: GetServerCertificateFunc(certFile, keyFile),
	}
	if caCertFile != "" {
		certificates, err := LoadCAFile(caCertFile)
		if err != nil {
			return nil, err
		}
		tlsConfig.ClientCAs = certificates
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return &tlsConfig, nil
}

// GetServerCertificateFunc returns a function for tls.Config.GetCertificate
func GetServerCertificateFunc(certFile, keyFile string) func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		return LoadKeyPair(certFile, keyFile)
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
