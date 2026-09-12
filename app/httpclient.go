package app

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
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

// Tunables, set from the upstream config block by ExecuteConfig; these are
// the defaults for a config that omits it. Slower is gentler on the egress
// address, which DeviantArt bans when it asks too often.
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

	// waiting counts requests queued for a slot, for load shedding.
	waiting atomic.Int64
}

// maxQueueWait is the longest a request may expect to queue before it is shed
// with errUpstreamBusy. devianter gives up after 30 seconds, so a request
// that would wait longer than this would only time out anyway, while holding
// a place in the queue that a live request could have used.
const maxQueueWait = 20 * time.Second

// errUpstreamBusy is returned without an upstream call when the queue is
// already deeper than a client will wait for. Error maps it to a 503.
var errUpstreamBusy = errors.New("upstream queue full")

// RoundTrip applies the rate and concurrency limits to DeviantArt requests and
// passes everything else straight through to the base transport.
//
// A request whose context ends while it waits is dropped without taking a
// turn: under a crawl, most queued requests have already been abandoned by
// their client, and letting each one still burn an interval slot is what
// turned the queue into a wall of timeouts.
func (t *daThrottle) RoundTrip(req *http.Request) (*http.Response, error) {
	// Only throttle DeviantArt's WAF-protected API host; let everything else fly.
	if !strings.Contains(req.URL.Hostname(), "deviantart.com") {
		return t.base.RoundTrip(req)
	}
	ctx := req.Context()

	// Shed before queueing when the queue already implies a wait no client
	// will sit through.
	if queued := t.waiting.Load(); time.Duration(queued)*daMinInterval > maxQueueWait {
		return nil, errUpstreamBusy
	}
	t.waiting.Add(1)
	defer t.waiting.Add(-1)

	// Concurrency cap: wait for a slot, or give up with the caller.
	select {
	case t.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-t.sem }()

	// Rate cap: enforce a minimum interval between request starts. A request
	// cancelled while waiting leaves last untouched, so the interval it did
	// not use goes to the next request.
	t.mu.Lock()
	if wait := daMinInterval - time.Since(t.last); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			t.mu.Unlock()
			return nil, ctx.Err()
		}
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
