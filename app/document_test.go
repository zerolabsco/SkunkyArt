package app

import (
	"strings"
	"testing"

	"github.com/krazywarez/devianter"
)

// docText wraps a document body the way DeviantArt stores it.
func docText(body string) devianter.Text {
	var t devianter.Text
	t.Html.Markup = `{"version":1,"document":{"type":"doc","content":[` + body + `]},"features":[]}`
	return t
}

func withDocConfig(t *testing.T) {
	t.Helper()
	proxy, uri, nsfw := CFG.Proxy, CFG.URI, CFG.Nsfw
	CFG.Proxy, CFG.URI, CFG.Nsfw = true, "/", true
	t.Cleanup(func() { CFG.Proxy, CFG.URI, CFG.Nsfw = proxy, uri, nsfw })
}

// TestDocumentParagraphsMarksAndBreaks uses the shape seen on live
// descriptions: paragraphs of text with link, underline and italic marks.
func TestDocumentParagraphsMarksAndBreaks(t *testing.T) {
	withDocConfig(t)
	out := ParseDescription("http://localhost", docText(`
	{"type":"paragraph","attrs":{"textAlign":"center"},"content":[
	  {"type":"text","text":"| "},
	  {"type":"text","marks":[{"type":"link","attrs":{"href":"https://www.deviantart.com/users/outgoing?https://www.patreon.com/x","target":"_blank"}},{"type":"underline"}],"text":"PATREON"},
	  {"type":"hardBreak"},
	  {"type":"text","marks":[{"type":"italic"}],"text":"Finished <YCH> for "},
	  {"type":"text","marks":[{"type":"textStyle"}],"text":"plain"}
	]}`))
	for _, want := range []string{
		`<p>| <a target="_blank" href="https://www.patreon.com/x"><u>PATREON</u></a><br><i>Finished &lt;YCH&gt; for </i>plain</p>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestDocumentBlocksAndLists(t *testing.T) {
	withDocConfig(t)
	out := ParseDescription("http://localhost", docText(`
	{"type":"heading","attrs":{"level":1},"content":[{"type":"text","text":"Title"}]},
	{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","marks":[{"type":"bold"}],"text":"one"}]}]}]},
	{"type":"orderedList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"two"}]}]}]},
	{"type":"blockquote","content":[{"type":"paragraph","content":[{"type":"text","text":"quoted"}]}]},
	{"type":"codeBlock","content":[{"type":"text","text":"x < y"}]},
	{"type":"horizontalRule"},
	{"type":"mystery","content":[{"type":"text","text":"still shown"}]},
	{"type":"mysteryLeaf","attrs":{"x":1}}`))
	for _, want := range []string{"<h2>Title</h2>", "<ul><li><p><b>one</b></p></li></ul>", "<ol><li><p>two</p></li></ol>", "<blockquote><p>quoted</p></blockquote>", "<pre>x &lt; y</pre>", "<hr>", "still shown"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// TestDocumentEmotes is the new-format half of #6: an official emote with a
// source image maps to the emote route by file name, one without a source
// falls back to its code.
func TestDocumentEmotes(t *testing.T) {
	withDocConfig(t)
	out := ParseDescription("http://localhost", docText(`
	{"type":"paragraph","content":[
	  {"type":"da-emote","attrs":{"data-emote":":star:","data-type":"official","src":"https://e.deviantart.net/emoticons/s/star_full.gif","width":17,"height":16,"url":null,"title":null}},
	  {"type":"da-emote","attrs":{"data-emote":":love:","data-type":"official","src":null,"width":null,"height":null,"url":null,"title":null}},
	  {"type":"da-emote","attrs":{"data-emote":"","data-type":"custom","src":null}}
	]}`))
	for _, want := range []string{`src="http://localhost/media/emojitar/star_full?type=e" alt=":star:"`, `src="http://localhost/media/emojitar/love?type=e" alt=":love:"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "<img") != 2 {
		t.Errorf("want 2 images (the nameless emote renders nothing):\n%s", out)
	}
}

func TestDocumentEmbeddedDeviationAndGif(t *testing.T) {
	withDocConfig(t)
	out := ParseDescription("http://localhost", docText(`
	{"type":"paragraph","content":[
	  {"type":"da-deviation-thumb","attrs":{"cropping":"fill","deviation":{"deviationId":1276762071,"url":"https://www.deviantart.com/ashiori-chan/art/CLOSED-YCH-Portrait-auction-1276762071","title":"[CLOSED] YCH","author":{"username":"AShiori-chan"},"media":{"baseUri":"https://images-wixmp-abc.wixmp.com/f/u/x.png","prettyName":"x_by_y","token":["tok.en.sig"],"types":[{"t":"fullview","h":1920,"w":1280}]}}}},
	  {"type":"da-gif","attrs":{"url":"https://media4.giphy.com/media/x/giphy.mp4","width":480,"height":362}}
	]}`))
	for _, want := range []string{`<a href="http://localhost/post/ashiori-chan/CLOSED-YCH-Portrait-auction-1276762071">`, `src="http://localhost/media/file/abc/`, `alt="AShiori-chan - [CLOSED] YCH"`, `<a target="_blank" href="https://media4.giphy.com/media/x/giphy.mp4">[GIF]</a>`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestDocumentHidesEmbeddedNSFWWhenDisabled(t *testing.T) {
	withDocConfig(t)
	CFG.Nsfw = false
	out := ParseDescription("http://localhost", docText(`
	{"type":"paragraph","content":[{"type":"da-deviation","attrs":{"deviation":{"deviationId":1,"isMature":true,"url":"https://www.deviantart.com/a/art/b-1","title":"secret","author":{"username":"a"}}}}]}`))
	if strings.Contains(out, "secret") {
		t.Errorf("mature embed shown with nsfw off:\n%s", out)
	}
}

// TestDraftJSAndHTMLStillDispatch pins that the two older formats still reach
// their own parsers.
func TestDraftJSAndHTMLStillDispatch(t *testing.T) {
	withDocConfig(t)
	var draft devianter.Text
	draft.Html.Markup = `{"blocks":[{"text":"draft text","type":"unstyled","inlineStyleRanges":[],"entityRanges":[],"data":{}}],"entityMap":{}}`
	if out := ParseDescription("http://localhost", draft); !strings.Contains(out, "draft text") {
		t.Errorf("Draft.js description not rendered:\n%s", out)
	}
	var legacy devianter.Text
	legacy.Html.Markup = `plain <b>html</b>`
	if out := ParseDescription("http://localhost", legacy); !strings.Contains(out, "<b>html</b>") {
		t.Errorf("HTML description not rendered:\n%s", out)
	}
}
