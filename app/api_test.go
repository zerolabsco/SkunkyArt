package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/krazywarez/devianter"
)

// fullviewDeviation returns a deviation whose media assembles into a non-empty
// wixmp URL, i.e. one that sendMedia is meant to serve.
func fullviewDeviation() *devianter.Deviation {
	d := &devianter.Deviation{}
	d.Media.BaseUri = "https://images-wixmp-abc.wixmp.com/f/u/x.png"
	d.Media.Name = "x"
	d.Media.Types = append(d.Media.Types, struct {
		T    string
		H, W int
	}{T: "fullview", H: 1920, W: 1280})
	return d
}

// TestSendMediaServesRealMedia is the regression test for the inverted guard: a
// deviation that has media must be sent, not dropped. In non-proxy mode that is
// a 302 to the wixmp URL; the pre-fix guard returned before writing anything.
func TestSendMediaServesRealMedia(t *testing.T) {
	proxy := CFG.Proxy
	CFG.Proxy = false
	defer func() { CFG.Proxy = proxy }()

	w := httptest.NewRecorder()
	API{main: &skunkyart{Writer: w}}.sendMedia(fullviewDeviation())

	if w.Code != 302 {
		t.Errorf("status is %d, want a 302 redirect to the media", w.Code)
	}
	if w.Header().Get("Location") == "" {
		t.Error("no Location header set — the media was dropped")
	}
}

// TestSendMediaIgnoresEmptyMedia pins the other half of the bug: a deviation
// with no media must be a no-op. With proxy on, the pre-fix code fell through to
// mediaURL[21:] on an empty string and panicked.
func TestSendMediaIgnoresEmptyMedia(t *testing.T) {
	proxy := CFG.Proxy
	CFG.Proxy = true
	defer func() { CFG.Proxy = proxy }()

	w := httptest.NewRecorder()
	API{main: &skunkyart{Writer: w}}.sendMedia(&devianter.Deviation{})

	if w.Code != 200 {
		t.Errorf("status is %d, want nothing written (recorder default 200)", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "" {
		t.Errorf("Location %q set for a media-less deviation, want none", loc)
	}
}

// withDailyDeviations scripts the daily deviations fetch with the given
// entries and counts the calls.
func withDailyDeviations(t *testing.T, devs ...devianter.Deviation) *int {
	t.Helper()
	orig := fetchDailyDeviations
	calls := 0
	fetchDailyDeviations = func(int) (devianter.DailyDeviations, devianter.Error) {
		calls++
		return devianter.DailyDeviations{Deviations: devs}, devianter.Error{}
	}
	t.Cleanup(func() { fetchDailyDeviations = orig })
	return &calls
}

// TestRandomPicksFromTheDailyDeviations pins the new source: one fetch of the
// daily page, and the pick is served as media.
func TestRandomPicksFromTheDailyDeviations(t *testing.T) {
	proxy, nsfw := CFG.Proxy, CFG.Nsfw
	CFG.Proxy, CFG.Nsfw = false, true
	defer func() { CFG.Proxy, CFG.Nsfw = proxy, nsfw }()
	calls := withDailyDeviations(t, *fullviewDeviation())

	w := httptest.NewRecorder()
	API{main: &skunkyart{Writer: w}}.Random()

	if *calls != 1 {
		t.Errorf("daily deviations fetched %d times, want 1", *calls)
	}
	if w.Code != 302 || w.Header().Get("Location") == "" {
		t.Errorf("status %d, Location %q; want a 302 to the pick's media", w.Code, w.Header().Get("Location"))
	}
}

// TestRandomHonoursNSFW pins that a pick is drawn only from what the instance
// may show: with nsfw off and only mature entries there is nothing to serve.
func TestRandomHonoursNSFW(t *testing.T) {
	proxy, nsfw := CFG.Proxy, CFG.Nsfw
	CFG.Proxy, CFG.Nsfw = false, false
	defer func() { CFG.Proxy, CFG.Nsfw = proxy, nsfw }()
	mature := *fullviewDeviation()
	mature.NSFW = true
	withDailyDeviations(t, mature)

	w := httptest.NewRecorder()
	API{main: &skunkyart{Writer: w}}.Random()

	if w.Code != 404 || w.Header().Get("Location") != "" {
		t.Errorf("status %d, Location %q; want 404 and no media", w.Code, w.Header().Get("Location"))
	}
}

// TestSendMediaProxiesWithTheTokenInTheQuery is the regression test for
// /api/random answering 401 with proxying on: the signing token was passed
// inside the path, so wixmp never saw it as a parameter.
func TestSendMediaProxiesWithTheTokenInTheQuery(t *testing.T) {
	proxy, cache := CFG.Proxy, CFG.Cache.Enabled
	CFG.Proxy, CFG.Cache.Enabled = true, false
	defer func() { CFG.Proxy, CFG.Cache.Enabled = proxy, cache }()

	var fetched string
	orig := fetchMedia
	fetchMedia = func(u string) Downloaded {
		fetched = u
		return Downloaded{Status: 200, Body: []byte("png"), Headers: http.Header{"Content-Type": {"image/png"}}}
	}
	defer func() { fetchMedia = orig }()

	d := fullviewDeviation()
	d.Media.Token = []string{"tok.en.sig"}

	w := httptest.NewRecorder()
	API{main: &skunkyart{Writer: w, Args: url.Values{}}}.sendMedia(d)

	u, err := url.Parse(fetched)
	if err != nil || u.Host != "images-wixmp-abc.wixmp.com" {
		t.Fatalf("fetched %q, want a wixmp URL on the deviation's subdomain", fetched)
	}
	if strings.Contains(u.Path, "token") || u.Query().Get("token") == "" {
		t.Errorf("token not passed as a query parameter: path %q query %q", u.Path, u.RawQuery)
	}
	if w.Code != 200 || w.Body.String() != "png" {
		t.Errorf("response %d %q, want the proxied image", w.Code, w.Body.String())
	}
}
