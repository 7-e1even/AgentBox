package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentbox/internal/catalog"
	"agentbox/internal/platform"
)

type networkProxyCheckStore struct {
	fakeStore
	proxyID  string
	serverID string
	target   string
}

func (s *networkProxyCheckStore) CreateNetworkProxyCheck(
	_ context.Context,
	proxyID, serverID, target string,
) (platform.NetworkProxyCheck, error) {
	s.proxyID = proxyID
	s.serverID = serverID
	s.target = target
	return platform.NetworkProxyCheck{
		ID: "754f76dd-2297-44e9-8204-a688be9be4a5", ProxyID: proxyID,
		ServerID: serverID, ServerName: "Worker One", Scope: "worker",
		Status: "pending", Target: target,
	}, nil
}

func TestCheckNetworkProxyEnqueuesSelectedWorker(t *testing.T) {
	t.Parallel()

	repository := &networkProxyCheckStore{}
	handler := New(
		repository,
		catalog.BuiltinCatalog,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
		Config{DisableAuth: true},
	)
	const serverID = "7b20f83b-6418-4a9f-8477-3dc7c35d6310"
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/network-proxies/proxy-one/check",
		strings.NewReader(`{"serverId":"`+serverID+`"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusAccepted, response.Body.String())
	}
	if repository.proxyID != "proxy-one" || repository.serverID != serverID || repository.target != networkProxyCheckTarget {
		t.Fatalf("enqueued check = proxy %q, server %q, target %q", repository.proxyID, repository.serverID, repository.target)
	}
	var body struct {
		Result platform.NetworkProxyCheck `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Result.Scope != "worker" || body.Result.ServerName != "Worker One" || body.Result.Status != "pending" {
		t.Fatalf("result = %#v", body.Result)
	}
}

func TestGetNetworkProxyCheckReturnsWorkerResult(t *testing.T) {
	t.Parallel()

	handler := debugTestHandler()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/network-proxies/proxy-one/checks/754f76dd-2297-44e9-8204-a688be9be4a5",
		nil,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"scope":"worker"`) ||
		!strings.Contains(response.Body.String(), `"status":"completed"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
}
