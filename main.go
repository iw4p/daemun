// A validating admission webhook, with nothing hidden.
//
// The Kubernetes API server calls this server over HTTPS before it writes a Pod
// to etcd. It sends an AdmissionReview as JSON; we send one back saying whether
// the write may proceed. That is the entire contract — there is no SDK here and
// no dependencies in go.mod, because none are needed.
//
// The policy itself lives in a ConfigMap, not in this file. See rules.go.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

// --- the wire format ---------------------------------------------------------
// These structs are the AdmissionReview API, written by hand. The real ones live
// in k8s.io/api/admission/v1; they have more fields, but not different ones.

type AdmissionReview struct {
	APIVersion string             `json:"apiVersion"`
	Kind       string             `json:"kind"`
	Request    *AdmissionRequest  `json:"request,omitempty"`
	Response   *AdmissionResponse `json:"response,omitempty"`
}

type AdmissionRequest struct {
	UID       string          `json:"uid"`
	Operation string          `json:"operation"`
	Namespace string          `json:"namespace"`
	Object    json.RawMessage `json:"object"`
}

type AdmissionResponse struct {
	UID     string  `json:"uid"`
	Allowed bool    `json:"allowed"`
	Status  *Status `json:"status,omitempty"`
}

type Status struct {
	Message string `json:"message"`
}

// partialPod is the slice of a Pod this webhook reads. Decoding only what you
// need keeps it working across API versions that add fields.
type partialPod struct {
	Metadata struct {
		Name         string            `json:"name"`
		GenerateName string            `json:"generateName"`
		Labels       map[string]string `json:"labels"`
	} `json:"metadata"`
}

// --- the server --------------------------------------------------------------

// server holds the current policy. The pointer is swapped by watchRules while
// requests are in flight, so reads are atomic and never block on a reload.
type server struct {
	rules atomic.Pointer[RuleSet]
}

func (s *server) validate(w http.ResponseWriter, r *http.Request) {
	var review AdmissionReview
	if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
		http.Error(w, "could not decode AdmissionReview: "+err.Error(), http.StatusBadRequest)
		return
	}
	if review.Request == nil {
		http.Error(w, "AdmissionReview carried no request", http.StatusBadRequest)
		return
	}
	req := review.Request

	var pod partialPod
	if err := json.Unmarshal(req.Object, &pod); err != nil {
		respond(w, req.UID, false, "could not read the Pod: "+err.Error())
		return
	}

	// A Pod created from a Deployment has no name yet — only generateName. The
	// API server fills the name in after admission, so never rely on it here.
	name := pod.Metadata.Name
	if name == "" {
		name = pod.Metadata.GenerateName + "(generated)"
	}

	// The only policy decision in this file is "ask the rules".
	if violation := s.rules.Load().Evaluate(pod.Metadata.Labels); violation != "" {
		log.Printf("DENY   %s  ns=%s  op=%s  %s", name, req.Namespace, req.Operation, violation)
		respond(w, req.UID, false, fmt.Sprintf("Pod %q rejected: %s", name, violation))
		return
	}

	log.Printf("ALLOW  %s  ns=%s  op=%s", name, req.Namespace, req.Operation)
	respond(w, req.UID, true, "")
}

// respond writes the AdmissionReview the API server is waiting for. The uid must
// be echoed back exactly — that is how the API server matches the reply to the
// request it sent.
func respond(w http.ResponseWriter, uid string, allowed bool, message string) {
	out := AdmissionReview{
		APIVersion: "admission.k8s.io/v1",
		Kind:       "AdmissionReview",
		Response: &AdmissionResponse{
			UID:     uid,
			Allowed: allowed,
		},
	}
	if message != "" {
		out.Response.Status = &Status{Message: message}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		log.Printf("could not write response: %v", err)
	}
}

// --- startup -----------------------------------------------------------------

func main() {
	addr := env("ADDR", ":8443")
	service := env("SERVICE_NAME", "admission-lab")
	namespace := env("NAMESPACE", "admission-lab")
	configName := env("WEBHOOK_CONFIG", "admission-lab")
	rulesPath := env("RULES_FILE", "/etc/admission-lab/rules.json")

	// Refuse to start without a policy. Running with none would silently allow
	// everything, which looks identical to working.
	initial, err := loadRules(rulesPath)
	if err != nil {
		log.Fatalf("could not load rules: %v", err)
	}
	srv := &server{}
	srv.rules.Store(initial)
	log.Printf("loaded %s from %s", initial.describe(), rulesPath)

	go watchRules(context.Background(), rulesPath, 10*time.Second, &srv.rules)

	// The two names the API server may dial this Service by.
	dnsNames := []string{
		fmt.Sprintf("%s.%s.svc", service, namespace),
		fmt.Sprintf("%s.%s.svc.cluster.local", service, namespace),
	}

	// Mint our own certificate, in memory. No files, no openssl.
	cert, caPEM, err := selfSignedCert(dnsNames)
	if err != nil {
		log.Fatalf("could not create a certificate: %v", err)
	}
	log.Printf("generated a certificate for %v", dnsNames)

	// Tell the API server to trust it, by writing it into our own webhook
	// config. Until this succeeds, every call to us would fail TLS.
	if err := publishCABundle(configName, caPEM); err != nil {
		log.Fatalf("could not publish our CA to validatingwebhookconfiguration/%s: %v", configName, err)
	}
	log.Printf("published our CA into validatingwebhookconfiguration/%s", configName)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /validate", srv.validate)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	httpServer := &http.Server{
		Addr:    addr,
		Handler: mux,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
	}

	log.Printf("listening on %s", addr)

	// Empty strings: the certificate is already in TLSConfig, not on disk.
	if err := httpServer.ListenAndServeTLS("", ""); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
