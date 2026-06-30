package handlers

import (
	"testing"

	"github.com/fasthttp/router"
)

func TestCompletionRegisterRoutes_LiteSurfaceOnly(t *testing.T) {
	h := &CompletionHandler{}
	r := router.New()
	h.RegisterRoutes(r)

	registered := []struct {
		method string
		path   string
	}{
		{"GET", "/v1/models"},
		{"POST", "/v1/chat/completions"},
		{"POST", "/v1/responses"},
		{"POST", "/v1/images/generations"},
		{"POST", "/v1/images/edits"},
	}
	for _, tc := range registered {
		handle, _ := r.Lookup(tc.method, tc.path, nil)
		if handle == nil {
			t.Fatalf("expected %s %s to be registered", tc.method, tc.path)
		}
	}

	removed := []struct {
		method string
		path   string
	}{
		{"POST", "/v1/images/variations"},
		{"GET", "/v1/async/responses/job-1"},
		{"POST", "/v1/async/responses"},
		{"POST", "/v1/async/images/generations"},
		{"POST", "/v1/async/images/edits"},
	}
	for _, tc := range removed {
		handle, _ := r.Lookup(tc.method, tc.path, nil)
		if handle != nil {
			t.Fatalf("expected %s %s to be absent from Lite route surface", tc.method, tc.path)
		}
	}
}
