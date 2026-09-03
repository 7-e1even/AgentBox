package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentbox/internal/platform"
	"agentbox/internal/store"
)

func revocationTestTarget() sandboxSessionTarget {
	return sandboxSessionTarget{
		SandboxID: "sandbox-one", ServerID: sessionTestServerID,
		ExternalID: "container-one", Driver: "docker",
	}
}

func pendingClaimCount(hub *sessionHub) int {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	return len(hub.pendingClaims)
}

func beginRevocationTestIssue(t *testing.T, hub *sessionHub, authorization sessionAuthorization) *sandboxSessionReservation {
	t.Helper()
	reservation, err := hub.beginTicketIssue(authorization, revocationTestTarget().SandboxID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return reservation
}

func TestLogoutRevokesOnlyCurrentLoginTickets(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	target := revocationTestTarget()
	first, _, err := hub.issueTicket(target, "terminal", testSessionAuthorization(hub, "user-one", "login-one"))
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := hub.issueTicket(target, "terminal", testSessionAuthorization(hub, "user-one", "login-two"))
	if err != nil {
		t.Fatal(err)
	}
	hub.revokeSession("login-one")
	if _, ok := hub.consumeTicket(first, target.SandboxID, "terminal"); ok {
		t.Fatal("logout session ticket remained valid")
	}
	if _, ok := hub.consumeTicket(second, target.SandboxID, "terminal"); !ok {
		t.Fatal("another login for the same user was revoked")
	}
}

func TestUserRevocationInvalidatesEveryTicket(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	target := revocationTestTarget()
	first, _, _ := hub.issueTicket(target, "terminal", testSessionAuthorization(hub, "user-one", "login-one"))
	second, _, _ := hub.issueTicket(target, "desktop", testSessionAuthorization(hub, "user-one", "login-two"))
	hub.revokeUser("user-one")
	if _, ok := hub.consumeTicket(first, target.SandboxID, "terminal"); ok {
		t.Fatal("terminal ticket remained valid after user revocation")
	}
	if _, ok := hub.consumeTicket(second, target.SandboxID, "desktop"); ok {
		t.Fatal("desktop ticket remained valid after user revocation")
	}
}

func TestRevocationClosesOnlyBrowserRegistrationAndKeepsWorker(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	target := revocationTestTarget()
	hub.registerWorker(&workerSessionConnection{serverID: target.ServerID})
	token, _, _ := hub.issueTicket(target, "terminal", testSessionAuthorization(hub, "user-one", "login-one"))
	ticket, ok := hub.consumeTicket(token, target.SandboxID, "terminal")
	if !ok {
		t.Fatal("ticket was not consumed")
	}
	browser := &browserSessionConnection{id: ticket.AuditSessionID, serverID: target.ServerID, mode: ticket.Mode, ticket: ticket}
	if _, ok := hub.registerBrowser(browser); !ok {
		t.Fatal("browser registration failed")
	}
	if count := pendingClaimCount(hub); count != 0 {
		t.Fatalf("pending claims after registration = %d", count)
	}
	closed := hub.revokeSession("login-one")
	if len(closed) != 1 || closed[0] != browser {
		t.Fatalf("closed browsers = %#v", closed)
	}
	if !hub.hasWorker(target.ServerID) {
		t.Fatal("browser revocation disconnected the Worker")
	}
}

func TestRevocationWinsConsumeRegisterRace(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	target := revocationTestTarget()
	hub.registerWorker(&workerSessionConnection{serverID: target.ServerID})
	token, _, _ := hub.issueTicket(target, "terminal", testSessionAuthorization(hub, "user-one", "login-one"))
	ticket, ok := hub.consumeTicket(token, target.SandboxID, "terminal")
	if !ok {
		t.Fatal("ticket was not consumed")
	}
	hub.revokeSession("login-one")
	browser := &browserSessionConnection{id: ticket.AuditSessionID, serverID: target.ServerID, mode: ticket.Mode, ticket: ticket}
	if _, ok := hub.registerBrowser(browser); ok {
		t.Fatal("consumed ticket registered after its login was revoked")
	}
	if count := pendingClaimCount(hub); count != 0 {
		t.Fatalf("pending claims after revocation = %d", count)
	}
}

func TestFailedBrowserRegistrationConsumesPendingClaim(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	target := revocationTestTarget()
	token, _, _ := hub.issueTicket(target, "terminal", testSessionAuthorization(hub, "user-one", "login-one"))
	ticket, ok := hub.consumeTicket(token, target.SandboxID, "terminal")
	if !ok {
		t.Fatal("ticket was not consumed")
	}
	if count := pendingClaimCount(hub); count != 1 {
		t.Fatalf("pending claims before registration = %d", count)
	}
	browser := &browserSessionConnection{id: ticket.AuditSessionID, serverID: target.ServerID, mode: ticket.Mode, ticket: ticket}
	if _, ok := hub.registerBrowser(browser); ok {
		t.Fatal("browser registered without a Worker")
	}
	if count := pendingClaimCount(hub); count != 0 {
		t.Fatalf("pending claims after failed registration = %d", count)
	}
	assertSessionAdmissionReleased(t, hub)
}

func TestUserRevocationRemovesPendingClaim(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	target := revocationTestTarget()
	hub.registerWorker(&workerSessionConnection{serverID: target.ServerID})
	token, _, _ := hub.issueTicket(target, "terminal", testSessionAuthorization(hub, "user-one", "login-one"))
	ticket, ok := hub.consumeTicket(token, target.SandboxID, "terminal")
	if !ok {
		t.Fatal("ticket was not consumed")
	}
	hub.revokeUser("user-one")
	browser := &browserSessionConnection{id: ticket.AuditSessionID, serverID: target.ServerID, mode: ticket.Mode, ticket: ticket}
	if _, ok := hub.registerBrowser(browser); ok {
		t.Fatal("pending claim registered after user revocation")
	}
	if count := pendingClaimCount(hub); count != 0 {
		t.Fatalf("pending claims after user revocation = %d", count)
	}
}

func TestLogoutRevokesOnlyMatchingPendingClaim(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	target := revocationTestTarget()
	hub.registerWorker(&workerSessionConnection{serverID: target.ServerID})
	firstToken, _, _ := hub.issueTicket(target, "terminal", testSessionAuthorization(hub, "user-one", "login-one"))
	secondToken, _, _ := hub.issueTicket(target, "terminal", testSessionAuthorization(hub, "user-one", "login-two"))
	first, firstOK := hub.consumeTicket(firstToken, target.SandboxID, "terminal")
	second, secondOK := hub.consumeTicket(secondToken, target.SandboxID, "terminal")
	if !firstOK || !secondOK {
		t.Fatal("fresh tickets were not consumed")
	}

	hub.revokeSession("login-one")
	firstBrowser := &browserSessionConnection{id: first.AuditSessionID, serverID: target.ServerID, mode: first.Mode, ticket: first}
	if _, ok := hub.registerBrowser(firstBrowser); ok {
		t.Fatal("revoked pending claim registered")
	}
	secondBrowser := &browserSessionConnection{id: second.AuditSessionID, serverID: target.ServerID, mode: second.Mode, ticket: second}
	if _, ok := hub.registerBrowser(secondBrowser); !ok {
		t.Fatal("another login's pending claim was revoked")
	}
	if count := pendingClaimCount(hub); count != 0 {
		t.Fatalf("pending claims after matching revocation = %d", count)
	}
}

func TestSessionRevocationDoesNotRetainHistoricalLoginClaims(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	target := revocationTestTarget()
	hub.registerWorker(&workerSessionConnection{serverID: target.ServerID})
	startedAt := time.Now().UTC()

	for index := range 5000 {
		sessionKey := fmt.Sprintf("login-%d", index)
		authorization := testSessionAuthorization(hub, "user-one", sessionKey)
		reservation, err := hub.beginTicketIssue(authorization, target.SandboxID, startedAt.Add(time.Duration(index)*sandboxSessionIssueRateWindow))
		if err != nil {
			t.Fatalf("reserve ticket %d: %v", index, err)
		}
		token, _, err := hub.commitTicket(reservation, target, "terminal", authorization)
		if err != nil {
			hub.releaseTicketIssue(reservation)
			t.Fatalf("issue ticket %d: %v", index, err)
		}
		ticket, ok := hub.consumeTicket(token, target.SandboxID, "terminal")
		if !ok {
			t.Fatalf("consume ticket %d", index)
		}
		hub.revokeSession(sessionKey)
		browser := &browserSessionConnection{id: ticket.AuditSessionID, serverID: target.ServerID, mode: ticket.Mode, ticket: ticket}
		if _, ok := hub.registerBrowser(browser); ok {
			t.Fatalf("revoked ticket %d registered", index)
		}
	}

	hub.mu.RLock()
	pendingClaims := len(hub.pendingClaims)
	tickets := len(hub.tickets)
	sessions := len(hub.sessions)
	hub.mu.RUnlock()
	if pendingClaims != 0 || tickets != 0 || sessions != 0 {
		t.Fatalf("retained state after unique revocations: pending=%d tickets=%d sessions=%d", pendingClaims, tickets, sessions)
	}
}

func TestUserRevocationDoesNotRetainHistoricalUsers(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	target := revocationTestTarget()
	hub.registerWorker(&workerSessionConnection{serverID: target.ServerID})
	startedAt := time.Now().UTC()

	for index := range 5000 {
		userID := fmt.Sprintf("user-%d", index)
		authorization := testSessionAuthorization(hub, userID, "login")
		reservation, err := hub.beginTicketIssue(authorization, target.SandboxID, startedAt.Add(time.Duration(index)*sandboxSessionIssueRateWindow))
		if err != nil {
			t.Fatalf("reserve ticket %d: %v", index, err)
		}
		token, _, err := hub.commitTicket(reservation, target, "terminal", authorization)
		if err != nil {
			hub.releaseTicketIssue(reservation)
			t.Fatalf("issue ticket %d: %v", index, err)
		}
		ticket, ok := hub.consumeTicket(token, target.SandboxID, "terminal")
		if !ok {
			t.Fatalf("consume ticket %d", index)
		}
		hub.revokeUser(userID)
		browser := &browserSessionConnection{id: ticket.AuditSessionID, serverID: target.ServerID, mode: ticket.Mode, ticket: ticket}
		if _, ok := hub.registerBrowser(browser); ok {
			t.Fatalf("revoked user ticket %d registered", index)
		}
	}

	hub.mu.RLock()
	pendingClaims := len(hub.pendingClaims)
	tickets := len(hub.tickets)
	sessions := len(hub.sessions)
	hub.mu.RUnlock()
	if pendingClaims != 0 || tickets != 0 || sessions != 0 {
		t.Fatalf("retained state after unique user revocations: pending=%d tickets=%d sessions=%d", pendingClaims, tickets, sessions)
	}
}

func TestRevocationWinsAuthenticationIssueRace(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	authorization := testSessionAuthorization(hub, "user-one", "login-one")
	hub.revokeUser("user-one")
	if _, _, err := hub.issueTicket(revocationTestTarget(), "terminal", authorization); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("issue error = %v, want unauthorized", err)
	}
}

