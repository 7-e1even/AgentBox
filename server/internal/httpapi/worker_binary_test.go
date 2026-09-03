package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkerDownloadRedirectRejectsHTTPSDowngrade(t *testing.T) {
	httpsRequest := httptest.NewRequest(http.MethodGet, "https://releases.example/worker", nil)
	for _, target := range []string{
		"http://localhost/worker",
		"http://127.0.0.1/worker",
		"http://[::1]/worker",
	} {
		redirect := httptest.NewRequest(http.MethodGet, target, nil)
		if err := validateWorkerDownloadRedirect(redirect, []*http.Request{httpsRequest}); err == nil {
			t.Errorf("validateWorkerDownloadRedirect() allowed HTTPS downgrade to %q", target)
		}
	}
}

func TestWorkerDownloadRedirectPreservesSecureAndLoopbackFlows(t *testing.T) {
	tests := []struct {
		from string
		to   string
	}{
		{from: "https://releases.example/worker", to: "https://cdn.example/worker"},
		{from: "http://localhost/worker", to: "http://127.0.0.1/worker"},
		{from: "http://localhost/worker", to: "https://releases.example/worker"},
	}
	for _, test := range tests {
		previous := httptest.NewRequest(http.MethodGet, test.from, nil)
		redirect := httptest.NewRequest(http.MethodGet, test.to, nil)
		if err := validateWorkerDownloadRedirect(redirect, []*http.Request{previous}); err != nil {
			t.Errorf("validateWorkerDownloadRedirect(%q -> %q) error = %v", test.from, test.to, err)
		}
	}
}

func TestWorkerDownloadRedirectStopsAfterTenHops(t *testing.T) {
	redirect := httptest.NewRequest(http.MethodGet, "https://cdn.example/worker", nil)
	via := make([]*http.Request, 10)
	for index := range via {
		via[index] = httptest.NewRequest(http.MethodGet, "https://releases.example/worker", nil)
	}
	if err := validateWorkerDownloadRedirect(redirect, via); err == nil {
		t.Fatal("validateWorkerDownloadRedirect() allowed more than 10 redirects")
	}
}

func TestWorkerBinaryServesRequestedArchitecture(t *testing.T) {
	directory := t.TempDir()
	content := []byte("linux-worker-binary")
	if err := os.WriteFile(filepath.Join(directory, "agentbox-worker-linux-amd64"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	server := &Server{workerBinaryDir: directory, workerVersion: "dev"}
	request := httptest.NewRequest(http.MethodGet, "/api/worker/agentbox-worker?arch=amd64", nil)
	response := httptest.NewRecorder()
	server.workerBinary(response, request)
	if response.Code != http.StatusOK || response.Body.String() != string(content) {
		t.Fatalf("status = %d body = %q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("content type = %q", got)
	}
}

func TestWorkerBinaryDownloadsVerifiesAndCachesReleaseAsset(t *testing.T) {
	content := []byte("verified-linux-worker")
	checksum := fmt.Sprintf("%x", sha256.Sum256(content))
	requests := 0
	release := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests++
		switch request.URL.Path {
		case "/v1.2.3/SHA256SUMS":
			_, _ = fmt.Fprintf(w, "%s  agentbox-worker-linux-amd64\n", checksum)
		case "/v1.2.3/agentbox-worker-linux-amd64":
			_, _ = w.Write(content)
		default:
			http.NotFound(w, request)
		}
	}))
	defer release.Close()
	server := &Server{
		workerVersion:    "v1.2.3",
		workerReleaseURL: release.URL,
		workerCacheDir:   t.TempDir(),
		workerHTTPClient: release.Client(),
	}
	for range 2 {
		request := httptest.NewRequest(http.MethodGet, "/api/worker/agentbox-worker?arch=amd64", nil)
		response := httptest.NewRecorder()
		server.workerBinary(response, request)
		if response.Code != http.StatusOK || response.Body.String() != string(content) {
			t.Fatalf("status = %d body = %q", response.Code, response.Body.String())
		}
	}
	if requests != 2 {
		t.Fatalf("release requests = %d, want 2 before cache reuse", requests)
	}
}

func TestWorkerBinaryRejectsReleaseChecksumMismatch(t *testing.T) {
	release := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1.2.3/SHA256SUMS" {
			_, _ = fmt.Fprintln(w, "0000000000000000000000000000000000000000000000000000000000000000  agentbox-worker-linux-amd64")
			return
		}
		_, _ = w.Write([]byte("tampered"))
	}))
	defer release.Close()
	server := &Server{
		workerVersion:    "v1.2.3",
		workerReleaseURL: release.URL,
		workerCacheDir:   t.TempDir(),
		workerHTTPClient: release.Client(),
	}
	request := httptest.NewRequest(http.MethodGet, "/api/worker/agentbox-worker?arch=amd64", nil)
	response := httptest.NewRecorder()
	server.workerBinary(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestWorkerBinaryRejectsUnknownArchitecture(t *testing.T) {
	server := &Server{workerBinaryDir: t.TempDir()}
	request := httptest.NewRequest(http.MethodGet, "/api/worker/agentbox-worker?arch=386", nil)
	response := httptest.NewRecorder()
	server.workerBinary(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if body.Error.Code != "invalid_request" || body.Error.Message != "unsupported Worker architecture" || body.Error.Retryable {
		t.Fatalf("error = %+v", body.Error)
	}
}

func TestWorkerBinaryRejectsVersionOutsideCurrentServerRelease(t *testing.T) {
	server := &Server{workerVersion: "v1.2.3", workerBinaryDir: t.TempDir()}
	request := httptest.NewRequest(http.MethodGet, "/api/worker/agentbox-worker?arch=amd64&version=v1.2.2", nil)
	response := httptest.NewRecorder()
	server.workerBinary(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}
