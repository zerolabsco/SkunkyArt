package app

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRT is the upstream: it counts calls and returns a scripted response.
type fakeRT struct {
	mu     sync.Mutex
	calls  int
	status int
	body   string
	delay  time.Duration
}

func (f *fakeRT) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	time.Sleep(f.delay)
	return &http.Response{
		StatusCode: f.status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(f.body)),
		Request:    r,
	}, nil
}

func (f *fakeRT) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

const puppyURL = "https://www.deviantart.com/_puppy/dabrowse/search/all?q=fox&csrf_token=abc"

func get(t *testing.T, rt http.RoundTripper, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil) //nolint:noctx // test request
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestSecondRequestIsServedFromCache(t *testing.T) {
	up := &fakeRT{status: 200, body: `{"a":1}`}
	rt := newAPICache(1<<20, time.Minute, 0).transport(up)

	get(t, rt, puppyURL)
	status, body := get(t, rt, puppyURL)

	if up.count() != 1 {
		t.Errorf("upstream called %d times, want 1", up.count())
	}
	if status != 200 || body != `{"a":1}` {
		t.Errorf("cached response is %d %q", status, body)
	}
}

func TestExpiredEntryIsRefetched(t *testing.T) {
	up := &fakeRT{status: 200, body: `{}`}
	c := newAPICache(1<<20, time.Minute, 0)
	now := time.Now()
	c.now = func() time.Time { return now }
	rt := c.transport(up)

	get(t, rt, puppyURL)
	now = now.Add(2 * time.Minute)
	get(t, rt, puppyURL)

	if up.count() != 2 {
		t.Errorf("upstream called %d times, want 2 after expiry", up.count())
	}
}

// A 500 here rather than a 403: a 403 is a block and starts the backoff,
// which is covered in apicache_stale_test.go.
func TestNon200IsNotStored(t *testing.T) {
	up := &fakeRT{status: 500, body: "upstream broke"}
	rt := newAPICache(1<<20, time.Minute, 0).transport(up)

	status, body := get(t, rt, puppyURL)
	get(t, rt, puppyURL)

	if status != 500 || body != "upstream broke" {
		t.Errorf("first response is %d %q, want the upstream 500 passed through", status, body)
	}
	if up.count() != 2 {
		t.Errorf("upstream called %d times, want 2: a 500 must not be cached", up.count())
	}
}

func TestBypassesSessionAndOtherHosts(t *testing.T) {
	up := &fakeRT{status: 200, body: "x"}
	rt := newAPICache(1<<20, time.Minute, 0).transport(up)

	for _, url := range []string{
		"https://www.deviantart.com/_puppy",
		"https://www.deviantart.com",
		"https://a.deviantart.net/avatars-big/a/alice.png",
	} {
		get(t, rt, url)
		get(t, rt, url)
	}
	if up.count() != 6 {
		t.Errorf("upstream called %d times, want 6: none of these URLs may be cached", up.count())
	}
}

func TestKeyIgnoresCSRFToken(t *testing.T) {
	up := &fakeRT{status: 200, body: "x"}
	rt := newAPICache(1<<20, time.Minute, 0).transport(up)

	get(t, rt, puppyURL)
	get(t, rt, strings.Replace(puppyURL, "csrf_token=abc", "csrf_token=def", 1))

	if up.count() != 1 {
		t.Errorf("upstream called %d times, want 1: a token refresh must not miss", up.count())
	}
}

func TestByteBoundEvictsLeastRecentlyUsed(t *testing.T) {
	up := &fakeRT{status: 200, body: strings.Repeat("x", 100)}
	rt := newAPICache(250, time.Minute, 0).transport(up)
	a := "https://www.deviantart.com/_puppy/a?p=1"
	b := "https://www.deviantart.com/_puppy/b?p=1"
	c := "https://www.deviantart.com/_puppy/c?p=1"

	get(t, rt, a)
	get(t, rt, b)
	get(t, rt, a) // a is now more recent than b
	get(t, rt, c) // 300 bytes would exceed 250: b goes
	get(t, rt, a)
	get(t, rt, c)
	get(t, rt, b)

	if up.count() != 4 {
		t.Errorf("upstream called %d times, want 4: only b should have been evicted", up.count())
	}
}

func TestConcurrentMissesMakeOneUpstreamCall(t *testing.T) {
	up := &fakeRT{status: 200, body: "x", delay: 50 * time.Millisecond}
	rt := newAPICache(1<<20, time.Minute, 0).transport(up)

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { get(t, rt, puppyURL) })
	}
	wg.Wait()

	if up.count() != 1 {
		t.Errorf("upstream called %d times, want 1 for a burst on one key", up.count())
	}
}

func TestStatsCountHitsAndMisses(t *testing.T) {
	up := &fakeRT{status: 200, body: "abc"}
	c := newAPICache(1<<20, time.Minute, 0)
	rt := c.transport(up)

	get(t, rt, puppyURL)
	get(t, rt, puppyURL)
	get(t, rt, puppyURL)

	hits, misses, stale, entries, held := c.stats()
	if hits != 2 || misses != 1 || stale != 0 || entries != 1 || held != 3 {
		t.Errorf("stats = %d hits, %d misses, %d stale, %d entries, %d bytes; want 2, 1, 0, 1, 3", hits, misses, stale, entries, held)
	}
}

// TestHitDoesNotConsumeAThrottleSlot pins the chain order: the cache sits in
// front of the throttle, so a hit returns even when every throttle slot is
// held. With the order reversed this test hangs and times out.
func TestHitDoesNotConsumeAThrottleSlot(t *testing.T) {
	up := &fakeRT{status: 200, body: "x"}
	th := &daThrottle{base: up, sem: make(chan struct{}, 1)}
	rt := newAPICache(1<<20, time.Minute, 0).transport(th)

	get(t, rt, puppyURL) // populate through the throttle

	th.sem <- struct{}{} // hold the only slot
	done := make(chan struct{})
	go func() {
		get(t, rt, puppyURL)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a cache hit waited on the throttle")
	}
}
