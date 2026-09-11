package app

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/krazywarez/devianter"
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

// TestUserAboutRendersProfileDetails is the regression test for the branch
// upstream had disabled with `else if false`: a person's about tab must show
// their interests, social links and how long they have been registered. The
// profile is decoded from JSON shaped like DeviantArt's, since the module
// slice's element type embeds an unexported struct and cannot be built by
// name from here.
func TestUserAboutRendersProfileDetails(t *testing.T) {
	const profile = `{
	  "owner": {"isGroup": false, "username": "alice"},
	  "gruser": {"gruserId": 42, "page": {"modules": [
	    {"name": "about", "moduleData": {"about": {
	      "deviantFor": 86400,
	      "interests": [{"label": "Favourite animal", "value": "skunk"}],
	      "socialLinks": [{"value": "https://social.example/alice"}]
	    }}}
	  ]}}
	}`
	orig := fetchProfile
	fetchProfile = func(string) (devianter.GRuser, devianter.Error, error) {
		var p devianter.GRuser
		if err := json.Unmarshal([]byte(profile), &p); err != nil {
			t.Fatal(err)
		}
		return p, devianter.Error{}, nil
	}
	t.Cleanup(func() { fetchProfile = orig })

	loadTemplates()
	rec := httptest.NewRecorder()
	s := skunkyart{Writer: rec, Host: "http://localhost", BasePath: "/", Type: 'a', Query: "alice", Args: url.Values{}, _pth: "/group_user"}
	s.GRUser()

	body := rec.Body.String()
	for _, want := range []string{"Favourite animal: <b>skunk</b>", `href="https://social.example/alice"`, "Registration date"} {
		if !strings.Contains(body, want) {
			t.Errorf("about page lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "0001-01-01") {
		t.Error("registration date is the zero time")
	}
}
