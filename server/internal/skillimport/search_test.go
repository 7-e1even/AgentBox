package skillimport

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"agentbox/internal/platform"
)

func TestSearchUsesPublicCatalogAndOnlyReturnsImportableSources(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "skills.sh" || request.URL.Path != "/api/search" || request.URL.Query().Get("q") != "react & design" || request.URL.Query().Get("limit") != "20" {
			t.Fatalf("unexpected search target: %s", request.URL)
		}
		if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			t.Fatal("public catalog search must not forward credentials")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"skills":[
			{"id":"owner/repo/react:components","name":"React components","source":"owner/repo","installs":42},
			{"id":"owner/repo/react:components","name":"Duplicate","source":"owner/repo","installs":42},
			{"id":"example.com/design","name":"Well-known source","source":"example.com","installs":200},
			{"id":"other/repo/skill","name":"Mismatched source","source":"owner/repo","installs":50},
			{"id":"owner/repo/../../private","name":"Unsafe path","source":"owner/repo","installs":50}
		]}`)), Request: request}, nil
	})}
	result, err := search(t.Context(), client, "  react & design  ")
	if err != nil || result.Query != "react & design" || len(result.Skills) != 1 || result.Excluded != 3 {
		t.Fatalf("search result = %#v, %v", result, err)
	}
	skill := result.Skills[0]
	if skill.Installs != 42 || skill.URL != "https://skills.sh/owner/repo/react:components" {
		t.Fatalf("catalog metadata lost: %#v", skill)
	}
	link, _ := url.Parse(skill.URL)
	if _, err := skillsSHPathParts(link); err != nil {
		t.Fatalf("search returned a link the importer rejects: %v", err)
	}
}

func TestSearchRejectsBadQueriesBeforeNetwork(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid query reached the upstream")
		return nil, nil
	})}
	for _, query := range []string{"", "r", "   ", "react\x00", strings.Repeat("界", 101)} {
		if _, err := search(t.Context(), client, query); !platform.IsValidationError(err) {
			t.Errorf("invalid query %q accepted: %v", query, err)
		}
	}
}

func TestSearchDoesNotTurnUpstreamFailureIntoEmptyResults(t *testing.T) {
	for _, body := range []string{`{}`, `{"skills":null}`, `<html>Unavailable</html>`, strings.Repeat("x", (1<<20)+1)} {
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		})}
		if _, err := search(t.Context(), client, "react"); err == nil || platform.IsValidationError(err) {
			t.Fatalf("upstream failure must be distinct from an empty result or bad query: %v", err)
		}
	}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusTooManyRequests, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
	})}
	if _, err := search(t.Context(), client, "react"); err == nil || platform.IsValidationError(err) {
		t.Fatalf("rate limit was not reported as upstream failure: %v", err)
	}
	client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"skills":[]}`)), Request: request}, nil
	})
	result, err := search(t.Context(), client, "no matches")
	if err != nil || result.Skills == nil || len(result.Skills) != 0 {
		t.Fatalf("valid empty search = %#v, %v", result, err)
	}
}
