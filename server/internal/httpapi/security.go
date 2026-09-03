package httpapi

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"agentbox/internal/platform"
)

// 登录限流：按客户端 IP 滑动窗口，轮换用户名也不能绕过。
const (
	loginRateLimitAttempts         = 5
	loginRateLimitWindow           = time.Minute
	loginRateLimitMaxKeys          = 4096
	webhookRateLimitAttempts       = 30
	webhookGlobalRateLimitAttempts = 600
	webhookPreAuthGlobalMultiplier = 10
	webhookRateLimitWindow         = time.Minute
	webhookRateLimitMaxKeys        = 4096
)

type trustedProxySettings struct {
	enabled  bool
	prefixes []netip.Prefix
}

// trustedProxyFromEnv 只在显式启用且配置了可信直连代理网段时信任转发头。
func trustedProxyFromEnv() (trustedProxySettings, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("AGENTBOX_TRUSTED_PROXY")), "true") {
		return trustedProxySettings{}, nil
	}
	value := strings.TrimSpace(os.Getenv("AGENTBOX_TRUSTED_PROXY_CIDRS"))
	if value == "" {
		return trustedProxySettings{}, errors.New("AGENTBOX_TRUSTED_PROXY_CIDRS is required when AGENTBOX_TRUSTED_PROXY=true")
	}
	prefixes := make([]netip.Prefix, 0)
	for raw := range strings.SplitSeq(value, ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return trustedProxySettings{}, fmt.Errorf("invalid AGENTBOX_TRUSTED_PROXY_CIDRS entry %q: %w", raw, err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return trustedProxySettings{enabled: true, prefixes: prefixes}, nil
}

// ValidateTrustedProxyEnvironment lets the executable fail startup rather than
// silently trusting attacker-controlled forwarding headers.
func ValidateTrustedProxyEnvironment() error {
	_, err := trustedProxyFromEnv()
	return err
}

// authBearer 按 RFC 7235 解析 Authorization: Bearer <token>，scheme 大小写不敏感。
// 未携带或 scheme 不匹配时返回空串。
func authBearer(request *http.Request) string {
	authorization := strings.TrimSpace(request.Header.Get("Authorization"))
	scheme, value, ok := strings.Cut(authorization, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(value)
}

func roleRank(role platform.UserRole) int {
	switch role {
	case platform.UserRoleAdmin:
		return 3
	case platform.UserRoleOperator:
		return 2
	default:
		return 1
	}
}

// requireRole 要求当前用户角色不低于 minimum（operator 含 admin，viewer 只读）。
// 必须在 requireUser 之后使用。
func (s *Server) requireRole(minimum platform.UserRole, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if roleRank(userFromContext(request.Context()).Role) < roleRank(minimum) {
			s.writeError(w, http.StatusForbidden, "当前角色无权执行此操作")
			return
		}
		next.ServeHTTP(w, request)
	})
}

// loginRateLimiter 是进程内滑动窗口限流器（map+mutex，惰性清理过期条目）。
type loginRateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

func newLoginRateLimiter() *loginRateLimiter {
	return &loginRateLimiter{attempts: make(map[string][]time.Time)}
}

func (l *loginRateLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	// 防止攻击者轮换 key 导致 map 无限增长：条目过多时整体清理过期记录。
	if len(l.attempts) >= loginRateLimitMaxKeys {
		cutoff := now.Add(-loginRateLimitWindow)
		for existing, timestamps := range l.attempts {
			if len(timestamps) == 0 || timestamps[len(timestamps)-1].Before(cutoff) {
				delete(l.attempts, existing)
			}
		}
		if _, exists := l.attempts[key]; !exists && len(l.attempts) >= loginRateLimitMaxKeys {
			return false
		}
	}
	cutoff := now.Add(-loginRateLimitWindow)
	kept := l.attempts[key][:0]
	for _, timestamp := range l.attempts[key] {
		if timestamp.After(cutoff) {
			kept = append(kept, timestamp)
		}
	}
	if len(kept) >= loginRateLimitAttempts {
		l.attempts[key] = kept
		return false
	}
	l.attempts[key] = append(kept, now)
	return true
}

