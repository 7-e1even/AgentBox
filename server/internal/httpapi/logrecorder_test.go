package httpapi

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentbox/internal/platform"
)

type auditRecorderStore struct {
	dispatched atomic.Int64
	mu         sync.Mutex
	logs       []platform.LogEntry
}

func (s *auditRecorderStore) DispatchAuditEvents(context.Context) error {
	s.dispatched.Add(1)
	return nil
}

func (s *auditRecorderStore) InsertLogs(_ context.Context, entries []platform.LogEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, entries...)
	return nil
}

func TestLogRecorderDispatchesIdleAuditAndMarksTelemetry(t *testing.T) {
	store := &auditRecorderStore{}
	recorder := newLogRecorder(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for store.dispatched.Load() == 0 {
		select {
		case <-ticker.C:
		case <-deadline.C:
			_ = recorder.Close(t.Context())
			t.Fatal("idle log recorder did not dispatch durable audit events")
		}
	}
	input := map[string]any{"reason": "test"}
	recorder.Record(platform.LogEntry{Category: platform.LogCategoryAPI, Action: "diagnostic", Detail: input})
	beforeClose := store.dispatched.Load()
	if err := recorder.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if store.dispatched.Load() <= beforeClose {
		t.Fatal("shutdown did not dispatch durable audit events")
	}
	if len(store.logs) != 1 || store.logs[0].Detail["delivery"] != "best-effort" {
		t.Fatalf("telemetry logs=%#v", store.logs)
	}
	if _, mutated := input["delivery"]; mutated {
		t.Fatal("log recording mutated the caller's metadata map")
	}
}

func TestUserContextCarriesAuthenticatedAuditActor(t *testing.T) {
	user := platform.User{ID: "authenticated-user", Name: "Operator"}
	ctx := withUserAuditContext(t.Context(), user)
	actor := platform.AuditActorFromContext(ctx)
	if actor.Type != "user" || actor.ID != user.ID || actor.Name != user.Name || userFromContext(ctx).ID != user.ID {
		t.Fatalf("authenticated actor=%#v user=%#v", actor, userFromContext(ctx))
	}
}
