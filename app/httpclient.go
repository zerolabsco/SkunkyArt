package app

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DeviantArt fronts its API with AWS CloudFront + WAF, which bans egress IPs that
// hit it too hard. Under a bot flood, unbounded concurrent handlers each fetch
// ~150-200 KB of DA JSON, which both hammers that IP (risking a ban) and can OOM
// the process. devianter makes its requests with a bare &http.Client{}, so they go
// through http.DefaultTransport — we wrap it here to bound the rate and concurrency
// of calls to deviantart.com and to add timeouts. Requests to other hosts (e.g.
// wixmp image CDN) are passed straight through, so media stays fast.
//
// http.ProxyFromEnvironment is preserved, so HTTPS_PROXY (VPN egress) still applies.

// Tunables (kept in source; safe defaults). Lower is gentler on the DA IP.
var (
	daMinInterval   = 400 * time.Millisecond // minimum gap between DA request starts
	daMaxConcurrent = 2                      // max simultaneous in-flight DA requests
)

// downloadTimeout bounds a single outbound fetch end to end, so that a stalled
// CDN connection cannot pin a request handler open indefinitely.
const downloadTimeout = 60 * time.Second

type daThrottle struct {
	base http.RoundTripper
	sem  chan struct{}
	mu   sync.Mutex
	last time.Time
}

// RoundTrip applies the rate and concurrency limits to DeviantArt requests and
// passes everything else straight through to the base transport.
func (t *daThrottle) RoundTrip(req *http.Request) (*http.Response, error) {
	// Only throttle DeviantArt's WAF-protected API host; let everything else fly.
	if !strings.Contains(req.URL.Hostname(), "deviantart.com") {
		return t.base.RoundTrip(req)
	}

	// Concurrency cap: block until a slot frees up (backpressure under floods).
	t.sem <- struct{}{}
	defer func() { <-t.sem }()

	// Rate cap: enforce a minimum interval between request starts.
	t.mu.Lock()
	if wait := daMinInterval - time.Since(t.last); wait > 0 {
		time.Sleep(wait)
	}
	t.last = time.Now()
	t.mu.Unlock()

	return t.base.RoundTrip(req)
}

// baseTransport is the tuned transport installed by InstallDAThrottle, kept so
// that per-client transports (see ProxiedTransport) inherit the same timeouts
// instead of silently bypassing them.
var baseTransport *http.Transport

// tunedTransport clones the current default transport, preserving its Proxy
// (ProxyFromEnvironment) and connection-pool defaults, and tightens timeouts to
// bound hung connections.
func tunedTransport() *http.Transport {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		// Already wrapped, or a non-standard transport is installed. Start from a
		// fresh one rather than panicking on a type assertion.
		base = &http.Transport{Proxy: http.ProxyFromEnvironment}
	}

	t := base.Clone()
	t.TLSHandshakeTimeout = 10 * time.Second
	t.ResponseHeaderTimeout = 20 * time.Second
	t.ExpectContinueTimeout = 2 * time.Second
	return t
}

// daCache is the API response cache shared by every transport, or nil when
// api-cache.enabled is false.
var daCache *apiCache

// chain wraps base with the throttle and, when enabled, the cache in front
// of it, so a hit never spends a throttle slot.
func chain(base http.RoundTripper) http.RoundTripper {
	rt := throttled(base)
	if daCache != nil {
		return daCache.transport(rt)
	}
	return rt
}

// logCacheStatsForever prints one line an hour so an operator can see the
// cache working without an endpoint. Run it in its own goroutine.
func logCacheStatsForever(c *apiCache) {
	for {
		time.Sleep(time.Hour)
		hits, misses, stale, entries, held := c.stats()
		println("api cache:", hits, "hits,", misses, "misses,", stale, "served stale,", entries, "entries,", held>>20, "MB held")
	}
}

// InstallDAThrottle wraps http.DefaultTransport with the rate/concurrency limits
// and timeouts above, and with the API response cache when it is enabled. Call
// once at startup, after ExecuteConfig and before any DeviantArt request.
func InstallDAThrottle() {
	baseTransport = tunedTransport()
	if CFG.APICache.Enabled {
		daCache = newAPICache(CFG.APICache.MaxSize<<20, apiCacheTTL, apiCacheStale)
		go logCacheStatsForever(daCache)
	}
	http.DefaultTransport = chain(baseTransport)
}

// throttled wraps base with the DeviantArt rate and concurrency limits.
func throttled(base http.RoundTripper) http.RoundTripper {
	return &daThrottle{base: base, sem: make(chan struct{}, daMaxConcurrent)}
}

// ProxiedTransport returns a throttled transport routing through proxy. Downloads
// configured with download-proxy go through here so they keep the timeouts and
// limits that InstallDAThrottle installs on the default transport.
func ProxiedTransport(proxy *url.URL) http.RoundTripper {
	var base *http.Transport
	if baseTransport != nil {
		base = baseTransport.Clone()
	} else {
		base = tunedTransport()
	}
	base.Proxy = http.ProxyURL(proxy)
	return chain(base)
}
