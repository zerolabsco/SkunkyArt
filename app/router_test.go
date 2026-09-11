package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// serve runs one request through the real handler.
func serve(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	loadTemplates()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	Handler()(rec, req)
	return rec
}

// TestCacheControlByRoute pins the header each kind of response carries, and
// that error responses carry none, so a proxy or browser never keeps a
// failure.
func TestCacheControlByRoute(t *testing.T) {
	uri := CFG.URI
	CFG.URI = "/"
	defer func() { CFG.URI = uri }()

	orig := fetchAvatar
	fetchAvatar = func(name string, _ rune) (string, error) {
		if name == "missing" {
			return "", errors.New("user not exists")
		}
		return "png-bytes", nil
	}
	defer func() { fetchAvatar = orig }()

	cases := []struct {
		target, want string
		status       int
	}{
		{"/stylesheet", cacheControlAssets, 200},
		{"/favicon.ico", cacheControlAssets, 200},
		{"/about", cacheControlPage, 200},
		{"/api/instance", cacheControlPage, 200},
		{"/media/emojitar/alice?type=a", cacheControlAssets, 200},
		{"/media/emojitar/missing?type=a", "", 404},
		{"/media/file/x@evil/f.png", "", 400},
		{"/api/nonexistent", "", 404},
		{"/nonexistent", "", 404},
	}
	for _, c := range cases {
		rec := serve(t, c.target)
		if rec.Code != c.status {
			t.Errorf("%s: status %d, want %d", c.target, rec.Code, c.status)
		}
		if got := rec.Header().Get("Cache-Control"); got != c.want {
			t.Errorf("%s: Cache-Control %q, want %q", c.target, got, c.want)
		}
	}
}
