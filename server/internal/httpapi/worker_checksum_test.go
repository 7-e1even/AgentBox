package httpapi

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerBinaryServesChecksumHeaderFromBinaryDir(t *testing.T) {
	directory := t.TempDir()
	content := []byte("linux-worker-binary-with-checksum")
	binaryPath := filepath.Join(directory, "agentbox-worker-linux-amd64")
	if err := os.WriteFile(binaryPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	server := &Server{workerBinaryDir: directory, workerVersion: "dev"}
	request := httptest.NewRequest(http.MethodGet, "/api/worker/agentbox-worker?arch=amd64", nil)
	response := httptest.NewRecorder()
	server.workerBinary(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	expected := fmt.Sprintf("%x", sha256.Sum256(content))
	if got := response.Header().Get("X-Checksum-Sha256"); got != expected {
		t.Fatalf("X-Checksum-Sha256 = %q, want %q", got, expected)
	}
	// The computed checksum is persisted as a sidecar for subsequent requests.
	sidecar, err := os.ReadFile(binaryPath + ".sha256")
	if err != nil {
		t.Fatalf("read checksum sidecar: %v", err)
	}
	if strings.TrimSpace(string(sidecar)) != expected {
		t.Fatalf("sidecar = %q, want %q", strings.TrimSpace(string(sidecar)), expected)
	}
}

func TestWorkerBinaryRecomputesChecksumWhenSidecarIsGarbage(t *testing.T) {
	directory := t.TempDir()
	content := []byte("linux-worker-binary-garbage-sidecar")
	binaryPath := filepath.Join(directory, "agentbox-worker-linux-amd64")
	if err := os.WriteFile(binaryPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binaryPath+".sha256", []byte("not-a-checksum\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := &Server{workerBinaryDir: directory, workerVersion: "dev"}
	request := httptest.NewRequest(http.MethodGet, "/api/worker/agentbox-worker?arch=amd64", nil)
	response := httptest.NewRecorder()
	server.workerBinary(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	expected := fmt.Sprintf("%x", sha256.Sum256(content))
	if got := response.Header().Get("X-Checksum-Sha256"); got != expected {
		t.Fatalf("X-Checksum-Sha256 = %q, want %q", got, expected)
	}
}

func TestWorkerBinaryServesUpstreamVerifiedChecksumHeader(t *testing.T) {
	content := []byte("verified-linux-worker-checksum-header")
	checksum := fmt.Sprintf("%x", sha256.Sum256(content))
	release := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
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
	cacheDir := t.TempDir()
	server := &Server{
		workerVersion:    "v1.2.3",
		workerReleaseURL: release.URL,
		workerCacheDir:   cacheDir,
		workerHTTPClient: release.Client(),
	}
	for range 2 {
		request := httptest.NewRequest(http.MethodGet, "/api/worker/agentbox-worker?arch=amd64", nil)
		response := httptest.NewRecorder()
		server.workerBinary(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d", response.Code)
		}
		if got := response.Header().Get("X-Checksum-Sha256"); got != checksum {
			t.Fatalf("X-Checksum-Sha256 = %q, want the upstream-verified %q", got, checksum)
		}
	}
	sidecar, err := os.ReadFile(filepath.Join(cacheDir, "v1.2.3", "agentbox-worker-linux-amd64.sha256"))
	if err != nil {
		t.Fatalf("read checksum sidecar: %v", err)
	}
	if strings.TrimSpace(string(sidecar)) != checksum {
		t.Fatalf("sidecar = %q, want %q", strings.TrimSpace(string(sidecar)), checksum)
	}
}
