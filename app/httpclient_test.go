package app

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// stubTransport records how many requests reached it and returns an empty 200.
type stubTransport struct {
	mu sync.Mutex
	n  int
}

func (s *stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
	return httptest.NewRecorder().Result(), nil
}

func newTestThrottle(base http.RoundTripper, gap time.Duration, maxConcurrent int) *daThrottle {
	return &daThrottle{base: base, sem: make(chan struct{}, maxConcurrent)}
}

// DeviantArt requests must be spaced by at least daMinInterval.
func TestThrottleRateLimitsDeviantArt(t *testing.T) {
	stub := &stubTransport{}
	tr := newTestThrottle(stub, daMinInterval, daMaxConcurrent)

	start := time.Now()
	const n = 3
	for range n {
		req, _ := http.NewRequest("GET", "https://www.deviantart.com/_puppy/x", nil)
		if _, err := tr.RoundTrip(req); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	elapsed := time.Since(start)

	// n requests => at least (n-1) gaps between them.
	if want := time.Duration(n-1) * daMinInterval; elapsed < want {
		t.Errorf("DA requests were not throttled: %d requests took %v, want >= %v", n, elapsed, want)
	}
	if stub.n != n {
		t.Errorf("expected all %d requests to reach the base transport, got %d", n, stub.n)
	}
}

// Non-DA hosts (e.g. the wixmp image CDN) must not be slowed down.
func TestThrottleSkipsOtherHosts(t *testing.T) {
	stub := &stubTransport{}
	tr := newTestThrottle(stub, daMinInterval, daMaxConcurrent)

	start := time.Now()
	for range 5 {
		req, _ := http.NewRequest("GET", "https://images-wixmp-ed30a86b8c4ca887773594c2.wixmp.com/f/x.jpg", nil)
		if _, err := tr.RoundTrip(req); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if elapsed := time.Since(start); elapsed >= daMinInterval {
		t.Errorf("non-DA host was throttled: 5 requests took %v, want < %v", elapsed, daMinInterval)
	}
	if stub.n != 5 {
		t.Errorf("expected 5 requests through, got %d", stub.n)
	}
}

// Concurrent callers must never exceed daMaxConcurrent in-flight DA requests.
func TestThrottleCapsConcurrency(t *testing.T) {
	var (
		mu       sync.Mutex
		inFlight int
		peak     int
	)
	counting := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		mu.Unlock()

		time.Sleep(20 * time.Millisecond) // hold the slot

		mu.Lock()
		inFlight--
		mu.Unlock()
		return httptest.NewRecorder().Result(), nil
	})

	tr := newTestThrottle(counting, daMinInterval, daMaxConcurrent)

	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			req, _ := http.NewRequest("GET", "https://www.deviantart.com/_puppy/x", nil)
			_, _ = tr.RoundTrip(req)
		})
	}
	wg.Wait()

	if peak > daMaxConcurrent {
		t.Errorf("concurrency cap breached: peak %d in-flight DA requests, max %d", peak, daMaxConcurrent)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// InstallDAThrottle must preserve proxy-from-environment so HTTPS_PROXY (VPN
// egress) keeps working, and must not panic on a repeat call.
func TestInstallDAThrottlePreservesProxy(t *testing.T) {
	orig, origCache := http.DefaultTransport, daCache
	defer func() { http.DefaultTransport, daCache = orig, origCache }()

	InstallDAThrottle()

	// With api-cache on (the default) the cache is outermost and the throttle
	// sits inside it; with it off the throttle is outermost.
	rt := http.DefaultTransport
	if CFG.APICache.Enabled {
		ct, ok := rt.(*cachedTransport)
		if !ok {
			t.Fatalf("DefaultTransport is not the cache, got %T", rt)
		}
		rt = ct.base
	}
	th, ok := rt.(*daThrottle)
	if !ok {
		t.Fatalf("DefaultTransport was not wrapped by the throttle, got %T", rt)
	}
	base, ok := th.base.(*http.Transport)
	if !ok {
		t.Fatalf("base transport is not *http.Transport, got %T", th.base)
	}
	if base.Proxy == nil {
		t.Error("base transport lost its Proxy func: HTTPS_PROXY / VPN egress would break")
	}
}
