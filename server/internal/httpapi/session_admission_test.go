package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentbox/internal/platform"
	"agentbox/internal/store"
)

type sessionAdmissionProbeStore struct {
	sessionTestStore
	authorizeCalls atomic.Int64
	listCalls      atomic.Int64
	authorizeErr   error
}

func (s *sessionAdmissionProbeStore) AuthorizeSandboxCredentialAccess(context.Context, string) error {
	s.authorizeCalls.Add(1)
	return s.authorizeErr
}

func (s *sessionAdmissionProbeStore) ListResources(ctx context.Context) ([]platform.Resource, error) {
	s.listCalls.Add(1)
	return s.sessionTestStore.ListResources(ctx)
}

func newSessionAdmissionRequest(t *testing.T, authorization sessionAuthorization) *http.Request {
	t.Helper()
	user := platform.User{
		ID: authorization.userID, Name: authorization.actor.Name,
		Role: authorization.role, Status: platform.UserStatusActive,
	}
	ctx := withUserAuditContext(t.Context(), user)
	ctx = context.WithValue(ctx, sessionAuthorizationContextKey{}, authorization)
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/sandboxes/sandbox-one/session-ticket", nil)
	request.SetPathValue("id", "sandbox-one")
	return request
}

func newSessionAdmissionServer(hub *sessionHub, sessionStore PlatformStore) *Server {
	return &Server{
		store: sessionStore, sessions: hub,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func assertSessionAdmissionReleased(t *testing.T, hub *sessionHub) {
	t.Helper()
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	if hub.outstanding != 0 || hub.issuing != 0 || len(hub.reservations) != 0 ||
		len(hub.outstandingByUser) != 0 || len(hub.outstandingByLogin) != 0 ||
		len(hub.issuingByLogin) != 0 || len(hub.tickets) != 0 ||
		len(hub.pendingClaims) != 0 || len(hub.sessions) != 0 {
		t.Fatalf(
			"retained session admission state: outstanding=%d issuing=%d reservations=%d users=%d logins=%d issuingLogins=%d tickets=%d pending=%d active=%d",
			hub.outstanding, hub.issuing, len(hub.reservations), len(hub.outstandingByUser),
			len(hub.outstandingByLogin), len(hub.issuingByLogin), len(hub.tickets),
			len(hub.pendingClaims), len(hub.sessions),
		)
	}
}

func reserveSessionAdmissionAt(hub *sessionHub, authorization sessionAuthorization, now time.Time) (*sandboxSessionReservation, error) {
	return hub.beginTicketIssue(authorization, revocationTestTarget().SandboxID, now)
}

func TestSessionAdmissionRequiresAuthenticatedContextBeforeWork(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	probe := &sessionAdmissionProbeStore{}
	server := newSessionAdmissionServer(hub, probe)
	request := httptest.NewRequest(http.MethodPost, "/api/sandboxes/sandbox-one/session-ticket", nil)
	request.SetPathValue("id", "sandbox-one")
	response := httptest.NewRecorder()

	server.createSandboxSessionTicket(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if probe.authorizeCalls.Load() != 0 || probe.listCalls.Load() != 0 {
		t.Fatalf("unauthenticated request reached Store: authorize=%d list=%d", probe.authorizeCalls.Load(), probe.listCalls.Load())
	}
	assertSessionAdmissionReleased(t, hub)
}

func TestSessionAdmissionPrecedesStoreAndWorkerLookups(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	now := time.Now().UTC()
	reservations := make([]*sandboxSessionReservation, 0, sandboxSessionIssuingGlobalLimit)
	for index := range sandboxSessionIssuingGlobalLimit {
		authorization := testSessionAuthorization(hub, fmt.Sprintf("user-%d", index), fmt.Sprintf("login-%d", index))
		reservation, err := reserveSessionAdmissionAt(hub, authorization, now)
		if err != nil {
			t.Fatal(err)
		}
		reservations = append(reservations, reservation)
	}
	defer func() {
		for _, reservation := range reservations {
			hub.releaseTicketIssue(reservation)
		}
	}()

	probe := &sessionAdmissionProbeStore{}
	server := newSessionAdmissionServer(hub, probe)
	authorization := testSessionAuthorization(hub, "overflow-user", "overflow-login")
	response := httptest.NewRecorder()
	server.createSandboxSessionTicket(response, newSessionAdmissionRequest(t, authorization))

	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("status = %d Retry-After = %q", response.Code, response.Header().Get("Retry-After"))
	}
	if probe.authorizeCalls.Load() != 0 || probe.listCalls.Load() != 0 {
		t.Fatalf("rejected admission reached Store: authorize=%d list=%d", probe.authorizeCalls.Load(), probe.listCalls.Load())
	}
}

func TestSessionAdmissionRateLimitPrecedesStoreAndWorkerLookups(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	authorization := testSessionAuthorization(hub, "user-one", "login-one")
	now := time.Now().UTC()
	for range sandboxSessionIssueLoginRateLimit {
		reservation, err := reserveSessionAdmissionAt(hub, authorization, now)
		if err != nil {
			t.Fatal(err)
		}
		hub.releaseTicketIssue(reservation)
	}

	probe := &sessionAdmissionProbeStore{}
	server := newSessionAdmissionServer(hub, probe)
	response := httptest.NewRecorder()
	server.createSandboxSessionTicket(response, newSessionAdmissionRequest(t, authorization))

	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "60" {
		t.Fatalf("status = %d Retry-After = %q", response.Code, response.Header().Get("Retry-After"))
	}
	if probe.authorizeCalls.Load() != 0 || probe.listCalls.Load() != 0 {
		t.Fatalf("rate-limited request reached Store: authorize=%d list=%d", probe.authorizeCalls.Load(), probe.listCalls.Load())
	}
	assertSessionAdmissionReleased(t, hub)
}

func TestSessionAdmissionFailureReleasesIssuingReservation(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	probe := &sessionAdmissionProbeStore{authorizeErr: store.ErrForbidden}
	server := newSessionAdmissionServer(hub, probe)
	authorization := testSessionAuthorization(hub, "user-one", "login-one")
	response := httptest.NewRecorder()

	server.createSandboxSessionTicket(response, newSessionAdmissionRequest(t, authorization))

	if response.Code != http.StatusForbidden || probe.authorizeCalls.Load() != 1 || probe.listCalls.Load() != 0 {
		t.Fatalf("status=%d authorize=%d list=%d", response.Code, probe.authorizeCalls.Load(), probe.listCalls.Load())
	}
	assertSessionAdmissionReleased(t, hub)
}

func TestSessionAdmissionOutstandingCaps(t *testing.T) {
	target := revocationTestTarget()
	for _, test := range []struct {
		name  string
		limit int
		auth  func(*sessionHub, int) sessionAuthorization
	}{
		{
			name: "per login", limit: sandboxSessionOutstandingLoginLimit,
			auth: func(hub *sessionHub, _ int) sessionAuthorization {
				return testSessionAuthorization(hub, "user-one", "login-one")
			},
		},
		{
			name: "per user", limit: sandboxSessionOutstandingUserLimit,
			auth: func(hub *sessionHub, index int) sessionAuthorization {
				return testSessionAuthorization(hub, "user-one", fmt.Sprintf("login-%d", index))
			},
		},
		{
			name: "global", limit: sandboxSessionOutstandingGlobalLimit,
			auth: func(hub *sessionHub, index int) sessionAuthorization {
				return testSessionAuthorization(hub, fmt.Sprintf("user-%d", index), fmt.Sprintf("login-%d", index))
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			hub := newSessionHub(nil, trustedProxySettings{})
			for index := range test.limit {
				if _, _, err := hub.issueTicket(target, "terminal", test.auth(hub, index)); err != nil {
					t.Fatalf("issue %d: %v", index, err)
				}
			}
			if _, _, err := hub.issueTicket(target, "terminal", test.auth(hub, test.limit)); !errors.Is(err, errSandboxSessionCapacityLimited) {
				t.Fatalf("overflow error = %v, want capacity limit", err)
			}
			hub.mu.RLock()
			outstanding := hub.outstanding
			hub.mu.RUnlock()
			if outstanding != test.limit {
				t.Fatalf("outstanding = %d, want %d", outstanding, test.limit)
			}
			hub.revokeSandbox(target.SandboxID)
			assertSessionAdmissionReleased(t, hub)
		})
	}
}

func TestSessionAdmissionIssuingCapsAndIdempotentRelease(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct {
		name  string
		limit int
		auth  func(*sessionHub, int) sessionAuthorization
	}{
		{
			name: "per login", limit: sandboxSessionIssuingLoginLimit,
			auth: func(hub *sessionHub, _ int) sessionAuthorization {
				return testSessionAuthorization(hub, "user-one", "login-one")
			},
		},
		{
			name: "global", limit: sandboxSessionIssuingGlobalLimit,
			auth: func(hub *sessionHub, index int) sessionAuthorization {
				return testSessionAuthorization(hub, fmt.Sprintf("user-%d", index), fmt.Sprintf("login-%d", index))
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			hub := newSessionHub(nil, trustedProxySettings{})
			reservations := make([]*sandboxSessionReservation, 0, test.limit)
			for index := range test.limit {
				reservation, err := reserveSessionAdmissionAt(hub, test.auth(hub, index), now)
				if err != nil {
					t.Fatalf("reserve %d: %v", index, err)
				}
				reservations = append(reservations, reservation)
			}
			if _, err := reserveSessionAdmissionAt(hub, test.auth(hub, test.limit), now); !errors.Is(err, errSandboxSessionCapacityLimited) {
				t.Fatalf("overflow error = %v, want capacity limit", err)
			}
			for _, reservation := range reservations {
				hub.releaseTicketIssue(reservation)
				hub.releaseTicketIssue(reservation)
			}
			assertSessionAdmissionReleased(t, hub)
		})
	}
}

func TestSessionAdmissionRateLimits(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct {
		name  string
		limit int
		auth  func(*sessionHub, int) sessionAuthorization
	}{
		{
			name: "per login", limit: sandboxSessionIssueLoginRateLimit,
			auth: func(hub *sessionHub, _ int) sessionAuthorization {
				return testSessionAuthorization(hub, "user-one", "login-one")
			},
		},
		{
			name: "per user", limit: sandboxSessionIssueUserRateLimit,
			auth: func(hub *sessionHub, index int) sessionAuthorization {
				return testSessionAuthorization(hub, "user-one", fmt.Sprintf("login-%d", index/sandboxSessionIssueLoginRateLimit))
			},
		},
		{
			name: "global", limit: sandboxSessionIssueGlobalRateLimit,
			auth: func(hub *sessionHub, index int) sessionAuthorization {
				userIndex := index / sandboxSessionIssueUserRateLimit
				loginIndex := index / sandboxSessionIssueLoginRateLimit
				return testSessionAuthorization(hub, fmt.Sprintf("user-%d", userIndex), fmt.Sprintf("login-%d", loginIndex))
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			hub := newSessionHub(nil, trustedProxySettings{})
			for index := range test.limit {
				reservation, err := reserveSessionAdmissionAt(hub, test.auth(hub, index), now)
				if err != nil {
					t.Fatalf("attempt %d: %v", index, err)
				}
				hub.releaseTicketIssue(reservation)
			}
			if _, err := reserveSessionAdmissionAt(hub, test.auth(hub, test.limit), now); !errors.Is(err, errSandboxSessionRateLimited) {
				t.Fatalf("overflow error = %v, want rate limit", err)
			}
			reservation, err := reserveSessionAdmissionAt(hub, test.auth(hub, test.limit), now.Add(sandboxSessionIssueRateWindow))
			if err != nil {
				t.Fatalf("window boundary remained limited: %v", err)
			}
			hub.releaseTicketIssue(reservation)
			assertSessionAdmissionReleased(t, hub)
		})
	}
}

func TestSessionRateRejectedLoginCannotExhaustBroaderBuckets(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	now := time.Now().UTC()
	limited := testSessionAuthorization(hub, "limited-user", "limited-login")
	for range sandboxSessionIssueLoginRateLimit {
		reservation, err := reserveSessionAdmissionAt(hub, limited, now)
		if err != nil {
			t.Fatal(err)
		}
		hub.releaseTicketIssue(reservation)
	}
	for index := range 1000 {
		if _, err := reserveSessionAdmissionAt(hub, limited, now); !errors.Is(err, errSandboxSessionRateLimited) {
			t.Fatalf("rejected attempt %d error = %v", index, err)
		}
	}

	hub.mu.RLock()
	globalAttempts := len(hub.issueRate.global)
	userAttempts := len(hub.issueRate.byUser[limited.userID])
	hub.mu.RUnlock()
	if globalAttempts != sandboxSessionIssueLoginRateLimit || userAttempts != sandboxSessionIssueLoginRateLimit {
		t.Fatalf("rejected login consumed broader rate buckets: global=%d user=%d", globalAttempts, userAttempts)
	}

	other := testSessionAuthorization(hub, "other-user", "other-login")
	reservation, err := reserveSessionAdmissionAt(hub, other, now)
	if err != nil {
		t.Fatalf("another user was starved by rejected login attempts: %v", err)
	}
	hub.releaseTicketIssue(reservation)
	assertSessionAdmissionReleased(t, hub)
}

func TestSessionReservationLifecycleAndSandboxRevocation(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	target := revocationTestTarget()
	worker := &workerSessionConnection{serverID: target.ServerID}
	hub.registerWorker(worker)
	authorizations := make([]sessionAuthorization, 4)
	reservations := make([]*sandboxSessionReservation, 4)
	for index := range reservations {
		authorizations[index] = testSessionAuthorization(hub, "user-one", fmt.Sprintf("login-%d", index))
		var err error
		reservations[index], err = reserveSessionAdmissionAt(hub, authorizations[index], time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
	}

	if _, _, err := hub.commitTicket(reservations[1], target, "terminal", authorizations[1]); err != nil {
		t.Fatal(err)
	}
	pendingToken, _, err := hub.commitTicket(reservations[2], target, "terminal", authorizations[2])
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := hub.consumeTicket(pendingToken, target.SandboxID, "terminal"); !ok {
		t.Fatal("ticket did not enter pending state")
	}
	activeToken, _, err := hub.commitTicket(reservations[3], target, "terminal", authorizations[3])
	if err != nil {
		t.Fatal(err)
	}
	activeTicket, ok := hub.consumeTicket(activeToken, target.SandboxID, "terminal")
	if !ok {
		t.Fatal("ticket for active state was rejected")
	}
	browser := &browserSessionConnection{
		id: activeTicket.AuditSessionID, serverID: target.ServerID,
		mode: activeTicket.Mode, ticket: activeTicket,
	}
	if _, ok := hub.registerBrowser(browser); !ok {
		t.Fatal("browser did not enter active state")
	}

	wantStates := []sandboxSessionState{
		sandboxSessionStateIssuing, sandboxSessionStateTicket,
		sandboxSessionStatePending, sandboxSessionStateActive,
	}
	for index, want := range wantStates {
		if reservations[index].state != want {
			t.Fatalf("reservation %d state = %q, want %q", index, reservations[index].state, want)
		}
	}
	epoch := hub.authorizationEpoch()
	closed := hub.revokeSandbox(target.SandboxID)
	if len(closed) != 1 || closed[0] != browser {
		t.Fatalf("sandbox revocation closed = %#v", closed)
	}
	if hub.authorizationEpoch() != epoch {
		t.Fatal("sandbox revocation changed the authentication epoch")
	}
	for index, reservation := range reservations {
		if reservation.state != sandboxSessionStateDone {
			t.Fatalf("reservation %d state = %q, want done", index, reservation.state)
		}
	}
	assertSessionAdmissionReleased(t, hub)
}

func TestWorkerDisconnectAndBrowserCleanupAreIdempotent(t *testing.T) {
	for _, test := range []struct {
		name       string
		disconnect func(*sessionHub, *workerSessionConnection, *browserSessionConnection)
	}{
		{
			name: "browser cleanup",
			disconnect: func(hub *sessionHub, _ *workerSessionConnection, browser *browserSessionConnection) {
				hub.unregisterBrowser(browser)
				hub.unregisterBrowser(browser)
			},
		},
		{
			name: "worker disconnect",
			disconnect: func(hub *sessionHub, worker *workerSessionConnection, _ *browserSessionConnection) {
				hub.unregisterWorker(worker)
				hub.unregisterWorker(worker)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			hub := newSessionHub(nil, trustedProxySettings{})
			target := revocationTestTarget()
			worker := &workerSessionConnection{serverID: target.ServerID}
			hub.registerWorker(worker)
			token, _, err := hub.issueTicket(target, "terminal", testSessionAuthorization(hub, "user-one", "login-one"))
			if err != nil {
				t.Fatal(err)
			}
			ticket, ok := hub.consumeTicket(token, target.SandboxID, "terminal")
			if !ok {
				t.Fatal("ticket was not consumed")
			}
			browser := &browserSessionConnection{
				id: ticket.AuditSessionID, serverID: target.ServerID, mode: ticket.Mode, ticket: ticket,
			}
			if _, ok := hub.registerBrowser(browser); !ok {
				t.Fatal("browser was not registered")
			}
			test.disconnect(hub, worker, browser)
			if ticket.reservation.state != sandboxSessionStateDone {
				t.Fatalf("state = %q, want done", ticket.reservation.state)
			}
			assertSessionAdmissionReleased(t, hub)
		})
	}
}

func TestExpiredTicketCleanupIsBoundedByOutstandingCap(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	target := revocationTestTarget()
	for index := range sandboxSessionOutstandingGlobalLimit {
		authorization := testSessionAuthorization(hub, fmt.Sprintf("user-%d", index), fmt.Sprintf("login-%d", index))
		if _, _, err := hub.issueTicket(target, "terminal", authorization); err != nil {
			t.Fatalf("issue %d: %v", index, err)
		}
	}
	hub.mu.Lock()
	if len(hub.tickets) != sandboxSessionOutstandingGlobalLimit {
		t.Fatalf("tickets = %d, want %d", len(hub.tickets), sandboxSessionOutstandingGlobalLimit)
	}
	expiredAt := time.Now().UTC().Add(-time.Second)
	for token, ticket := range hub.tickets {
		ticket.ExpiresAt = expiredAt
		hub.tickets[token] = ticket
	}
	hub.mu.Unlock()

	authorization := testSessionAuthorization(hub, "fresh-user", "fresh-login")
	reservation, err := reserveSessionAdmissionAt(hub, authorization, time.Now().UTC())
	if err != nil {
		t.Fatalf("expired tickets retained capacity: %v", err)
	}
	hub.mu.RLock()
	outstanding, tickets := hub.outstanding, len(hub.tickets)
	hub.mu.RUnlock()
	if outstanding != 1 || tickets != 0 {
		t.Fatalf("after expiration cleanup: outstanding=%d tickets=%d", outstanding, tickets)
	}
	hub.releaseTicketIssue(reservation)
	assertSessionAdmissionReleased(t, hub)
}

func TestSessionAdmissionTenThousandUniqueIdentitiesRemainBounded(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	startedAt := time.Now().UTC()
	for index := range 10_000 {
		authorization := testSessionAuthorization(hub, fmt.Sprintf("user-%d", index), fmt.Sprintf("login-%d", index))
		reservation, err := reserveSessionAdmissionAt(hub, authorization, startedAt.Add(time.Duration(index)*sandboxSessionIssueRateWindow))
		if err != nil {
			t.Fatalf("attempt %d: %v", index, err)
		}
		hub.releaseTicketIssue(reservation)
	}
	assertSessionAdmissionReleased(t, hub)
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	if len(hub.issueRate.global) > 1 || len(hub.issueRate.byUser) > 1 || len(hub.issueRate.byLogin) > 1 {
		t.Fatalf(
			"rate history retained old identities: global=%d users=%d logins=%d",
			len(hub.issueRate.global), len(hub.issueRate.byUser), len(hub.issueRate.byLogin),
		)
	}
}

func TestConcurrentSessionAdmissionNeverExceedsIssuingCap(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	authorization := testSessionAuthorization(hub, "user-one", "login-one")
	now := time.Now().UTC()
	type result struct {
		reservation *sandboxSessionReservation
		err         error
	}
	results := make(chan result, 64)
	release := make(chan struct{})
	var workers sync.WaitGroup
	for range 64 {
		workers.Go(func() {
			reservation, err := reserveSessionAdmissionAt(hub, authorization, now)
			results <- result{reservation: reservation, err: err}
			if reservation != nil {
				<-release
				hub.releaseTicketIssue(reservation)
			}
		})
	}

	admitted := 0
	for range 64 {
		result := <-results
		switch {
		case result.err == nil:
			admitted++
		case errors.Is(result.err, errSandboxSessionCapacityLimited), errors.Is(result.err, errSandboxSessionRateLimited):
		default:
			t.Fatalf("unexpected admission error: %v", result.err)
		}
	}
	if admitted != sandboxSessionIssuingLoginLimit {
		t.Fatalf("admitted = %d, want %d", admitted, sandboxSessionIssuingLoginLimit)
	}
	close(release)
	workers.Wait()
	assertSessionAdmissionReleased(t, hub)
}

func TestRevocationPreventsIssuingReservationCommit(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	authorization := testSessionAuthorization(hub, "user-one", "login-one")
	reservation, err := reserveSessionAdmissionAt(hub, authorization, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	hub.revokeUser(authorization.userID)
	if _, _, err := hub.commitTicket(reservation, revocationTestTarget(), "terminal", authorization); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("commit error = %v, want unauthorized", err)
	}
	if reservation.state != sandboxSessionStateDone {
		t.Fatalf("state = %q, want done", reservation.state)
	}
	assertSessionAdmissionReleased(t, hub)
}
