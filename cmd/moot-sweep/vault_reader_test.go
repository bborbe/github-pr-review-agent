// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	agentlib "github.com/bborbe/agent"
)

// The gateway header is the difference between a working live read and a sweep
// that closes nothing: vault-api applies gatewaySecretMiddleware to /api/v1/*
// whenever GATEWAY_SECRET is set, which it is on both clusters. An earlier
// revision claimed no auth was needed because a grep of the container's `args`
// found no --gateway-secret; the flag arrives by env instead.
func TestVaultTaskReaderSendsGatewaySecret(t *testing.T) {
	tests := []struct {
		name      string
		secret    string
		wantValue string
		wantSent  bool
	}{
		{name: "secret configured is sent", secret: "s3cr3t", wantValue: "s3cr3t", wantSent: true},
		{name: "empty secret sends no header", secret: "", wantSent: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			var present bool
			server := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					got = r.Header.Get("X-Gateway-Secret")
					_, present = r.Header["X-Gateway-Secret"]
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"frontmatter":{"status":"in_progress"},"body":"x"}`))
				},
			))
			defer server.Close()

			reader := newVaultTaskReader(server.URL, tt.secret)
			fm, _, err := reader.ReadTask(context.Background(), agentlib.TaskIdentifier("t-1"))
			if err != nil {
				t.Fatalf("ReadTask: %v", err)
			}
			if fm == nil {
				t.Fatal("expected frontmatter, got nil")
			}
			if present != tt.wantSent {
				t.Fatalf("header present = %v, want %v", present, tt.wantSent)
			}
			if tt.wantSent && got != tt.wantValue {
				t.Fatalf("header = %q, want %q", got, tt.wantValue)
			}
		})
	}
}

// A 404 means the record is gone, not an error: the sweep must leave it alone
// rather than closing something it cannot see.
func TestVaultTaskReaderNotFoundIsNotAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	))
	defer server.Close()

	fm, body, err := newVaultTaskReader(server.URL, "").
		ReadTask(context.Background(), agentlib.TaskIdentifier("t-2"))
	if err != nil {
		t.Fatalf("404 must not be an error, got %v", err)
	}
	if fm != nil || body != "" {
		t.Fatalf("404 must return empty result, got %v / %q", fm, body)
	}
}

// A 401 must surface as an error so the sweep fails closed and says why — the
// silent-fallback shape is what hid the missing header.
func TestVaultTaskReaderUnauthorizedIsAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		},
	))
	defer server.Close()

	_, _, err := newVaultTaskReader(server.URL, "").
		ReadTask(context.Background(), agentlib.TaskIdentifier("t-3"))
	if err == nil {
		t.Fatal("401 must return an error so the sweep fails closed")
	}
}
