package skillimport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type SearchSkill struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Source   string `json:"source"`
	URL      string `json:"url"`
	Installs int64  `json:"installs"`
}

type SearchResult struct {
	Query    string        `json:"query"`
	Skills   []SearchSkill `json:"skills"`
	Excluded int           `json:"excluded"`
}

func Search(ctx context.Context, query string) (SearchResult, error) {
	client := newHTTPClient()
	client.Timeout = 15 * time.Second
	defer client.CloseIdleConnections()
	return search(ctx, client, query)
}

func search(ctx context.Context, client *http.Client, query string) (SearchResult, error) {
	query = strings.TrimSpace(query)
	if !utf8.ValidString(query) || utf8.RuneCountInString(query) < 2 || utf8.RuneCountInString(query) > 100 || strings.IndexFunc(query, unicode.IsControl) >= 0 {
		return SearchResult{}, invalid("请输入 2–100 个字符的搜索关键词")
	}
	// This public endpoint is also used by vercel-labs/skills src/find.ts.
	// The upstream origin and result limit are fixed, never supplied by clients.
	link := &url.URL{Scheme: "https", Host: "skills.sh", Path: "/api/search", RawQuery: url.Values{"q": {query}, "limit": {"20"}}.Encode()}
	data, _, err := download(ctx, client, link, 1<<20)
	if err != nil {
		return SearchResult{}, fmt.Errorf("skills.sh search: %v", err)
	}
	var response struct {
		Skills *[]struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Source   string `json:"source"`
			Installs int64  `json:"installs"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(data, &response); err != nil || response.Skills == nil {
		return SearchResult{}, fmt.Errorf("skills.sh returned an invalid search response")
	}
	result := SearchResult{Query: query, Skills: make([]SearchSkill, 0)}
	seen := make(map[string]bool)
	for _, skill := range *response.Skills {
		page := &url.URL{Scheme: "https", Host: "skills.sh", Path: "/" + skill.ID}
		parts, err := skillsSHPathParts(page)
		if err != nil || strings.Join(parts[:2], "/") != skill.Source {
			result.Excluded++
			continue
		}
		name := strings.TrimSpace(skill.Name)
		if name == "" || utf8.RuneCountInString(name) > 160 || strings.IndexFunc(name, unicode.IsControl) >= 0 || seen[skill.ID] {
			continue
		}
		seen[skill.ID] = true
		result.Skills = append(result.Skills, SearchSkill{
			ID: skill.ID, Name: name, Source: skill.Source, URL: page.String(), Installs: max(skill.Installs, 0),
		})
		if len(result.Skills) == 20 {
			break
		}
	}
	return result, nil
}
