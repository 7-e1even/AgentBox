package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"agentbox/internal/platform"
)

type auditRecorderStore struct {
	dispatched atomic.Int64
	mu         sync.Mutex
	logs       []platform.LogEntry
}

type contextSeparatingLogStore struct {
	dispatchContext context.Context
	sharedContext   bool
}

func (s *contextSeparatingLogStore) DispatchAuditEvents(ctx context.Context) error {
	s.dispatchContext = ctx
	return nil
}

func (s *contextSeparatingLogStore) InsertLogs(ctx context.Context, _ []platform.LogEntry) error {
	s.sharedContext = ctx == s.dispatchContext
	return nil
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

func TestLogRecorderUsesFreshTimeoutForTelemetryAfterAuditDispatch(t *testing.T) {
	store := &contextSeparatingLogStore{}
	recorder := newLogRecorder(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	recorder.Record(platform.LogEntry{Category: platform.LogCategoryAPI, Action: "diagnostic"})
	if err := recorder.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if store.sharedContext {
		t.Fatal("audit dispatch and telemetry insert shared one timeout context")
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

func TestLogRecorderBoundsUTF8FieldsAndDetail(t *testing.T) {
	store := &auditRecorderStore{}
	recorder := newLogRecorder(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	recorder.Record(platform.LogEntry{
		Action:     strings.Repeat("界", logFieldMaxLen),
		Message:    strings.Repeat("界", logMessageMaxLen),
		RemoteAddr: strings.Repeat("界", logFieldMaxLen),
		Detail:     map[string]any{"payload": strings.Repeat("x", logDetailMaxBytes)},
	})
	if err := recorder.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(store.logs) != 1 {
		t.Fatalf("logs = %#v", store.logs)
	}
	entry := store.logs[0]
	for name, value := range map[string]string{"action": entry.Action, "message": entry.Message, "remoteAddr": entry.RemoteAddr} {
		if !utf8.ValidString(value) {
			t.Fatalf("%s was truncated to invalid UTF-8", name)
		}
	}
	if len(entry.Action) > logFieldMaxLen || len(entry.Message) > logMessageMaxLen || len(entry.RemoteAddr) > logFieldMaxLen {
		t.Fatalf("log fields were not bounded: %#v", entry)
	}
	if entry.Detail["delivery"] != "best-effort" || entry.Detail["truncated"] != true {
		t.Fatalf("oversized detail = %#v", entry.Detail)
	}
}

func TestRecordAPIRequestUsesRoutePattern(t *testing.T) {
	store := &auditRecorderStore{}
	recorder := newLogRecorder(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := &Server{logRecorder: recorder}
	request, err := http.NewRequestWithContext(
		t.Context(), http.MethodGet, "https://agentbox.test/api/resources/attacker-controlled", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Pattern = "GET /api/resources/{id}"
	server.recordAPIRequest(request, http.StatusNotFound, time.Millisecond)
	if err := recorder.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(store.logs) != 1 {
		t.Fatalf("logs = %#v", store.logs)
	}
	entry := store.logs[0]
	if entry.Detail["path"] != "/api/resources/{id}" || strings.Contains(entry.Message, "attacker-controlled") {
		t.Fatalf("access log stored raw request path: %#v", entry)
	}
}

func TestRecordAPIRequestSkipsUnauthenticatedPollingAndUnmatchedRoutes(t *testing.T) {
	store := &auditRecorderStore{}
	recorder := newLogRecorder(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := &Server{logRecorder: recorder}
	for _, pattern := range []string{"GET /api/auth/status", ""} {
		request := httptest.NewRequest(http.MethodGet, "https://agentbox.test/probe", nil)
		request.Pattern = pattern
		server.recordAPIRequest(request, http.StatusNotFound, time.Millisecond)
	}
	if err := recorder.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(store.logs) != 0 {
		t.Fatalf("public probes persisted logs: %#v", store.logs)
	}
}
