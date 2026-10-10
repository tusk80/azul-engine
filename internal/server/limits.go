package server

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// limiter is a per-key token bucket: each key may make perMin requests a
// minute on average, with short bursts up to burst.
type limiter struct {
	mu      sync.Mutex
	perMin  float64
	burst   float64
	buckets map[string]*bucket
	swept   time.Time
}

type bucket struct {
	tokens float64
	seen   time.Time
}

// newLimiter returns nil when perMin is 0: a nil limiter allows everything.
func newLimiter(perMin int) *limiter {
	if perMin <= 0 {
		return nil
	}
	return &limiter{
		perMin:  float64(perMin),
		burst:   max(float64(perMin)/3, 5),
		buckets: make(map[string]*bucket),
	}
}

func (l *limiter) allow(key string, now time.Time) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.swept) > time.Minute {
		// Drop keys idle long enough to have refilled completely.
		idle := time.Duration(l.burst / l.perMin * float64(time.Minute))
		for k, b := range l.buckets {
			if now.Sub(b.seen) > idle {
				delete(l.buckets, k)
			}
		}
		l.swept = now
	}
	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, seen: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.seen).Minutes()*l.perMin)
	b.seen = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// clientIP returns the address rate limits are keyed on.
//
// Behind a reverse proxy every connection comes from the proxy, so the
// address has to come from a header the proxy sets. Only that one header is
// read, and only when configured, because clients can send any header they
// like: name a header your proxy always overwrites.
//
// X-Forwarded-For is a list that each proxy appends to; the last entry is
// the one our own proxy added, so that is the one used.
func clientIP(r *http.Request, header string) string {
	if header != "" {
		if v := r.Header.Get(header); v != "" {
			if i := strings.LastIndexByte(v, ','); i >= 0 {
				v = v[i+1:]
			}
			return strings.TrimSpace(v)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