type boundedRateLimiter struct {
	mu          sync.Mutex
	attempts    map[string][]time.Time
	maxAttempts int
	window      time.Duration
	maxKeys     int
}

func newBoundedRateLimiter(maxAttempts int, window time.Duration, maxKeys int) *boundedRateLimiter {
	return &boundedRateLimiter{
		attempts: make(map[string][]time.Time), maxAttempts: maxAttempts, window: window, maxKeys: maxKeys,
	}
}

func (l *boundedRateLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-l.window)
	if len(l.attempts) >= l.maxKeys {
		for existing, timestamps := range l.attempts {
			if len(timestamps) == 0 || !timestamps[len(timestamps)-1].After(cutoff) {
				delete(l.attempts, existing)
			}
		}
		if _, exists := l.attempts[key]; !exists && len(l.attempts) >= l.maxKeys {
			return false
		}
	}
	timestamps := l.attempts[key][:0]
	for _, timestamp := range l.attempts[key] {
		if timestamp.After(cutoff) {
			timestamps = append(timestamps, timestamp)
		}
	}
	if len(timestamps) >= l.maxAttempts {
		l.attempts[key] = timestamps
		return false
	}
	l.attempts[key] = append(timestamps, now)
	return true
}

type webhookRateLimiter struct {
	preAuthPerClient *boundedRateLimiter
	preAuthGlobal    *boundedRateLimiter
	perEndpoint      *boundedRateLimiter
	businessGlobal   *boundedRateLimiter
}

func newWebhookRateLimiter(perScopeAttempts, businessGlobalAttempts, maxKeys int) *webhookRateLimiter {
	return &webhookRateLimiter{
		preAuthPerClient: newBoundedRateLimiter(perScopeAttempts, webhookRateLimitWindow, maxKeys),
		// Keep unauthenticated storage-layer work globally finite while allowing
		// more headroom than the authenticated business-capacity bucket.
		preAuthGlobal:  newBoundedRateLimiter(businessGlobalAttempts*webhookPreAuthGlobalMultiplier, webhookRateLimitWindow, 1),
		perEndpoint:    newBoundedRateLimiter(perScopeAttempts, webhookRateLimitWindow, maxKeys),
		businessGlobal: newBoundedRateLimiter(businessGlobalAttempts, webhookRateLimitWindow, 1),
	}
}

func (l *webhookRateLimiter) allowPreAuthentication(clientIP string, now time.Time) bool {
	if !l.preAuthPerClient.allow(clientIP, now) {
		return false
	}
	// Do not release this admission on failure: every public request consumed
	// parser/storage work regardless of whether its endpoint or signature exists.
	return l.preAuthGlobal.allow("global", now)
}

func (l *webhookRateLimiter) reserveBusinessCapacity(endpointID string, now time.Time) bool {
	if !l.perEndpoint.allow(endpointID, now) {
		return false
	}
	if !l.businessGlobal.allow("global", now) {
		l.perEndpoint.release(endpointID, now)
		return false
	}
	return true
}

func (l *webhookRateLimiter) releaseBusinessCapacity(endpointID string, reservedAt time.Time) {
	l.perEndpoint.release(endpointID, reservedAt)
	l.businessGlobal.release("global", reservedAt)
}

func (l *boundedRateLimiter) release(key string, timestamp time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	timestamps, exists := l.attempts[key]
	if !exists {
		return
	}
	for index, candidate := range timestamps {
		if !candidate.Equal(timestamp) {
			continue
		}
		timestamps = append(timestamps[:index], timestamps[index+1:]...)
		if len(timestamps) == 0 {
			delete(l.attempts, key)
		} else {
			l.attempts[key] = timestamps
		}
		return
	}
}

