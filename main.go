// daemun is a Kubernetes validating admission webhook.
//
// It mints its own TLS certificate at startup and publishes the CA into its own
// ValidatingWebhookConfiguration, so installing it needs no openssl and no
// Secret. The policy it enforces lives in a ConfigMap — see policy.go.
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

const reloadInterval = 10 * time.Second

func main() {
	var (
		addr       = env("ADDR", ":8443")
		service    = env("SERVICE_NAME", "daemun")
		namespace  = env("NAMESPACE", "daemun")
		configName = env("WEBHOOK_CONFIG", "daemun")
		policyPath = env("POLICY_FILE", "/etc/daemun/policy.json")
	)

	// Refuse to start without a policy: enforcing nothing looks exactly like
	// working.
	source, err := NewSource(policyPath)
	if err != nil {
		log.Fatalf("could not load %s: %v", policyPath, err)
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go source.Watch(ctx, reloadInterval)

	dnsNames := []string{
		fmt.Sprintf("%s.%s.svc", service, namespace),
		fmt.Sprintf("%s.%s.svc.cluster.local", service, namespace),
	}
	cert, caPEM, err := selfSignedCert(dnsNames)
	if err != nil {
		log.Fatalf("could not create a certificate: %v", err)
	}
	if err := publishCABundle(configName, caPEM); err != nil {
		log.Fatalf("could not publish our CA to validatingwebhookconfiguration/%s: %v", configName, err)
	}
	log.Printf("serving %v, CA published to validatingwebhookconfiguration/%s", dnsNames, configName)

	server := &http.Server{
		Addr:    addr,
		Handler: (&Server{source: source}).Handler(),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
	}
	if err := server.ListenAndServeTLS("", ""); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
