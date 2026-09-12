package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/krazywarez/devianter"
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

// countingRT counts base round trips for the throttle tests.
type countingRT struct {
	mu    sync.Mutex
	calls int
}

func (c *countingRT) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return &http.Response{StatusCode: 200, Body: http.NoBody, Request: r}, nil
}

func daRequest(ctx context.Context) *http.Request {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.deviantart.com/_puppy/x", nil)
	return req
}

// TestThrottleShedsADeepQueue pins load shedding: with more requests queued
// than the client timeout can absorb, a new one is refused at once with
// errUpstreamBusy and never reaches upstream.
func TestThrottleShedsADeepQueue(t *testing.T) {
	interval := daMinInterval
	daMinInterval = time.Second
	defer func() { daMinInterval = interval }()
	base := &countingRT{}
	th := &daThrottle{base: base, sem: make(chan struct{}, 1)}
	th.waiting.Store(int64(maxQueueWait/time.Second) + 1)

	start := time.Now()
	_, err := th.RoundTrip(daRequest(context.Background()))

	if !errors.Is(err, errUpstreamBusy) {
		t.Fatalf("err = %v, want errUpstreamBusy", err)
	}
	if time.Since(start) > 100*time.Millisecond || base.calls != 0 {
		t.Errorf("shed request took %v and made %d upstream calls, want immediate and none", time.Since(start), base.calls)
	}
}

// TestThrottleDropsACancelledRequestWaitingForASlot pins that a request whose
// client has gone does not sit in the queue: with the only slot held, a
// cancelled context returns at once.
func TestThrottleDropsACancelledRequestWaitingForASlot(t *testing.T) {
	base := &countingRT{}
	th := &daThrottle{base: base, sem: make(chan struct{}, 1)}
	th.sem <- struct{}{} // hold the only slot
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := th.RoundTrip(daRequest(ctx))

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > 100*time.Millisecond || base.calls != 0 {
		t.Errorf("took %v with %d upstream calls, want immediate and none", time.Since(start), base.calls)
	}
}

// TestThrottleCancelledDuringIntervalKeepsTheSlot pins that a request
// cancelled while waiting out the interval does not consume it: the next
// live request starts as soon as the original interval allows.
func TestThrottleCancelledDuringIntervalKeepsTheSlot(t *testing.T) {
	interval := daMinInterval
	daMinInterval = 300 * time.Millisecond
	defer func() { daMinInterval = interval }()
	base := &countingRT{}
	th := &daThrottle{base: base, sem: make(chan struct{}, 1)}

	if _, err := th.RoundTrip(daRequest(context.Background())); err != nil {
		t.Fatal(err)
	}
	first := th.last

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := th.RoundTrip(daRequest(ctx)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if th.last != first {
		t.Error("a cancelled request advanced the interval clock")
	}
	if base.calls != 1 {
		t.Errorf("%d upstream calls, want 1: the cancelled request must not go upstream", base.calls)
	}
}

// TestErrorPageMapsAShedRequestTo503 pins the user-facing side: a shed
// request is a 503 with Retry-After, not a DeviantArt error.
func TestErrorPageMapsAShedRequestTo503(t *testing.T) {
	rec := httptest.NewRecorder()
	skunkyart{Writer: rec, Host: "http://localhost"}.Error(devianter.Error{Error: "devianter: Get ...: " + errUpstreamBusy.Error()})
	if rec.Code != 503 || rec.Header().Get("Retry-After") != "5" {
		t.Errorf("status %d Retry-After %q, want 503 and 5", rec.Code, rec.Header().Get("Retry-After"))
	}
}
