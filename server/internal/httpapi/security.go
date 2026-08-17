package httpapi

import (
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"agentbox/internal/platform"
)

// 登录限流：按客户端 IP 滑动窗口，轮换用户名也不能绕过。
const (
	loginRateLimitAttempts = 5
	loginRateLimitWindow   = time.Minute
	loginRateLimitMaxKeys  = 4096
)

// trustedProxyFromEnv 读取 AGENTBOX_TRUSTED_PROXY（默认 false）。
// 为 false 时不信任 X-Forwarded-* 系列请求头（WS Origin 白名单、
// Worker 回连地址、Cookie Secure 判定、登录限流客户端 IP）。
func trustedProxyFromEnv() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("AGENTBOX_TRUSTED_PROXY")), "true")
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

// clientIP 提取限流用的客户端地址；仅在信任代理时才采纳 X-Forwarded-For。
func (s *Server) clientIP(request *http.Request) string {
	if s.trustedProxy {
		if forwarded := firstForwardedValue(request.Header.Get("X-Forwarded-For")); forwarded != "" {
			return forwarded
		}
	}
	if host, _, err := net.SplitHostPort(request.RemoteAddr); err == nil {
		return host
	}
	return request.RemoteAddr
}

// cookieSecure 判定会话 Cookie 是否带 Secure 属性：
// 仅依据请求本身的 TLS；信任代理时才采纳 X-Forwarded-Proto。
func (s *Server) cookieSecure(request *http.Request) bool {
	return request.TLS != nil ||
		(s.trustedProxy && strings.EqualFold(strings.TrimSpace(request.Header.Get("X-Forwarded-Proto")), "https"))
}
