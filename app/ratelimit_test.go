package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRateLimiterAllowsBurstThenRefuses(t *testing.T) {
	l := newRateLimiter(60, 3)
	now := time.Now()
	l.now = func() time.Time { return now }

	for i := range 3 {
		if !l.allow("c") {
			t.Fatalf("request %d refused inside the burst", i+1)
		}
	}
	if l.allow("c") {
		t.Error("request past the burst allowed")
	}
	if !l.allow("other") {
		t.Error("a different client was refused by the first one's budget")
	}
}

func TestRateLimiterRefillsAtTheConfiguredRate(t *testing.T) {
	l := newRateLimiter(60, 1) // one token per second
	now := time.Now()
	l.now = func() time.Time { return now }

	l.allow("c")
	if l.allow("c") {
		t.Fatal("second immediate request allowed with burst 1")
	}
	now = now.Add(500 * time.Millisecond)
	if l.allow("c") {
		t.Error("allowed after half a second, want a full second per token")
	}
	now = now.Add(600 * time.Millisecond)
	if !l.allow("c") {
		t.Error("refused after more than a second, want one token refilled")
	}
}

func TestRateLimiterPrunesIdleBuckets(t *testing.T) {
	l := newRateLimiter(60, 1)
	now := time.Now()
	l.now = func() time.Time { return now }

	l.allow("old")
	now = now.Add(bucketIdle + time.Minute)
	l.prune(now)

	if _, ok := l.buckets["old"]; ok {
		t.Error("idle bucket survived a prune")
	}
}

func TestClientAddrTrustsForwardedForOnlyBehindAProxy(t *testing.T) {
	cases := []struct{ remote, xff, want string }{
		{"127.0.0.1:1234", "203.0.113.5", "203.0.113.5"},
		{"10.0.0.2:1234", "198.51.100.7, 203.0.113.5", "203.0.113.5"},
		{"127.0.0.1:1234", "", "127.0.0.1"},
		{"203.0.113.9:1234", "198.51.100.7", "203.0.113.9"},
		{"[::1]:1234", "203.0.113.5", "203.0.113.5"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := clientAddr(r); got != c.want {
			t.Errorf("remote %s xff %q: got %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
}

func TestRobotsTXTCarriesTheBaseURI(t *testing.T) {
	out := robotsTXT("/art/")
	for _, want := range []string{"Disallow: /art/search\n", "Disallow: /art/api\n", "Disallow: /art/*?p=\n", "Crawl-delay: 10\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("robots.txt lacks %q:\n%s", want, out)
		}
	}
}

// TestHandlerLimitsPagesButNotMedia drives the real handler: a client past its
// budget gets 429 on a page with Retry-After and no Cache-Control, while its
// stylesheet, favicon and avatar requests are never counted.
func TestHandlerLimitsPagesButNotMedia(t *testing.T) {
	limiter, uri := daLimiter, CFG.URI
	daLimiter = newRateLimiter(60, 2)
	CFG.URI = "/"
	defer func() { daLimiter, CFG.URI = limiter, uri }()

	orig := fetchAvatar
	fetchAvatar = func(string, rune) (string, error) { return "png", nil }
	defer func() { fetchAvatar = orig }()

	from := func(target string) *httptest.ResponseRecorder {
		loadTemplates()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.RemoteAddr = "203.0.113.5:4000"
		Handler()(rec, req)
		return rec
	}

	from("/about")
	from("/about")
	third := from("/about")
	if third.Code != 429 {
		t.Fatalf("third page request: status %d, want 429", third.Code)
	}
	if third.Header().Get("Retry-After") == "" || third.Header().Get("Cache-Control") != "" {
		t.Errorf("429 headers: Retry-After %q, Cache-Control %q", third.Header().Get("Retry-After"), third.Header().Get("Cache-Control"))
	}

	for _, target := range []string{"/stylesheet", "/favicon.ico", "/media/emojitar/alice?type=a", "/robots.txt"} {
		if rec := from(target); rec.Code != 200 {
			t.Errorf("%s: status %d while limited, want 200 (exempt)", target, rec.Code)
		}
	}
}

func TestHandlerServesRobotsTXT(t *testing.T) {
	uri := CFG.URI
	CFG.URI = "/"
	defer func() { CFG.URI = uri }()

	rec := serve(t, "/robots.txt")
	if rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "User-agent: *\n") {
		t.Errorf("robots.txt: status %d body %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type %q, want text/plain", ct)
	}
}
