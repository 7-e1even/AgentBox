package httpapi

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxWorkerBinarySize = 128 << 20

var workerVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func (s *Server) workerBinary(w http.ResponseWriter, request *http.Request) {
	arch := request.URL.Query().Get("arch")
	if arch != "amd64" && arch != "arm64" {
		http.Error(w, "unsupported Worker architecture", http.StatusBadRequest)
		return
	}
	version := strings.TrimSpace(request.URL.Query().Get("version"))
	if version == "" {
		version = s.workerVersion
	}
	if !workerVersionPattern.MatchString(version) {
		http.Error(w, "invalid Worker version", http.StatusBadRequest)
		return
	}
	if version != s.workerVersion {
		http.Error(w, "unsupported Worker version", http.StatusBadRequest)
		return
	}
	path, err := s.resolveWorkerBinary(request.Context(), version, arch)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("resolve Worker binary", "version", version, "arch", arch, "error", err)
		}
		http.Error(w, "Worker binary is unavailable", http.StatusServiceUnavailable)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		http.Error(w, "Worker binary is unavailable", http.StatusServiceUnavailable)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.Error(w, "Worker binary is unavailable", http.StatusServiceUnavailable)
		return
	}
	// Expose the SHA-256 of the exact bytes served so installers can verify the
	// last hop. The checksum is captured from the upstream-verified download and
	// persisted next to the cached asset; for older caches it is computed once
	// from the stored file and then reused.
	if checksum, err := s.workerServedChecksum(request.Context(), path); err == nil && checksum != "" {
		w.Header().Set("X-Checksum-Sha256", checksum)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="agentbox-worker"`)
	http.ServeContent(w, request, info.Name(), info.ModTime(), file)
}

// workerServedChecksum returns the lowercase hex SHA-256 of the served asset,
// reading the sidecar written when the asset was cached and computing (then
// persisting) it for assets cached before the sidecar existed.
func (s *Server) workerServedChecksum(ctx context.Context, path string) (string, error) {
	sidecar := path + ".sha256"
	if data, err := os.ReadFile(sidecar); err == nil {
		sum := strings.ToLower(strings.TrimSpace(string(data)))
		if len(sum) == sha256.Size*2 {
			if _, err := hex.DecodeString(sum); err == nil {
				return sum, nil
			}
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	sum := hex.EncodeToString(hasher.Sum(nil))
	if err := os.WriteFile(sidecar, []byte(sum+"\n"), 0o644); err != nil {
		if s.logger != nil {
			s.logger.WarnContext(ctx, "persist Worker checksum sidecar", "path", sidecar, "error", err)
		}
	}
	return sum, nil
}

func (s *Server) resolveWorkerBinary(ctx context.Context, version, arch string) (string, error) {
	asset := "agentbox-worker-linux-" + arch
	if s.workerBinaryDir != "" {
		path := filepath.Join(s.workerBinaryDir, asset)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	if s.workerCacheDir == "" || s.workerReleaseURL == "" || s.workerHTTPClient == nil {
		return "", errors.New("Worker release proxy is not configured")
	}

	s.workerBinaryMu.Lock()
	defer s.workerBinaryMu.Unlock()

	versionDir := filepath.Join(s.workerCacheDir, version)
	cachedPath := filepath.Join(versionDir, asset)
	if info, err := os.Stat(cachedPath); err == nil && info.Mode().IsRegular() {
		return cachedPath, nil
	}
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		return "", fmt.Errorf("create Worker cache: %w", err)
	}
	expected, err := s.fetchWorkerChecksum(ctx, version, asset)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.workerAssetURL(version, asset), nil)
	if err != nil {
		return "", fmt.Errorf("create Worker asset request: %w", err)
	}
	response, err := s.workerHTTPClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("download Worker asset: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download Worker asset: unexpected status %d", response.StatusCode)
	}
	temporary, err := os.CreateTemp(versionDir, ".agentbox-worker-*")
	if err != nil {
		return "", fmt.Errorf("create Worker cache file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hasher), io.LimitReader(response.Body, maxWorkerBinarySize+1))
	closeErr := temporary.Close()
	if copyErr != nil {
		return "", fmt.Errorf("cache Worker asset: %w", copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close Worker cache file: %w", closeErr)
	}
	if written > maxWorkerBinarySize {
		return "", errors.New("Worker asset exceeds size limit")
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return "", errors.New("Worker asset checksum mismatch")
	}
	if err := os.Chmod(temporaryPath, 0o755); err != nil {
		return "", fmt.Errorf("make Worker cache executable: %w", err)
	}
	if err := os.Rename(temporaryPath, cachedPath); err != nil {
		return "", fmt.Errorf("publish Worker cache file: %w", err)
	}
	if err := os.WriteFile(cachedPath+".sha256", []byte(actual+"\n"), 0o644); err != nil && s.logger != nil {
		s.logger.WarnContext(ctx, "persist Worker checksum sidecar", "path", cachedPath+".sha256", "error", err)
	}
	return cachedPath, nil
}

func (s *Server) fetchWorkerChecksum(ctx context.Context, version, asset string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.workerAssetURL(version, "SHA256SUMS"), nil)
	if err != nil {
		return "", fmt.Errorf("create Worker checksum request: %w", err)
	}
	response, err := s.workerHTTPClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("download Worker checksums: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download Worker checksums: unexpected status %d", response.StatusCode)
	}
	scanner := bufio.NewScanner(io.LimitReader(response.Body, 1<<20))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != asset {
			continue
		}
		if len(fields[0]) != sha256.Size*2 {
			return "", errors.New("invalid Worker checksum")
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return "", errors.New("invalid Worker checksum")
		}
		return strings.ToLower(fields[0]), nil
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read Worker checksums: %w", err)
	}
	return "", errors.New("Worker checksum is missing")
}

func (s *Server) workerAssetURL(version, asset string) string {
	return strings.TrimRight(s.workerReleaseURL, "/") + "/" + url.PathEscape(version) + "/" + url.PathEscape(asset)
}
