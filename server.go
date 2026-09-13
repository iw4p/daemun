package main

import (
	"encoding/json"
	"log"
	"net/http"
)

type Server struct{ source *Source }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /validate", s.validate)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
}

func (s *Server) validate(w http.ResponseWriter, r *http.Request) {
	var review AdmissionReview
	if err := json.NewDecoder(r.Body).Decode(&review); err != nil || review.Request == nil {
		http.Error(w, "expected an AdmissionReview with a request", http.StatusBadRequest)
		return
	}
	req := review.Request

	obj, err := DecodeObject(req.Object, req.Namespace)
	if err != nil {
		writeReview(w, deny(req.UID, []string{"could not read the object: " + err.Error()}, nil))
		return
	}

	result := s.source.Policy().Evaluate(obj)
	if !result.Allowed() {
		log.Printf("DENY  %s/%s %s %v", obj.Namespace, obj.Name, obj.Kind, result.Deny)
		writeReview(w, deny(req.UID, result.Deny, result.Warn))
		return
	}

	if len(result.Warn) > 0 {
		log.Printf("WARN  %s/%s %s %v", obj.Namespace, obj.Name, obj.Kind, result.Warn)
	}
	writeReview(w, allow(req.UID, result.Warn))
}
