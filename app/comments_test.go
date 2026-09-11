package app

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/krazywarez/devianter"
)

// withCommentSeams scripts the deviation and comment fetches and counts the
// comment fetches, which is the call the link is meant to save.
func withCommentSeams(t *testing.T, total int) *int {
	t.Helper()
	origDev, origCom := fetchDeviation, fetchComments
	comments := 0
	fetchDeviation = func(string, string) (devianter.Post, devianter.Error) {
		var p devianter.Post
		p.Deviation.Title = "T"
		p.Deviation.Author.Username = "alice"
		p.Comments.Total = total
		return p, devianter.Error{}
	}
	fetchComments = func(string, string, int, int) (devianter.Comments, devianter.Error) {
		comments++
		return devianter.Comments{Total: total}, devianter.Error{}
	}
	t.Cleanup(func() { fetchDeviation, fetchComments = origDev, origCom })
	return &comments
}

func post(args url.Values) *httptest.ResponseRecorder {
	loadTemplates()
	rec := httptest.NewRecorder()
	s := skunkyart{Writer: rec, Host: "http://localhost", BasePath: "/", Args: args, _pth: "/post/alice/t-1"}
	s.Deviation("alice", "t-1")
	return rec
}

func TestPostShowsACommentsLinkWithoutFetching(t *testing.T) {
	nsfw := CFG.Nsfw
	CFG.Nsfw = true
	defer func() { CFG.Nsfw = nsfw }()
	fetches := withCommentSeams(t, 7)

	body := post(url.Values{}).Body.String()

	if *fetches != 0 {
		t.Errorf("comments fetched %d times on a plain post view, want 0", *fetches)
	}
	if !strings.Contains(body, `href="/post/alice/t-1?comments=1"`) || !strings.Contains(body, "Comments (7)") {
		t.Errorf("post lacks the comments link with its count:\n%s", body)
	}
}

func TestPostFetchesCommentsWhenAsked(t *testing.T) {
	nsfw := CFG.Nsfw
	CFG.Nsfw = true
	defer func() { CFG.Nsfw = nsfw }()
	fetches := withCommentSeams(t, 7)

	body := post(url.Values{"comments": {"1"}}).Body.String()

	if *fetches != 1 {
		t.Errorf("comments fetched %d times with ?comments=1, want 1", *fetches)
	}
	if !strings.Contains(body, "<details><summary>Comments: <b>7</b>") {
		t.Errorf("thread not rendered:\n%s", body)
	}
}

func TestNavBaseKeepsTheCommentsParameter(t *testing.T) {
	s := skunkyart{_pth: "/post/alice/t-1", Args: url.Values{"comments": {"1"}}, Page: 1}
	out := s.NavBase(DeviationList{More: true})
	if !strings.Contains(out, "?p=2&comments=1") {
		t.Errorf("next link drops comments=1:\n%s", out)
	}
}

func TestGroupSearchURLPagesByTen(t *testing.T) {
	cases := map[int]string{
		0: "https://www.deviantart.com/groups/?q=cats",
		1: "https://www.deviantart.com/groups/?q=cats",
		2: "https://www.deviantart.com/groups/?q=cats&offset=10",
		3: "https://www.deviantart.com/groups/?q=cats&offset=20",
	}
	for page, want := range cases {
		if got := groupSearchURL("cats", page); got != want {
			t.Errorf("page %d: %s, want %s", page, got, want)
		}
	}
}
