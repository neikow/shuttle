package orchestrator

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Default per-IP limit for the unauthenticated webhook endpoints. Generous
// enough for real CI/registry webhook traffic, tight enough to bound abuse of
// handlers that do real work (HMAC verify, reconcile) before any auth gate.
const (
	defaultWebhookRatePerMin = 120
	bucketIdleTTL            = 10 * time.Minute
)

// ipRateLimiter applies a per-client-IP token-bucket rate limit. Buckets idle
// out (bucketIdleTTL) so memory stays bounded under IP churn. Keyed on the TCP
// peer (RemoteAddr), not X-Forwarded-For, since XFF is client-spoofable and
// trusting it would let an attacker evade the limit by forging the header.
type ipRateLimiter struct {
	r     rate.Limit
	burst int

	mu      sync.Mutex
	buckets map[string]*ipBucket
	lastGC  time.Time
}

type ipBucket struct {
	lim  *rate.Limiter
	seen time.Time
}

// newIPRateLimiter builds a limiter of perMinute requests/IP with the given
// burst. perMinute must be > 0.
func newIPRateLimiter(perMinute, burst int) *ipRateLimiter {
	if burst < 1 {
		burst = 1
	}
	return &ipRateLimiter{
		r:       rate.Limit(float64(perMinute) / 60.0),
		burst:   burst,
		buckets: make(map[string]*ipBucket),
		lastGC:  time.Now(),
	}
}

// allow reports whether a request from ip may proceed, consuming a token.
func (l *ipRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	if now.Sub(l.lastGC) > bucketIdleTTL {
		for k, b := range l.buckets {
			if now.Sub(b.seen) > bucketIdleTTL {
				delete(l.buckets, k)
			}
		}
		l.lastGC = now
	}

	b, ok := l.buckets[ip]
	if !ok {
		b = &ipBucket{lim: rate.NewLimiter(l.r, l.burst)}
		l.buckets[ip] = b
	}
	b.seen = now
	return b.lim.Allow()
}

// middleware wraps next, returning 429 with Retry-After when the caller's IP
// exceeds the limit.
func (l *ipRateLimiter) middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientIP(r)) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

// clientIP extracts the peer IP from RemoteAddr (host:port), falling back to the
// raw value if it can't be split.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
