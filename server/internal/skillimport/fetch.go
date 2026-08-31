package skillimport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strings"
	"time"

	"agentbox/internal/platform"
)

func Fetch(ctx context.Context, source string) (Draft, error) {
	link, err := sourceURL(source)
	if err != nil {
		return Draft{}, err
	}
	client := newHTTPClient()
	defer client.CloseIdleConnections()
	if strings.EqualFold(link.Hostname(), "skills.sh") || strings.EqualFold(link.Hostname(), "www.skills.sh") {
		return fetchSkillsSH(ctx, client, link)
	}
	return fetch(ctx, client, link, source)
}

func newHTTPClient() *http.Client {
	transport := &http.Transport{
		// Do not delegate DNS or destination checks to an environment proxy.
		DialContext:           dialPublic,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return invalid("来源链接重定向过多")
			}
			return validateURL(request.URL)
		},
	}
}

func sourceURL(source string) (*url.URL, error) {
	link, err := url.Parse(strings.TrimSpace(source))
	if err != nil || len(source) > 4096 {
		return nil, invalid("请填写有效的公开 HTTPS 文件链接")
	}
	if err := validateURL(link); err != nil {
		return nil, err
	}
	if strings.EqualFold(link.Hostname(), "github.com") {
		parts := strings.Split(strings.Trim(link.Path, "/"), "/")
		if len(parts) >= 4 && parts[2] == "archive" && strings.HasSuffix(strings.ToLower(link.Path), ".zip") {
			link.Fragment = ""
			return link, nil
		}
		if len(parts) < 5 || parts[2] != "blob" || !strings.HasSuffix(strings.ToLower(link.Path), ".md") {
			return nil, invalid("请使用 GitHub 的 SKILL.md 文件链接；完整目录请打包 ZIP 上传或提供 ZIP 直链")
		}
		link.Host = "raw.githubusercontent.com"
		link.Path = "/" + strings.Join(append(parts[:2:2], parts[3:]...), "/")
		link.RawPath = ""
		link.RawQuery = ""
	}
	link.Fragment = ""
	return link, nil
}

func validateURL(link *url.URL) error {
	if link.Scheme != "https" || link.Hostname() == "" || link.User != nil || (link.Port() != "" && link.Port() != "443") {
		return invalid("导入仅支持不含账号密码的公网 HTTPS 链接（443 端口）")
	}
	if strings.EqualFold(strings.TrimSuffix(link.Hostname(), "."), "localhost") {
		return invalid("不允许从本机或内网地址导入 Skill")
	}
	if address, err := netip.ParseAddr(link.Hostname()); err == nil && !publicAddress(address) {
		return invalid("不允许从本机或内网地址导入 Skill")
	}
	return nil
}

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
}

func publicAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.Zone() != "" {
		return false
	}
	if address.Is6() && !netip.MustParsePrefix("2000::/3").Contains(address) {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func allowedSourceAddress(host string, address netip.Addr) bool {
	if publicAddress(address) {
		return true
	}
	// DNS proxies can map these fixed catalog/GitHub origins to the Fake-IP range.
	// HTTPS still verifies the original hostname. Never grant this
	// exception to arbitrary source hosts, literal IPs, or private networks.
	if address.Zone() != "" || !netip.MustParsePrefix("198.18.0.0/15").Contains(address.Unmap()) {
		return false
	}
	switch strings.ToLower(host) {
	case "skills.sh", "www.skills.sh", "github.com", "raw.githubusercontent.com", "codeload.github.com":
		return true
	default:
		return false
	}
}

func dialPublic(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	for _, resolved := range addresses {
		if !allowedSourceAddress(host, resolved) {
			return nil, invalid("来源域名未解析到可验证的公网地址；使用 Fake-IP 代理时，请使用真实 IP 解析或下载后上传")
		}
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	for _, resolved := range addresses {
		// Dial the exact checked IP, so a second DNS lookup cannot rebind it.
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		err = dialErr
	}
	if err == nil {
		err = invalid("来源域名没有可用的公网地址")
	}
	return nil, err
}

func fetch(ctx context.Context, client *http.Client, link *url.URL, source string) (Draft, error) {
	data, response, err := download(ctx, client, link, platform.MaxSkillBundleBytes)
	if err != nil {
		return Draft{}, err
	}
	filename := path.Base(response.Request.URL.Path)
	if len(data) >= 4 && string(data[:4]) == "PK\x03\x04" {
		filename = "skill.zip"
	} else if path.Ext(filename) != ".md" && strings.HasPrefix(response.Header.Get("Content-Type"), "text/") {
		filename = "SKILL.md"
	}
	draft, err := Parse(filename, data)
	if err != nil {
		return Draft{}, err
	}
	draft.Spec.Source = "url"
	draft.Spec.Path = strings.TrimSpace(source)
	return draft, nil
}

func download(ctx context.Context, client *http.Client, link *url.URL, limit int64) ([]byte, *http.Response, error) {
	if err := validateURL(link); err != nil {
		return nil, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, link.String(), nil)
	if err != nil {
		return nil, nil, invalid("来源链接格式无效")
	}
	request.Header.Set("Accept", "application/json, text/plain, text/markdown, application/zip, application/octet-stream")
	request.Header.Set("User-Agent", "AgentBox-Skill-Import")
	response, err := client.Do(request)
	if err != nil {
		var validation *platform.ValidationError
		if errors.As(err, &validation) {
			return nil, nil, validation
		}
		return nil, nil, invalid("无法读取公开链接，请确认地址可访问，或下载后使用本地上传")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, nil, invalid(fmt.Sprintf("来源返回 HTTP %d，请确认来源公开可用，或下载后上传", response.StatusCode))
	}
	if response.ContentLength > limit {
		return nil, nil, invalid(fmt.Sprintf("来源下载超过 %d MiB，请只打包所需 Skill 后上传", limit>>20))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, nil, invalid("读取来源文件失败，请重试或下载后上传")
	}
	if int64(len(data)) > limit {
		return nil, nil, invalid(fmt.Sprintf("来源下载超过 %d MiB，请只打包所需 Skill 后上传", limit>>20))
	}
	return data, response, nil
}