func (settings trustedProxySettings) contains(address netip.Addr) bool {
	address = address.Unmap()
	for _, prefix := range settings.prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func requestPeerAddress(request *http.Request) (netip.Addr, bool) {
	value := strings.TrimSpace(request.RemoteAddr)
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	address, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}

func (s *Server) trustsForwardedHeaders(request *http.Request) bool {
	peer, ok := requestPeerAddress(request)
	return ok && s.trustedProxy.enabled && s.trustedProxy.contains(peer)
}

// trustedForwardedValue accepts a singleton header only. X-Forwarded-Host and
// X-Forwarded-Proto are expected to be overwritten by the trusted edge proxy.
func (s *Server) trustedForwardedValue(request *http.Request, name string) string {
	if !s.trustsForwardedHeaders(request) {
		return ""
	}
	var values []string
	for _, line := range request.Header.Values(name) {
		for value := range strings.SplitSeq(line, ",") {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				values = append(values, trimmed)
			}
		}
	}
	if len(values) != 1 {
		return ""
	}
	return values[0]
}

// clientIP parses the full X-Forwarded-For chain from right to left, removing
// only explicitly trusted proxies. Any malformed chain falls back to the peer.
func (s *Server) clientIP(request *http.Request) string {
	peer, ok := requestPeerAddress(request)
	if !ok {
		return request.RemoteAddr
	}
	if !s.trustsForwardedHeaders(request) {
		return peer.String()
	}
	chain := make([]netip.Addr, 0, 4)
	for _, line := range request.Header.Values("X-Forwarded-For") {
		for raw := range strings.SplitSeq(line, ",") {
			address, err := netip.ParseAddr(strings.TrimSpace(raw))
			if err != nil {
				return peer.String()
			}
			chain = append(chain, address.Unmap())
		}
	}
	if len(chain) == 0 {
		return peer.String()
	}
	chain = append(chain, peer)
	for index := len(chain) - 1; index >= 0; index-- {
		if !s.trustedProxy.contains(chain[index]) {
			return chain[index].String()
		}
	}
	return chain[0].String()
}

// cookieSecure 判定会话 Cookie 是否带 Secure 属性：
// 仅依据请求本身的 TLS；信任代理时才采纳 X-Forwarded-Proto。
func (s *Server) cookieSecure(request *http.Request) bool {
	return request.TLS != nil ||
		strings.EqualFold(s.trustedForwardedValue(request, "X-Forwarded-Proto"), "https")
}

func canonicalOrigin(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "null") {
		return "", false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.Hostname() == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	hostname := strings.ToLower(parsed.Hostname())
	if strings.HasSuffix(hostname, ".") {
		return "", false
	}
	port := parsed.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	host := hostname
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	return scheme + "://" + host, true
}

func originFromReferer(value string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return "", false
	}
	return canonicalOrigin(parsed.Scheme + "://" + parsed.Host)
}

func (s *Server) effectiveRequestOrigin(request *http.Request) (string, bool) {
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.ToLower(s.trustedForwardedValue(request, "X-Forwarded-Proto")); forwarded == "http" || forwarded == "https" {
		scheme = forwarded
	}
	host := request.Host
	if forwarded := s.trustedForwardedValue(request, "X-Forwarded-Host"); forwardedHostPattern(forwarded) != "" {
		host = forwarded
	}
	return canonicalOrigin(scheme + "://" + host)
}

func (s *Server) allowedRequestOrigin(request *http.Request, origin string) bool {
	canonical, ok := canonicalOrigin(origin)
	if !ok {
		return false
	}
	if _, allowed := s.allowedOrigins[canonical]; allowed {
		return true
	}
	effective, ok := s.effectiveRequestOrigin(request)
	return ok && canonical == effective
}

func (s *Server) validCookieRequestSource(request *http.Request) bool {
	switch request.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if origins := request.Header.Values("Origin"); len(origins) != 0 {
		return len(origins) == 1 && s.allowedRequestOrigin(request, origins[0])
	}
	if referers := request.Header.Values("Referer"); len(referers) != 0 {
		if len(referers) != 1 {
			return false
		}
		origin, ok := originFromReferer(referers[0])
		return ok && s.allowedRequestOrigin(request, origin)
	}
	// Non-browser API clients commonly send neither header. Cookie-authenticated
	// browsers send Origin on unsafe fetch/form requests.
	return true
}
