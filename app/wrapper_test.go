package app

import (
	"errors"
	"net/http/httptest"
	"testing"
)

// withAvatarCache turns the media cache on over a temporary directory and
// swaps in a counting avatar fetcher for the test's duration.
func withAvatarCache(t *testing.T, enabled bool, fetch func(string, rune) (string, error)) *int {
	t.Helper()
	cache, orig := CFG.Cache, fetchAvatar
	CFG.Cache.Enabled = enabled
	CFG.Cache.MemCache = false
	CFG.Cache.Path = t.TempDir()
	calls := 0
	fetchAvatar = func(name string, r rune) (string, error) {
		calls++
		return fetch(name, r)
	}
	t.Cleanup(func() { CFG.Cache, fetchAvatar = cache, orig })
	return &calls
}

func emojitar(name string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	skunkyart{Writer: rec, Host: "http://localhost", Type: 'a'}.Emojitar(name)
	return rec
}

func TestEmojitarServesFromCacheAfterFirstFetch(t *testing.T) {
	calls := withAvatarCache(t, true, func(string, rune) (string, error) { return "png", nil })

	first := emojitar("Alice")
	second := emojitar("alice") // case-insensitive, as DeviantArt's paths are

	if *calls != 1 {
		t.Errorf("avatar fetched %d times, want 1", *calls)
	}
	if first.Body.String() != "png" || second.Body.String() != "png" {
		t.Errorf("bodies %q and %q, want both png", first.Body.String(), second.Body.String())
	}
}

func TestEmojitarDoesNotCacheAMiss(t *testing.T) {
	calls := withAvatarCache(t, true, func(string, rune) (string, error) { return "", errors.New("user not exists") })

	first := emojitar("nobody")
	emojitar("nobody")

	if first.Code != 404 {
		t.Errorf("status %d, want 404", first.Code)
	}
	if *calls != 2 {
		t.Errorf("avatar fetched %d times, want 2: a miss must not be cached", *calls)
	}
}

func TestEmojitarFetchesEveryTimeWithCacheOff(t *testing.T) {
	calls := withAvatarCache(t, false, func(string, rune) (string, error) { return "png", nil })

	emojitar("alice")
	emojitar("alice")

	if *calls != 2 {
		t.Errorf("avatar fetched %d times, want 2 with the cache off", *calls)
	}
}
