// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package receiver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// bucketName is the JetStream KV bucket usage events are stored in. One bucket is enough for
// this reference receiver; a production sink could shard by tenant or time window.
const bucketName = "USAGE_EVENTS"

// Config configures an embedded NATS JetStream Server.
type Config struct {
	// StoreDir is the on-disk directory JetStream persists to. Data here survives process
	// restarts, which is the entire point of using JetStream instead of an in-memory map:
	// the colocated-receiver topology in the proposal must survive an extproc crash.
	StoreDir string
}

// Server is an HTTP sink for UsageEvents backed by an embedded, in-process NATS JetStream
// server. It never opens a NATS network listener: the HTTP API is the only external surface,
// which is what makes it suitable as a minimal sidecar next to Envoy AI Gateway.
type Server struct {
	ns     *server.Server
	nc     *nats.Conn
	kv     jetstream.KeyValue
	logger *slog.Logger
}

// New starts an embedded NATS server with JetStream enabled, rooted at cfg.StoreDir, and
// provisions the KV bucket used to durably store and deduplicate UsageEvents.
func New(ctx context.Context, cfg Config) (*Server, error) {
	if cfg.StoreDir == "" {
		return nil, errors.New("receiver: StoreDir must be set")
	}

	ns, err := server.NewServer(&server.Options{
		JetStream:  true,
		StoreDir:   cfg.StoreDir,
		DontListen: true, // in-process only: no TCP port, so there is nothing to secure/expose.
		Port:       -1,
	})
	if err != nil {
		return nil, fmt.Errorf("receiver: creating embedded nats server: %w", err)
	}
	ns.Start()
	if !ns.ReadyForConnections(10 * time.Second) {
		ns.Shutdown()
		return nil, errors.New("receiver: embedded nats server did not become ready")
	}

	nc, err := nats.Connect("", nats.InProcessServer(ns))
	if err != nil {
		ns.Shutdown()
		return nil, fmt.Errorf("receiver: connecting to embedded nats server: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		ns.Shutdown()
		return nil, fmt.Errorf("receiver: creating jetstream context: %w", err)
	}

	kv, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket:  bucketName,
		Storage: jetstream.FileStorage,
	})
	if err != nil {
		nc.Close()
		ns.Shutdown()
		return nil, fmt.Errorf("receiver: provisioning KV bucket: %w", err)
	}

	return &Server{
		ns:     ns,
		nc:     nc,
		kv:     kv,
		logger: slog.Default(),
	}, nil
}

// Close drains the NATS connection and shuts down the embedded server.
func (s *Server) Close() error {
	s.nc.Close()
	s.ns.Shutdown()
	s.ns.WaitForShutdown()
	return nil
}

// Handler returns the HTTP handler exposing the sink's API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.HandleFunc("POST /v1/usage-events", s.handlePublish)
	mux.HandleFunc("GET /v1/usage-events", s.handleList)
	mux.HandleFunc("GET /v1/usage-events/{eventID}", s.handleGet)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	if s.kv == nil || !s.ns.JetStreamIsCurrent() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready"))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// kvKey derives a JetStream KV key from an arbitrary event_id. KV keys are restricted to
// [-/_=.a-zA-Z0-9]; UsageEvent's event_id format (e.g. "req-abc|llmroute|backend") is not,
// so it is base64url-encoded rather than used directly.
func kvKey(eventID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(eventID))
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	var event map[string]any
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	eventID, _ := event["event_id"].(string)
	if eventID == "" {
		writeError(w, http.StatusBadRequest, "event_id is required")
		return
	}

	raw, err := json.Marshal(event)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unable to re-marshal event: "+err.Error())
		return
	}

	// Create (not Put) is the dedup mechanism: it fails with ErrKeyExists if the event was
	// already durably stored, matching the proposal's "acknowledges events and deduplicates
	// on event_id" requirement for the reference receiver. The HTTP ack is only returned
	// after JetStream has persisted the write.
	_, err = s.kv.Create(r.Context(), kvKey(eventID), raw)
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, map[string]string{"event_id": eventID, "status": "accepted"})
	case errors.Is(err, jetstream.ErrKeyExists):
		writeJSON(w, http.StatusOK, map[string]string{"event_id": eventID, "status": "duplicate"})
	default:
		s.logger.Error("failed to persist usage event", "event_id", eventID, "error", err)
		writeError(w, http.StatusServiceUnavailable, "failed to persist event")
	}
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("eventID")
	entry, err := s.kv.Get(r.Context(), kvKey(eventID))
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}
	if err != nil {
		s.logger.Error("failed to read usage event", "event_id", eventID, "error", err)
		writeError(w, http.StatusServiceUnavailable, "failed to read event")
		return
	}

	var payload map[string]any
	if err := json.Unmarshal(entry.Value(), &payload); err != nil {
		writeError(w, http.StatusInternalServerError, "stored event is not valid JSON")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"event_id": eventID, "payload": payload})
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	keys, err := s.kv.Keys(r.Context())
	if err != nil && !errors.Is(err, jetstream.ErrNoKeysFound) {
		s.logger.Error("failed to list usage events", "error", err)
		writeError(w, http.StatusServiceUnavailable, "failed to list events")
		return
	}

	events := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		entry, err := s.kv.Get(r.Context(), key)
		if err != nil {
			s.logger.Error("failed to read usage event during list", "key", key, "error", err)
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(entry.Value(), &payload); err != nil {
			continue
		}
		events = append(events, payload)
	}

	writeJSON(w, http.StatusOK, map[string]any{"count": len(events), "events": events})
}