func TestUnrelatedLoginRevocationRevalidatesTicketIssue(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	server := &Server{store: sessionTestStore{}, sessions: hub}
	request := httptest.NewRequest(http.MethodPost, "/api/sandboxes/sandbox-one/session-ticket", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "login-two-token"})
	tokenHash, ok := sessionHash(request)
	if !ok {
		t.Fatal("session cookie was not hashed")
	}
	user := testAdmin()
	authorization := sessionAuthorization{
		userID: user.ID, sessionKey: sessionKey(tokenHash), role: user.Role,
		actor: platform.AuditActor{Type: "user", ID: user.ID, Name: user.Name, Role: user.Role},
		epoch: hub.authorizationEpoch(),
	}

	hub.revokeSession("another-login")
	reservation := beginRevocationTestIssue(t, hub, authorization)
	defer hub.releaseTicketIssue(reservation)
	token, _, err := server.issueSandboxSessionTicket(request, reservation, revocationTestTarget(), "terminal", authorization)
	if err != nil {
		t.Fatalf("unrelated revocation rejected valid login: %v", err)
	}
	ticket, ok := hub.consumeTicket(token, "sandbox-one", "terminal")
	if !ok {
		t.Fatal("revalidated ticket was not consumable")
	}
	hub.releaseTicketClaim(ticket)
}

