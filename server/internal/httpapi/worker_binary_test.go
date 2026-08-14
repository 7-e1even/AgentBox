package httpapi

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

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
