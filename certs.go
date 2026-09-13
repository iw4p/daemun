package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"time"
)

// selfSignedCert makes one certificate that acts as its own authority, in memory,
// at startup. The webhook serves it, and the same bytes go to the API server as
// the caBundle.
//
// This is why installing Kyverno never asks you to run openssl: its certmanager
// controller does exactly this (plus rotation, which we skip).
func selfSignedCert(dnsNames []string) (tls.Certificate, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: dnsNames[0]},

		// The names the API server will dial. A TLS client checks these and
		// ignores CommonName entirely, so getting them wrong fails the handshake.
		DNSNames: dnsNames,

		NotBefore: time.Now().Add(-time.Minute),
		NotAfter:  time.Now().AddDate(1, 0, 0),

		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	// Signed by itself — template is both the certificate and the issuer.
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	return pair, certPEM, nil
}
