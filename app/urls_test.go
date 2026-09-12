package app

import (
	"strings"
	"testing"

	"github.com/krazywarez/devianter"
)

func TestProxiedMediaURLCarriesTokenAndFilenameAsQuery(t *testing.T) {
	uri := CFG.URI
	CFG.URI = "/"
	defer func() { CFG.URI = uri }()

	raw := "https://images-wixmp-abc.wixmp.com/f/u/x.png/v1/fit/w_640,h_364/x_by_y.png?token=tok.en.sig"
	got := proxiedMediaURL("http://localhost", raw, "x_by_y.png")
	want := "http://localhost/media/file/abc/f/u/x.png/v1/fit/w_640,h_364/x_by_y.png?filename=x_by_y.png&token=tok.en.sig"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if proxiedMediaURL("http://localhost", "https://example.com/x.png", "x.png") != "" {
		t.Error("a non-wixmp URL was proxied")
	}
}

func TestParseMediaMatchesTheProxyRoute(t *testing.T) {
	proxy, uri := CFG.Proxy, CFG.URI
	CFG.Proxy, CFG.URI = true, "/"
	defer func() { CFG.Proxy, CFG.URI = proxy, uri }()

	d := fullviewDeviation()
	d.Media.Token = []string{"tok.en.sig"}
	got := ParseMedia("http://localhost", d.Media)
	if !strings.HasPrefix(got, "http://localhost/media/file/abc/") || !strings.Contains(got, "token=tok.en.sig") || !strings.Contains(got, "filename=") {
		t.Errorf("proxied media URL is %q", got)
	}

	CFG.Proxy = false
	if got := ParseMedia("http://localhost", d.Media); !strings.HasPrefix(got, "https://images-wixmp-abc.wixmp.com/") {
		t.Errorf("with proxying off got %q, want the wixmp URL", got)
	}
}

func TestConvertDeviantArtURLToSkunkyArt(t *testing.T) {
	uri := CFG.URI
	CFG.URI = "/"
	defer func() { CFG.URI = uri }()

	cases := map[string]string{
		"https://www.deviantart.com/alice/art/Title-123":         "http://localhost/post/alice/Title-123",
		"https://alice.deviantart.com/art/Title-123":             "http://localhost/post/alice/Title-123",
		"https://www.deviantart.com/stash/01t1te6losnc":          "",
		"https://sta.sh/01t1te6losnc":                            "",
		"https://www.deviantart.com/alice":                       "",
		"https://example.com/alice/art/Title-123":                "",
		"https://www.deviantart.com/alice/art/Title-123?comment": "http://localhost/post/alice/Title-123",
	}
	for in, want := range cases {
		if got := ConvertDeviantArtURLToSkunkyArt("http://localhost", in); got != want {
			t.Errorf("%s: got %q, want %q", in, got, want)
		}
	}
}

// TestLegacyEmoticonIsServedThroughTheInstance is the regression test for #6's
// HTML half: the emoticon name used to be cut out of the URL by fixed offsets,
// which only fit one URL shape.
func TestLegacyEmoticonIsServedThroughTheInstance(t *testing.T) {
	uri := CFG.URI
	CFG.URI = "/"
	defer func() { CFG.URI = uri }()

	var d devianter.Text
	d.Html.Markup = `hi <img src="https://e.deviantart.net/emoticons/s/smile.gif" title=":) (Smile)"> and <img src="https://e.deviantart.net/emoticons/letters/l/love.gif" title="Love"> not <img src="https://example.com/x.png">`
	out := ParseDescription("http://localhost", d)
	for _, want := range []string{`src="http://localhost/media/emojitar/smile?type=e"`, `src="http://localhost/media/emojitar/love?type=e"`, `alt=":) (Smile)"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "example.com") {
		t.Errorf("foreign image carried over:\n%s", out)
	}
}
