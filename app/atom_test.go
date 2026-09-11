package app

import (
	"encoding/xml"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/krazywarez/devianter"
)

// atomFeed is the subset of an Atom document the tests check. Decoding through
// encoding/xml also proves the output is well-formed XML.
type atomFeed struct {
	XMLName xml.Name `xml:"http://www.w3.org/2005/Atom feed"`
	ID      string   `xml:"id"`
	Updated string   `xml:"updated"`
	Links   []struct {
		Rel  string `xml:"rel,attr"`
		Href string `xml:"href,attr"`
	} `xml:"link"`
	Entries []struct {
		ID        string `xml:"id"`
		Published string `xml:"published"`
		Updated   string `xml:"updated"`
		Thumbnail struct {
			URL string `xml:"url,attr"`
		} `xml:"http://search.yahoo.com/mrss/ group>thumbnail"`
	} `xml:"entry"`
}

func renderFeed(t *testing.T, devs []devianter.Deviation) atomFeed {
	t.Helper()
	rec := httptest.NewRecorder()
	s := skunkyart{Writer: rec, Host: "http://localhost", Atom: true, _pth: "/dd", Args: url.Values{"atom": {"true"}}}
	s.DeviationList(devs, true)

	var feed atomFeed
	if err := xml.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatalf("feed is not well-formed: %v\n%s", err, rec.Body.String())
	}
	return feed
}

func selfLink(f atomFeed) string {
	for _, l := range f.Links {
		if l.Rel == "self" {
			return l.Href
		}
	}
	return ""
}

// TestAtomFeedHasTheRequiredElements pins what the spec demands and readers
// reject a feed without: a feed id and updated time, IRI entry ids, RFC 3339
// timestamps, and the correctly spelled media thumbnail.
func TestAtomFeedHasTheRequiredElements(t *testing.T) {
	proxy, nsfw := CFG.Proxy, CFG.Nsfw
	CFG.Proxy, CFG.Nsfw = true, true
	defer func() { CFG.Proxy, CFG.Nsfw = proxy, nsfw }()

	d := *fullviewDeviation()
	d.ID = 123
	d.Title = "T"
	d.Author.Username = "alice"
	d.PublishedTime.Time = time.Date(2026, 8, 11, 18, 36, 34, 0, time.UTC)

	feed := renderFeed(t, []devianter.Deviation{d})

	if !strings.HasPrefix(feed.ID, "http://localhost/dd?") {
		t.Errorf("feed id %q, want the feed's own URL", feed.ID)
	}
	if feed.Updated != "2026-08-11T18:36:34Z" {
		t.Errorf("feed updated %q, want the newest entry's RFC 3339 time", feed.Updated)
	}
	if selfLink(feed) != feed.ID {
		t.Errorf("self link %q, want %q", selfLink(feed), feed.ID)
	}
	if len(feed.Entries) != 1 {
		t.Fatalf("%d entries, want 1", len(feed.Entries))
	}
	e := feed.Entries[0]
	if !strings.HasPrefix(e.ID, "http://localhost/post/alice/") {
		t.Errorf("entry id %q, want the post's URL", e.ID)
	}
	if e.Published != "2026-08-11T18:36:34Z" || e.Updated != e.Published {
		t.Errorf("entry published %q updated %q, want RFC 3339 and equal", e.Published, e.Updated)
	}
	if e.Thumbnail.URL == "" {
		t.Error("entry has no media:thumbnail, want one (the element used to be misspelled)")
	}
}

// TestAtomFeedIsValidWhenEmpty pins that an empty listing still yields a feed
// with an updated time, so a reader polling an empty gallery gets a document
// rather than an error.
func TestAtomFeedIsValidWhenEmpty(t *testing.T) {
	feed := renderFeed(t, nil)
	if feed.ID == "" || feed.Updated == "" {
		t.Errorf("empty feed id %q updated %q, want both set", feed.ID, feed.Updated)
	}
	if _, err := time.Parse(time.RFC3339, feed.Updated); err != nil {
		t.Errorf("empty feed updated %q is not RFC 3339: %v", feed.Updated, err)
	}
}
