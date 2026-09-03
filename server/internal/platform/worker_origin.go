package platform

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

// NormalizeWorkerOrigin accepts HTTPS origins and exact loopback HTTP origins.
// Worker credentials and executable downloads must never cross a non-loopback
// plaintext transport.
func NormalizeWorkerOrigin(value string) (string, error) {
	parsed, err := parseSecureWorkerURL(value)
	if err != nil {
		return "", err
	}
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("Worker URL must contain only an origin")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

// ValidateWorkerDownloadURL applies the Worker transport rule to a complete
// release URL, whose path is expected to identify a release repository.
func ValidateWorkerDownloadURL(value string) error {
	_, err := parseSecureWorkerURL(value)
	return err
}

func parseSecureWorkerURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" {
		return nil, errors.New("Worker URL is invalid")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("Worker URL must use HTTPS, or HTTP on an exact loopback host")
	}
	if parsed.Scheme == "http" && !isLoopbackWorkerHost(parsed.Hostname()) {
		return nil, errors.New("plain HTTP is allowed only for an exact localhost or loopback IP")
	}
	return parsed, nil
}

func isLoopbackWorkerHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
