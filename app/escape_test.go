package app

import (
	"html/template"
	"net/http/httptest"
	"skunkyart/static"
	"strings"
	"sync"
	"testing"

	"github.com/krazywarez/devianter"
)

var loadTemplatesOnce sync.Once

// loadTemplates makes static.Templates usable from a test. The non-embed build
// reads the repository's static/ directory; the embed build already has it.
func loadTemplates() {
	loadTemplatesOnce.Do(func() {
		static.StaticPath = "../static"
		static.CopyTemplatesToMemory()
		LoadLanguages()
		ParseTemplates()
	})
}

// markup is the payload every escaping test injects. It closes an attribute,
// closes a tag and opens a new element, which is what an injection needs to do.
const markup = `"><b id=injected>x</b>`

// TestEveryPageTemplateRenders pins down that the switch to html/template
// parses and executes every page: html/template rejects some constructs
// text/template accepts, and a failure here would be a 500 on every request.
func TestEveryPageTemplateRenders(t *testing.T) {
	loadTemplates()
	for _, page := range []string{"about.htm", "daily.htm", "deviantion.htm", "gruser.htm", "search.htm"} {
		rec := httptest.NewRecorder()
		s := skunkyart{Writer: rec, Host: "http://localhost", BasePath: "/"}
		s.ExecuteTemplate(page, "html", &s)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "</html>") {
			t.Errorf("%s: status %d, body %q", page, rec.Code, rec.Body.String())
		}
	}

	rec := httptest.NewRecorder()
	s := skunkyart{Writer: rec, Host: "http://localhost", BasePath: "/", Lang: "en"}
	s.ExecuteTemplate("index.htm", "html", &s)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "</html>") || !strings.Contains(rec.Body.String(), `lang="en"`) {
		t.Errorf("index.htm: status %d, body %q", rec.Code, rec.Body.String())
	}
}

// TestSearchPageEscapesTheQuery is the regression test for the reflected
// query: it appears in the search box's value attribute and in the results
// heading, and both must show it as text.
func TestSearchPageEscapesTheQuery(t *testing.T) {
	loadTemplates()
	rec := httptest.NewRecorder()
	s := skunkyart{Writer: rec, Host: "http://localhost", BasePath: "/", Endpoint: "search", QueryRaw: markup}
	s.Templates.Search.List = template.HTML("<div></div>")
	s.Templates.Search.Content.Total = 1
	s.ExecuteTemplate("search.htm", "html", &s)

	body := rec.Body.String()
	if strings.Contains(body, "<b id=injected>") {
		t.Fatalf("query rendered as markup:\n%s", body)
	}
	if n := strings.Count(body, "&lt;b id=injected&gt;"); n != 3 {
		t.Errorf("escaped query appears %d times, want 3 (title, value attribute, heading):\n%s", n, body)
	}
}

// TestDeviationListEscapesTitles covers the Go-built listing, which
// html/template cannot escape because it arrives as template.HTML.
func TestDeviationListEscapesTitles(t *testing.T) {
	nsfw := CFG.Nsfw
	CFG.Nsfw = true
	defer func() { CFG.Nsfw = nsfw }()

	d := devianter.Deviation{Title: markup}
	d.Author.Username = markup
	devs := []devianter.Deviation{d}

	out := skunkyart{Host: "http://localhost"}.DeviationList(devs, false)
	if strings.Contains(out, "<b id=injected>") || !strings.Contains(out, "&lt;b id=injected&gt;") {
		t.Errorf("HTML listing did not escape the title:\n%s", out)
	}

	rec := httptest.NewRecorder()
	skunkyart{Host: "http://localhost", Writer: rec, Atom: true}.DeviationList(devs, true)
	if feed := rec.Body.String(); strings.Contains(feed, "<b id=injected>") || !strings.Contains(feed, "&lt;b id=injected&gt;") {
		t.Errorf("Atom feed did not escape the title:\n%s", feed)
	}
}

// TestParseCommentsEscapesUsernames covers the comment thread, where the name
// is written as link text and as the "In reply to" target.
func TestParseCommentsEscapesUsernames(t *testing.T) {
	var c devianter.Comments
	var parent, reply devianter.Thread
	parent.ID = 1
	parent.User.Username = markup
	reply.ID = 2
	reply.Parent = 1
	reply.User.Username = "bob"
	c.Thread = []devianter.Thread{parent, reply}

	out := skunkyart{Host: "http://localhost", _pth: "/post/x/y"}.ParseComments(c, devianter.Error{})
	if strings.Contains(out, "<b id=injected>") {
		t.Fatalf("username rendered as markup:\n%s", out)
	}
	if n := strings.Count(out, "&lt;b id=injected&gt;"); n != 4 {
		t.Errorf("escaped username appears %d times, want 4 (avatar, link, author, reply target):\n%s", n, out)
	}
}

// TestParseDescriptionEscapesMarkupText covers the plain-HTML branch: text is
// escaped, whitelisted tags are kept bare, and anything else is dropped.
func TestParseDescriptionEscapesMarkupText(t *testing.T) {
	var d devianter.Text
	d.Html.Markup = `a <b class="z">b</b> <script>alert(1)</script> &lt;i&gt;`

	out := ParseDescription("http://localhost", d)
	for _, bad := range []string{"<script>", `class="z"`, "<i>"} {
		if strings.Contains(out, bad) {
			t.Errorf("output contains %q:\n%s", bad, out)
		}
	}
	for _, want := range []string{"<b>b</b>", "&lt;i&gt;"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// TestErrorPageShowsOneEscapedLine covers the 502 page: a WAF block arrives
// as a whole HTML document, and only its first line is echoed, as text.
func TestErrorPageShowsOneEscapedLine(t *testing.T) {
	rec := httptest.NewRecorder()
	skunkyart{Writer: rec, Host: "http://localhost"}.Error(devianter.Error{Error: "blocked <!DOCTYPE html>\n<html>second line"})

	body := rec.Body.String()
	if rec.Code != 502 {
		t.Errorf("status %d, want 502", rec.Code)
	}
	if strings.Contains(body, "second line") {
		t.Errorf("error page carries lines past the first:\n%s", body)
	}
	if strings.Contains(body, "<!DOCTYPE html>") || !strings.Contains(body, "&lt;!DOCTYPE html&gt;") {
		t.Errorf("upstream error not escaped:\n%s", body)
	}
}

// TestExecuteTemplateUsesTheRequestLanguage pins that the per-language parsed
// sets answer with the right catalogue.
func TestExecuteTemplateUsesTheRequestLanguage(t *testing.T) {
	loadTemplates()
	rec := httptest.NewRecorder()
	s := skunkyart{Writer: rec, Host: "http://localhost", BasePath: "/", Lang: "es"}
	s.ExecuteTemplate("about.htm", "html", &s)
	if !strings.Contains(rec.Body.String(), "Ajustes de la instancia") {
		t.Errorf("Spanish request rendered without the Spanish catalogue:\n%s", rec.Body.String())
	}
}
