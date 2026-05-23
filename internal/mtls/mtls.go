// Package mtls builds mutual-TLS gRPC transport credentials from PEM files.
// The agent dials out to the orchestrator, so the orchestrator is the TLS
// server and the agent the TLS client; both present certs signed by a shared CA.
package mtls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"
)

func loadCAPool(caFile string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read CA %s: %w", caFile, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no certificates found in %s", caFile)
	}
	return pool, nil
}

// ServerCreds builds orchestrator-side credentials that require and verify a
// client certificate signed by caFile.
func ServerCreds(certFile, keyFile, caFile string) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load server keypair: %w", err)
	}
	pool, err := loadCAPool(caFile)
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	}), nil
}

// ServerTLSCreds builds orchestrator-side credentials that present certFile/
// keyFile but do NOT request a client certificate. Used for the token-auth
// transport, where TLS provides encryption + server identity and the agent
// authenticates with a bearer token instead of a client cert.
func ServerTLSCreds(certFile, keyFile string) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load server keypair: %w", err)
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.NoClientCert,
		MinVersion:   tls.VersionTLS13,
	}), nil
}

// ClientTLSCreds builds agent-side credentials that verify the orchestrator's
// certificate against caFile but present no client certificate. Pairs with
// ServerTLSCreds for token-based auth. serverName must match a SAN on the
// orchestrator certificate.
func ClientTLSCreds(caFile, serverName string) (credentials.TransportCredentials, error) {
	pool, err := loadCAPool(caFile)
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(&tls.Config{
		RootCAs:    pool,
		ServerName: serverName,
		MinVersion: tls.VersionTLS13,
	}), nil
}

// ClientCreds builds agent-side credentials that present certFile/keyFile and
// verify the orchestrator's certificate against caFile. serverName must match a
// SAN on the orchestrator certificate.
func ClientCreds(certFile, keyFile, caFile, serverName string) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load client keypair: %w", err)
	}
	pool, err := loadCAPool(caFile)
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ServerName:   serverName,
		MinVersion:   tls.VersionTLS13,
	}), nil
}