type revokedSessionRevalidationStore struct{ sessionTestStore }

func (revokedSessionRevalidationStore) UserBySession(context.Context, []byte) (platform.User, error) {
	return platform.User{}, store.ErrUnauthorized
}

func TestRevokedLoginCannotRevalidateTicketIssue(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	server := &Server{store: revokedSessionRevalidationStore{}, sessions: hub}
	request := httptest.NewRequest(http.MethodPost, "/api/sandboxes/sandbox-one/session-ticket", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "revoked-login-token"})
	tokenHash, ok := sessionHash(request)
	if !ok {
		t.Fatal("session cookie was not hashed")
	}
	user := testAdmin()
	authorization := sessionAuthorization{
		userID: user.ID, sessionKey: sessionKey(tokenHash), role: user.Role,
		actor: platform.AuditActor{Type: "user", ID: user.ID, Name: user.Name, Role: user.Role},
		epoch: hub.authorizationEpoch(),
	}

	hub.revokeSession(authorization.sessionKey)
	reservation := beginRevocationTestIssue(t, hub, authorization)
	defer hub.releaseTicketIssue(reservation)
	if _, _, err := server.issueSandboxSessionTicket(request, reservation, revocationTestTarget(), "terminal", authorization); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("issue error = %v, want unauthorized", err)
	}
	if len(hub.tickets) != 0 || pendingClaimCount(hub) != 0 {
		t.Fatal("revoked login retained ticket state")
	}
}

