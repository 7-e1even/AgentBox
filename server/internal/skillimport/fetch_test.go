package skillimport

import (
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"agentbox/internal/platform"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestSourceURLsRejectLocalAccessAndNormalizeGitHubFiles(t *testing.T) {
	link, err := sourceURL("https://github.com/owner/repo/blob/main/skills/sample/SKILL.md?plain=1")
	if err != nil || link.String() != "https://raw.githubusercontent.com/owner/repo/main/skills/sample/SKILL.md" {
		t.Fatalf("GitHub source = %v, %v", link, err)
	}
	for _, source := range []string{
		"http://example.com/SKILL.md", "file:///etc/passwd", "https://localhost/SKILL.md",
		"https://127.0.0.1/SKILL.md", "https://[::1]/SKILL.md", "https://[::ffff:127.0.0.1]/SKILL.md",
		"https://169.254.169.254/latest/meta-data", "https://10.0.0.1/SKILL.md",
		"https://user:password@example.com/SKILL.md", "https://example.com:8443/SKILL.md",
		"https://github.com/owner/repo/tree/main/skills/sample",
	} {
		if _, err := sourceURL(source); !platform.IsValidationError(err) {
			t.Errorf("unsafe/unsupported source %q accepted: %v", source, err)
		}
	}
	if _, err := dialPublic(t.Context(), "tcp", "localhost:443"); err == nil {
		t.Fatal("DNS lookup to loopback must not be dialed")
	}
}

func TestPublicAddressChecksIPv4AndIPv6(t *testing.T) {
	for _, value := range []string{
		"0.0.0.0", "127.1.2.3", "192.168.1.1", "172.16.0.1", "100.64.0.1", "198.18.0.1",
		"192.0.2.1", "169.254.1.1", "224.0.0.1", "240.1.2.3", "::", "::1", "fc00::1",
		"fe80::1", "::ffff:10.0.0.1", "2001:db8::1", "64:ff9b::7f00:1", "2002:7f00:1::",
	} {
		if publicAddress(netip.MustParseAddr(value)) {
			t.Errorf("non-public address %s accepted", value)
		}
	}
	for _, value := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !publicAddress(netip.MustParseAddr(value)) {
			t.Errorf("public address %s rejected", value)
		}
	}
}

func TestRedirectsCannotBypassSourcePolicy(t *testing.T) {
	for _, destination := range []string{"https://127.0.0.1/SKILL.md", "http://example.com/SKILL.md", "https://example.com:8080/SKILL.md"} {
		client := newHTTPClient()
		calls := 0
		client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{
				StatusCode: http.StatusFound, Header: http.Header{"Location": {destination}},
				Body: io.NopCloser(strings.NewReader("")), Request: request,
			}, nil
		})
		link, _ := url.Parse("https://example.com/SKILL.md")
		if _, err := fetch(t.Context(), client, link, link.String()); err == nil || calls != 1 {
			t.Fatalf("redirect %s was followed: calls=%d err=%v", destination, calls, err)
		}
	}
}

func TestFakeIPProxyExceptionOnlyAllowsFixedCatalogOrigins(t *testing.T) {
	for _, host := range []string{"skills.sh", "www.skills.sh", "github.com", "raw.githubusercontent.com", "codeload.github.com"} {
		if !allowedSourceAddress(host, netip.MustParseAddr("198.18.0.217")) {
			t.Errorf("GitHub origin %s cannot use a Fake-IP DNS proxy", host)
		}
		for _, address := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "::1"} {
			if allowedSourceAddress(host, netip.MustParseAddr(address)) {
				t.Errorf("GitHub origin %s allowed private address %s", host, address)
			}
		}
	}
	for _, host := range []string{"example.com", "skills.sh.example.com", "example.skills.sh", "github.com.example.com", "example.github.com", "198.18.0.217"} {
		if allowedSourceAddress(host, netip.MustParseAddr("198.18.0.217")) {
			t.Errorf("untrusted source %s allowed a Fake-IP address", host)
		}
	}
	if _, err := sourceURL("https://198.18.0.217/SKILL.md"); err == nil {
		t.Fatal("literal Fake-IP URL must remain blocked")
	}
}

func TestFetchParsesPublicContentAndBoundsChunkedResponses(t *testing.T) {
	link, _ := url.Parse("https://example.com/SKILL.md")
	for _, body := range []string{testDocument, strings.Repeat("x", platform.MaxSkillBundleBytes+1)} {
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
				t.Fatal("fetch must not forward authentication")
			}
			return &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/plain"}},
				ContentLength: -1, Body: io.NopCloser(strings.NewReader(body)), Request: request,
			}, nil
		})}
		draft, err := fetch(t.Context(), client, link, link.String())
		if body == testDocument {
			if err != nil || draft.Spec.Source != "url" || draft.Spec.Path != link.String() || draft.Spec.Instructions != testDocument {
				t.Fatalf("bad link import: %#v, %v", draft, err)
			}
		} else if !platform.IsValidationError(err) {
			t.Fatalf("oversized chunked response accepted: %v", err)
		}
	}
}
