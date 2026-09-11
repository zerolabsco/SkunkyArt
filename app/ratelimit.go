package app

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// rateLimiter is a token bucket per client address. It bounds how many page,
// feed and API requests one client can make, so a single crawler cannot spend
// the whole upstream budget and turn the DeviantArt throttle into latency for
// everyone else.
type rateLimiter struct {
	perSecond float64
	burst     float64
	now       func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
	inserts int
}

type bucket struct {
	tokens float64
	last   time.Time
}

// bucketIdle is how long a client can go unseen before its bucket is dropped.
const bucketIdle = 10 * time.Minute

func newRateLimiter(perMinute, burst int) *rateLimiter {
	return &rateLimiter{
		perSecond: float64(perMinute) / 60,
		burst:     float64(burst),
		now:       time.Now,
		buckets:   map[string]*bucket{},
	}
}

// allow reports whether client may make a request now, spending one token if
// so. A client's first request finds a full bucket.
func (l *rateLimiter) allow(client string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b := l.buckets[client]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[client] = b
		l.inserts++
		if l.inserts%1000 == 0 {
			l.prune(now)
		}
	} else {
		b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.perSecond)
		b.last = now
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// prune drops buckets idle past bucketIdle. The caller holds mu.
func (l *rateLimiter) prune(now time.Time) {
	for client, b := range l.buckets {
		if now.Sub(b.last) > bucketIdle {
			delete(l.buckets, client)
		}
	}
}

// clientAddr is the address a request is limited by. Behind a reverse proxy
// every connection arrives from the proxy, so when the connection is from a
// loopback or private address the client is the rightmost X-Forwarded-For
// entry, which is the one that proxy appended. A direct client's connection is
// not from a private address, so its own header is never trusted.
func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil || (!ip.IsLoopback() && !ip.IsPrivate()) {
		return host
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return host
	}
	parts := strings.Split(xff, ",")
	if forwarded := strings.TrimSpace(parts[len(parts)-1]); forwarded != "" {
		return forwarded
	}
	return host
}

// limited reports whether an endpoint counts against the client's budget.
// Media, avatars and static assets do not: one listing page loads twenty
// thumbnails, which would exhaust any sensible page budget.
func limited(endpoint string) bool {
	switch endpoint {
	case "media", "stylesheet", "favicon.ico", "robots.txt":
		return false
	}
	return true
}

// robotsTXT keeps crawlers off the pages that fan out into unbounded upstream
// requests. The index, daily deviations and posts stay allowed, so the instance
// is still discoverable.
func robotsTXT(base string) string {
	var b strings.Builder
	b.WriteString("User-agent: *\n")
	for _, p := range []string{"search", "api", "group_user", "media", "*?p="} {
		b.WriteString("Disallow: ")
		b.WriteString(base)
		b.WriteString(p)
		b.WriteString("\n")
	}
	b.WriteString("Crawl-delay: 10\n")
	return b.String()
}

// daLimiter is the running instance's limiter, or nil when rate-limit.per-minute
// is 0. Set by ExecuteConfig.
var daLimiter *rateLimiter
