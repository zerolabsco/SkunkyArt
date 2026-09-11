package app

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

// failingRT is an upstream that can be switched between a scripted status and
// a transport error mid-test.
type failingRT struct {
	fakeRT
	err error
}

func (f *failingRT) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	err := f.err
	f.mu.Unlock()
	if err != nil {
		f.mu.Lock()
		f.calls++
		f.mu.Unlock()
		return nil, err
	}
	return f.fakeRT.RoundTrip(r)
}

func (f *failingRT) set(status int, body string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.body, f.err = status, body, err
}

// staleCache returns a cache with a one minute TTL and one hour stale window
// over a controllable clock, warmed with one good response for puppyURL.
func staleCache(t *testing.T) (*apiCache, *failingRT, http.RoundTripper, *time.Time) {
	t.Helper()
	up := &failingRT{}
	up.set(200, `{"good":1}`, nil)
	c := newAPICache(1<<20, time.Minute, time.Hour)
	now := time.Now()
	c.now = func() time.Time { return now }
	rt := c.transport(up)
	get(t, rt, puppyURL)
	return c, up, rt, &now
}

func TestStaleEntryServedWhenUpstreamBlocks(t *testing.T) {
	c, up, rt, now := staleCache(t)
	*now = now.Add(2 * time.Minute) // past ttl, inside the stale window
	up.set(403, "<html>blocked</html>", nil)

	status, body := get(t, rt, puppyURL)

	if status != 200 || body != `{"good":1}` {
		t.Errorf("got %d %q, want the stale 200 body", status, body)
	}
	if up.count() != 2 {
		t.Errorf("upstream called %d times, want 2: one warm-up and one attempt that hit the block", up.count())
	}
	if _, _, stale, _, _ := c.stats(); stale != 1 {
		t.Errorf("stale counter is %d, want 1", stale)
	}
}

func TestStaleEntryServedOnTransportError(t *testing.T) {
	_, up, rt, now := staleCache(t)
	*now = now.Add(2 * time.Minute)
	up.set(0, "", errors.New("dial tcp: connection refused"))

	status, body := get(t, rt, puppyURL)

	if status != 200 || body != `{"good":1}` {
		t.Errorf("got %d %q, want the stale 200 body", status, body)
	}
}

func TestBlockBackoffSkipsUpstream(t *testing.T) {
	_, up, rt, now := staleCache(t)
	*now = now.Add(2 * time.Minute)
	up.set(403, "<html>blocked</html>", nil)
	get(t, rt, puppyURL) // triggers the block
	calls := up.count()

	other := "https://www.deviantart.com/_puppy/dabrowse/search/all?q=other"
	status, body := get(t, rt, other)
	if status != 403 || body != "<html>blocked</html>" {
		t.Errorf("uncached key during backoff got %d %q, want the block response", status, body)
	}
	if up.count() != calls {
		t.Errorf("upstream called during backoff (%d -> %d), want none", calls, up.count())
	}

	*now = now.Add(blockBackoff + time.Second)
	up.set(200, `{"back":1}`, nil)
	if status, _ := get(t, rt, other); status != 200 || up.count() != calls+1 {
		t.Errorf("after backoff: status %d, calls %d, want 200 and one more upstream call", status, up.count())
	}
}

func TestStaleEntryDroppedAfterTheWindow(t *testing.T) {
	c, up, rt, now := staleCache(t)
	*now = now.Add(time.Minute + time.Hour + time.Second) // past ttl and stale
	up.set(403, "<html>blocked</html>", nil)

	status, _ := get(t, rt, puppyURL)

	if status != 403 {
		t.Errorf("got %d, want the 403 passed through once nothing stale is held", status)
	}
	if _, _, _, entries, _ := c.stats(); entries != 0 {
		t.Errorf("%d entries held, want 0", entries)
	}
}

func TestZeroStaleKeepsNothingPastTTL(t *testing.T) {
	up := &failingRT{}
	up.set(200, "x", nil)
	c := newAPICache(1<<20, time.Minute, 0)
	now := time.Now()
	c.now = func() time.Time { return now }
	rt := c.transport(up)
	get(t, rt, puppyURL)
	now = now.Add(2 * time.Minute)
	up.set(403, "blocked", nil)

	if status, _ := get(t, rt, puppyURL); status != 403 {
		t.Errorf("got %d with stale 0, want 403: nothing may be served past ttl", status)
	}
}
