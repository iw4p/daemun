package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Every pod gets these three files mounted automatically. They are how a pod
// talks to its own API server without any config.
const serviceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"

// publishCABundle writes our certificate into our own ValidatingWebhookConfiguration,
// so the API server knows to trust us. Nobody has to run base64 by hand.
func publishCABundle(configName string, caPEM []byte) error {
	token, err := os.ReadFile(serviceAccountDir + "/token")
	if err != nil {
		return fmt.Errorf("no service account token — is this running in a pod? %w", err)
	}
	clusterCA, err := os.ReadFile(serviceAccountDir + "/ca.crt")
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(clusterCA) {
		return fmt.Errorf("could not parse the cluster CA")
	}

	// "add" rather than "replace": in JSON Patch, add also overwrites an
	// existing value, so this works whether or not caBundle is already set.
	patch, err := json.Marshal([]map[string]any{{
		"op":    "add",
		"path":  "/webhooks/0/clientConfig/caBundle",
		"value": base64.StdEncoding.EncodeToString(caPEM),
	}})
	if err != nil {
		return err
	}

	url := fmt.Sprintf(
		"https://%s:%s/apis/admissionregistration.k8s.io/v1/validatingwebhookconfigurations/%s",
		os.Getenv("KUBERNETES_SERVICE_HOST"),
		os.Getenv("KUBERNETES_SERVICE_PORT"),
		configName,
	)

	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewReader(patch))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+string(token))
	req.Header.Set("Content-Type", "application/json-patch+json")

	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("API server returned %s: %s", resp.Status, body)
	}
	return nil
}