func TestChangedRoleCannotRevalidateStaleTicketIssue(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	server := &Server{store: sessionTestStore{}, sessions: hub}
	request := httptest.NewRequest(http.MethodPost, "/api/sandboxes/sandbox-one/session-ticket", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "role-change-token"})
	tokenHash, ok := sessionHash(request)
	if !ok {
		t.Fatal("session cookie was not hashed")
	}
	user := testAdmin()
	authorization := sessionAuthorization{
		userID: user.ID, sessionKey: sessionKey(tokenHash), role: platform.UserRoleOperator,
		actor: platform.AuditActor{Type: "user", ID: user.ID, Name: user.Name, Role: platform.UserRoleOperator},
		epoch: hub.authorizationEpoch(),
	}

	hub.revokeUser(user.ID)
	reservation := beginRevocationTestIssue(t, hub, authorization)
	defer hub.releaseTicketIssue(reservation)
	if _, _, err := server.issueSandboxSessionTicket(request, reservation, revocationTestTarget(), "terminal", authorization); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("issue error = %v, want unauthorized", err)
	}
}

func TestDebugAuthorizationRevalidatesAfterUnrelatedRevocation(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	server := &Server{store: sessionTestStore{}, sessions: hub, disableAuth: true}
	request := httptest.NewRequest(http.MethodPost, "/api/sandboxes/sandbox-one/session-ticket", nil)
	user := testAdmin()
	authorization := sessionAuthorization{
		userID: user.ID, sessionKey: "debug:" + user.ID, role: user.Role,
		actor: platform.AuditActor{Type: "user", ID: user.ID, Name: user.Name, Role: user.Role},
		epoch: hub.authorizationEpoch(),
	}

	hub.revokeSession("another-login")
	reservation := beginRevocationTestIssue(t, hub, authorization)
	defer hub.releaseTicketIssue(reservation)
	token, _, err := server.issueSandboxSessionTicket(request, reservation, revocationTestTarget(), "terminal", authorization)
	if err != nil {
		t.Fatalf("unrelated revocation rejected debug authorization: %v", err)
	}
	ticket, ok := hub.consumeTicket(token, "sandbox-one", "terminal")
	if !ok {
		t.Fatal("revalidated debug ticket was not consumable")
	}
	hub.releaseTicketClaim(ticket)
}

func TestTicketBindsRoleAndFiniteSessionLifetime(t *testing.T) {
	hub := newSessionHub(nil, trustedProxySettings{})
	authorization := testSessionAuthorization(hub, "user-one", "login-one")
	authorization.role = platform.UserRoleAdmin
	token, _, err := hub.issueTicket(revocationTestTarget(), "terminal", authorization)
	if err != nil {
		t.Fatal(err)
	}
	ticket, ok := hub.consumeTicket(token, "sandbox-one", "terminal")
	if !ok {
		t.Fatal("ticket was not consumed")
	}
	if ticket.Role != platform.UserRoleAdmin || ticket.UserID != "user-one" || ticket.SessionKey != "login-one" {
		t.Fatalf("ticket identity = %#v", ticket)
	}
	remaining := time.Until(ticket.SessionExpiresAt)
	if remaining <= 0 || remaining > sandboxSessionMaxLifetime {
		t.Fatalf("session lifetime = %v", remaining)
	}
}
