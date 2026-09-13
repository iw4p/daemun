package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// The AdmissionReview contract, written out rather than imported. The real types
// in k8s.io/api/admission/v1 have more fields, not different ones.

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
	UID      string   `json:"uid"`
	Allowed  bool     `json:"allowed"`
	Warnings []string `json:"warnings,omitempty"`
	Status   *Status  `json:"status,omitempty"`
}

type Status struct {
	Message string `json:"message"`
}

func writeReview(w http.ResponseWriter, resp *AdmissionResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(AdmissionReview{
		APIVersion: "admission.k8s.io/v1",
		Kind:       "AdmissionReview",
		Response:   resp,
	})
}

// allow and deny both echo the request uid — that is how the API server matches
// a reply to the call it made.
func allow(uid string, warnings []string) *AdmissionResponse {
	return &AdmissionResponse{UID: uid, Allowed: true, Warnings: warnings}
}

func deny(uid string, reasons []string, warnings []string) *AdmissionResponse {
	return &AdmissionResponse{
		UID:      uid,
		Allowed:  false,
		Warnings: warnings,
		Status:   &Status{Message: strings.Join(reasons, "; ")},
	}
}
