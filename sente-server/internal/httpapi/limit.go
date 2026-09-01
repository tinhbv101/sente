package httpapi

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"sente.app/server/internal/ratelimit"
)

// Rate limiting middleware (docs/06 §1.3, docs/08 §4.1).

// clientIP is the address a limit is charged to when there is no user yet.
//
// X-Forwarded-For is only believed when the server is configured as being behind
// a proxy. Trusting it unconditionally would make every limit trivially bypassable
// by setting the header.
func (s *Server) clientIP(r *http.Request) string {
	if s.config.TrustProxyHeaders {
		// Behind a CDN there are two proxies, and X-Forwarded-For's rightmost entry
		// is the CDN's own address: every user would share one bucket. The CDN
		// puts the real client in a header of its own (Cloudflare: CF-Connecting-IP).
		if s.config.ClientIPHeader != "" {
			if ip := strings.TrimSpace(r.Header.Get(s.config.ClientIPHeader)); ip != "" {
				return ip
			}
		}
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			parts := strings.Split(forwarded, ",")
			// The rightmost entry is the one our own proxy appended, so it is the
			// only one an outside caller cannot forge.
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// limit charges one token before running the handler. The key is the user when
// there is one, otherwise the client address.
//
// A Redis failure refuses the request rather than waving it through. Redis is
// already required to play at all -- leases and routing both need it -- so
// pretending the limiter is optional would only mean losing limits at exactly the
// moment something is going wrong.
func (s *Server) limit(rule ratelimit.Rule, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := s.clientIP(r)
		if claims := userFrom(r.Context()); claims != nil {
			key = claims.UserID
		}

		result, err := s.config.Limiter.Allow(r.Context(), rule, key)
		if err != nil {
			s.config.Logger.Error("rate limiter unavailable", "rule", rule.Name, "error", err)
			writeError(w, r, http.StatusServiceUnavailable, "unavailable",
				"Dịch vụ đang bận. Vui lòng thử lại.")
			return
		}

		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(result.Limit))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(result.ResetAt.Unix(), 10))

		if !result.Allowed {
			retry := int(result.RetryAfter.Round(time.Second) / time.Second)
			if retry < 1 {
				retry = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			writeError(w, r, http.StatusTooManyRequests, "rate_limited",
				"Bạn thao tác quá nhanh. Vui lòng chờ một lát.")
			return
		}
		next(w, r)
	}
}
