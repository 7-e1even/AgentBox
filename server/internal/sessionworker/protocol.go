package sessionworker

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

const (
	maxFileSize        = 512 * 1024
	maxUploadChunkSize = 192 * 1024
	maxUploadSize      = 50 * 1024 * 1024
	loginShellCommand  = "if [ -r /opt/agentbox/secrets/agentbox.env ]; then set -a; . /opt/agentbox/secrets/agentbox.env; set +a; fi; cd /root 2>/dev/null || cd /; if command -v bash >/dev/null 2>&1; then exec bash -l; else exec sh -l; fi"
)

type message struct {
	Type       string `json:"type"`
	SessionID  string `json:"sessionId,omitempty"`
	RequestID  string `json:"requestId,omitempty"`
	ExternalID string `json:"externalId,omitempty"`
	Driver     string `json:"driver,omitempty"`
	Data       string `json:"data,omitempty"`
	Operation  string `json:"operation,omitempty"`
	Path       string `json:"path,omitempty"`
	Content    string `json:"content,omitempty"`
	UploadID   string `json:"uploadId,omitempty"`
	Cols       int    `json:"cols,omitempty"`
	Rows       int    `json:"rows,omitempty"`
	OK         bool   `json:"ok,omitempty"`
	Result     any    `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
}

type workerConfig struct {
	serverURL  string
	serverID   string
	credential string
}

func parseConfig(content string) (workerConfig, error) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) < 3 {
		return workerConfig{}, errors.New("worker configuration is invalid")
	}
	config := workerConfig{
		serverURL:  strings.TrimRight(strings.TrimSpace(lines[0]), "/"),
		serverID:   strings.TrimSpace(lines[1]),
		credential: strings.TrimSpace(lines[2]),
	}
	if config.serverURL == "" || config.serverID == "" || config.credential == "" {
		return workerConfig{}, errors.New("worker configuration is invalid")
	}
	if !strings.HasPrefix(config.serverURL, "http://") && !strings.HasPrefix(config.serverURL, "https://") {
		return workerConfig{}, errors.New("worker server URL must use http or https")
	}
	return config, nil
}

func (c workerConfig) websocketURL() string {
	base := strings.TrimPrefix(c.serverURL, "http://")
	scheme := "ws://"
	if strings.HasPrefix(c.serverURL, "https://") {
		base = strings.TrimPrefix(c.serverURL, "https://")
		scheme = "wss://"
	}
	return scheme + base + "/api/servers/" + c.serverID + "/sessions/connect"
}

func validPath(value string) (string, error) {
	if value == "" || !strings.HasPrefix(value, "/") || strings.ContainsRune(value, 0) || len(value) > 4096 {
		return "", errors.New("path must be an absolute container path")
	}
	return path.Clean(value), nil
}

func uploadTempPath(finalPath, uploadID string) (string, error) {
	if len(uploadID) != 36 {
		return "", errors.New("invalid upload id")
	}
	for _, character := range strings.ToLower(uploadID) {
		if !strings.ContainsRune("0123456789abcdef-", character) {
			return "", errors.New("invalid upload id")
		}
	}
	return fmt.Sprintf("%s.agentbox-upload-%s", finalPath, uploadID), nil
}

func truncateError(err error) string {
	value := err.Error()
	if len(value) > 4000 {
		return value[:4000]
	}
	return value
}
