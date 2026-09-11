package app

import (
	"context"
	"encoding/json"
	"fmt"
	htmlesc "html"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"skunkyart/static"
	"strconv"
	"strings"
	"time"

	"github.com/krazywarez/devianter"
	"golang.org/x/net/html"
)

/* INTERNAL */

// wr writes s to w. A write error here means the client went away mid-response,
// which a handler cannot act on, so it is deliberately discarded.
func wr(w io.Writer, s string) {
	_, _ = io.WriteString(w, s)
}

func exit(msg string, code int) {
	println(msg)
	os.Exit(code)
}
func try(e error) {
	if e != nil {
		println(e.Error())
	}
}
func tryWithExitStatus(err error, code int) {
	if err != nil {
		exit(err.Error(), code)
	}
}

// esc escapes s for use as HTML text or inside a quoted attribute. The Go-built
// fragments bypass html/template's contextual escaping because they are handed
// to it as template.HTML, so every DeviantArt-supplied string they contain has
// to be escaped here instead.
func esc(s string) string {
	return htmlesc.EscapeString(s)
}

// restore swallows a panic in the calling goroutine so that one bad parse cannot
// take the whole process down. The panic is logged rather than dropped silently.
func restore() {
	if r := recover(); r != nil {
		println("recovered from panic:", fmt.Sprint(r))
	}
}

var instances []byte

// About is the instance list and settings shown in the frontend, refreshed by
// RefreshInstances.
var About instanceAbout

// RefreshInstances re-fetches the published instance list every hour, forever.
// Run it in its own goroutine; fetch failures are logged and retried next cycle.
func RefreshInstances() {
	for {
		func() {
			defer restore()
			instances = Download("https://raw.githubusercontent.com/krazywarez/skunky-art/main/instances.json").Body
			try(json.Unmarshal(instances, &About))
		}()
		time.Sleep(1 * time.Hour)
	}
}

// instanceAbout is the instance metadata exposed to the frontend and the API.
type instanceAbout struct {
	Proxy     bool       `json:"proxy"`
	Nsfw      bool       `json:"nsfw"`
	HideAI    bool       `json:"hide-ai"`
	Theme     string     `json:"theme"`
	Instances []settings `json:"instances"`
}

type skunkyart struct {
	Writer http.ResponseWriter
	_pth   string

	Args url.Values
	Page int
	Type rune
	Atom bool

	// Lang is the catalogue chosen for this request, resolved once in the
	// handler so every template and helper agrees on one answer.
	Lang string

	// Host is the scheme and host this request arrived on, e.g.
	// "https://art.example.com". It is per-request rather than global because
	// concurrent requests can arrive on different hosts and ports.
	Host string

	BasePath, Endpoint string
	Query, QueryRaw    string

	API     API
	Version string

	// The template.HTML fields hold fragments the Go builders already
	// escaped, so html/template inserts them as-is. Everything typed string is
	// escaped by the template at the point of use.
	Templates struct {
		About instanceAbout

		SomeList  template.HTML
		DDStrips  template.HTML
		Deviation struct {
			Post        devianter.Post
			Description template.HTML
			Related     template.HTML
			StringTime  string
			Tags        template.HTML
			Comments    template.HTML
		}

		GroupUser struct {
			GR           devianter.GRuser
			Admins       template.HTML
			Group        bool
			CreationDate string

			About struct {
				A devianter.About

				DescriptionFormatted template.HTML
				Interests, Social    template.HTML
				Comments             template.HTML
				BG                   string
				BGMeta               devianter.Deviation
			}

			Gallery struct {
				Folders template.HTML
				Pages   int
				List    template.HTML
			}
		}
		Search struct {
			Content devianter.Search
			List    template.HTML
		}
	}
}

// ExecuteTemplate renders the named template from dir with data, responding 500
// if the template cannot be parsed.
func (s skunkyart) ExecuteTemplate(file, dir string, data any) {
	var buf strings.Builder
	tmp := template.New(file)
	// T is bound to this request's language, so templates ask for a key and
	// never have to know which catalogue answered.
	tmp = tmp.Funcs(template.FuncMap{
		"T": func(key string) string { return T(s.Lang, key) },
	})
	tmp, err := tmp.ParseFS(static.Templates, dir+"/*")
	if err != nil {
		s.Writer.WriteHeader(500)
		wr(s.Writer, err.Error())
		return
	}
	try(tmp.Execute(&buf, &data))
	wr(s.Writer, buf.String())
}

// URLBuilder joins strs into an absolute instance URL, prefixing host and the
// configured URI and inserting slashes between path segments but not before
// query separators. host is the request's own scheme and host: passing the
// wrong one emits links to another origin, which the instance's own
// Content-Security-Policy then blocks.
func URLBuilder(host string, strs ...string) string {
	var str strings.Builder
	l := len(strs)
	str.WriteString(host)
	str.WriteString(CFG.URI)
	for n, x := range strs {
		str.WriteString(x)
		if n := n + 1; n < l && len(strs[n]) != 0 && (strs[n][0] != '?' && strs[n][0] != '&') && (x[0] != '?' && x[0] != '&') {
			str.WriteString("/")
		}
	}
	return str.String()
}

// Error responds 502 with the error DeviantArt reported upstream. Only the
// first line is shown: a WAF block arrives as a whole HTML page, which is
// neither readable nor safe to echo.
func (s skunkyart) Error(dAerr devianter.Error) {
	s.Writer.WriteHeader(502)

	reason, _, _ := strings.Cut(dAerr.Error, "\n")

	var msg strings.Builder
	msg.WriteString(`<html><link rel="stylesheet" href="`)
	msg.WriteString(URLBuilder(s.Host, "stylesheet"))
	msg.WriteString(`" /><h3>DeviantArt error — '`)
	msg.WriteString(esc(reason))
	msg.WriteString("'</h3></html>")

	wr(s.Writer, msg.String())
}

// ReturnHTTPError responds with a styled error page for the given status.
func (s skunkyart) ReturnHTTPError(status int) {
	// A failed upstream fetch reports status 0, and WriteHeader panics on any
	// code outside 1xx-5xx. Treat anything unusable as a gateway failure.
	if status < 100 || status > 599 {
		status = http.StatusBadGateway
	}
	s.Writer.WriteHeader(status)

	var msg strings.Builder
	msg.WriteString(`<html><link rel="stylesheet" href="`)
	msg.WriteString(URLBuilder(s.Host, "stylesheet"))
	msg.WriteString(`" /><h1>`)
	msg.WriteString(strconv.Itoa(status))
	msg.WriteString(" - ")
	msg.WriteString(http.StatusText(status))
	msg.WriteString("</h1></html>")

	wr(s.Writer, msg.String())
}

// SetFilename sets the Content-Disposition filename for the response.
func (s skunkyart) SetFilename(name string) {
	var filename strings.Builder
	filename.WriteString(`filename="`)
	filename.WriteString(name)
	filename.WriteString(`"`)
	s.Writer.Header().Add("Content-Disposition", filename.String())
}

// Downloaded is the result of a Download. A Status of 0 means the request never
// completed, in which case Body and Headers are empty.
type Downloaded struct {
	Headers http.Header
	Status  int
	Body    []byte
}

// Download fetches urlString with the configured User-Agent, routing through
// download-proxy when one is set. Every failure path returns the zero
// Downloaded, so callers must check Status before trusting Body or Headers.
func Download(urlString string) (d Downloaded) {
	cli := &http.Client{}
	if CFG.DownloadProxy != "" {
		u, err := url.Parse(CFG.DownloadProxy)
		if err != nil {
			try(err)
			return
		}
		cli.Transport = ProxiedTransport(u)
	}

	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlString, nil)
	if err != nil {
		try(err)
		return
	}
	req.Header.Set("User-Agent", CFG.UserAgent)

	resp, err := cli.Do(req)
	if err != nil {
		try(err)
		return
	}
	defer func() { try(resp.Body.Close()) }()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		try(err)
		return
	}

	d.Body = b
	d.Status = resp.StatusCode
	d.Headers = resp.Header
	return
}

/* PARSING HELPERS */

// ParseMedia returns the URL to serve for media: a link back through this
// instance's media proxy when proxying is on, or DeviantArt's own URL when it is
// off. An optional thumb width selects a thumbnail instead of the full image.
// host is the request's scheme and host, as taken by URLBuilder.
func ParseMedia(host string, media devianter.Media, thumb ...int) string {
	mediaURL, filename := devianter.UrlFromMedia(media, thumb...)
	if len(mediaURL) != 0 && CFG.Proxy {
		mediaURL = mediaURL[21:]
		dot := strings.Index(mediaURL, ".")
		if filename == "" {
			filename = "image.gif"
		}
		return URLBuilder(host, "media", "file", mediaURL[:dot], mediaURL[dot+11:], "&filename=", filename)
	} else if !CFG.Proxy {
		return mediaURL
	}
	return ""
}

// ConvertDeviantArtURLToSkunkyArt rewrites a deviantart.com post link into the
// equivalent link on this instance. It returns an empty string for URLs it does
// not handle, including sta.sh links. host is the request's scheme and host, as
// taken by URLBuilder.
func ConvertDeviantArtURLToSkunkyArt(host, url string) (output string) {
	if len(url) > 32 && url[27:32] != "stash" {
		url = url[27:]
		firstshash := strings.Index(url, "/")
		lastshash := firstshash + strings.Index(url[firstshash+1:], "/")
		if lastshash != -1 {
			output = URLBuilder(host, "post", url[:firstshash], url[lastshash+2:])
		}
	}
	return
}

// BuildUserPlate renders the small avatar-and-username block linking to a user's
// about page. host is the request's scheme and host, as taken by URLBuilder.
func BuildUserPlate(host, name string) string {
	var htm strings.Builder
	htm.WriteString(`<div class="user-plate"><img src="`)
	htm.WriteString(esc(URLBuilder(host, "media", "emojitar", name, "?type=a")))
	htm.WriteString(`"><a href="`)
	htm.WriteString(esc(URLBuilder(host, "group_user", "?type=about&q=", name)))
	htm.WriteString(`">`)
	htm.WriteString(esc(name))
	htm.WriteString(`</a></div>`)
	return htm.String()
}

// GetValueOfTag returns the text of the tokenizer's next token, or an empty
// string if that token is not text.
func GetValueOfTag(t *html.Tokenizer) string {
	for tt := t.Next(); ; {
		if tt == html.TextToken {
			return string(t.Text())
		} else {
			return ""
		}
	}
}

// DeviationList describes the pagination state of a list of artworks: how many
// pages exist, and whether another page follows the current one.
type DeviationList struct {
	Pages int
	More  bool
}

// NavBase renders the page navigation bar for a list.
func (s skunkyart) NavBase(c DeviationList) string {
	var list strings.Builder

	list.WriteString("<br>")
	prevrev := func(msg string, page int, onpage bool) {
		if !onpage {
			list.WriteString(`<a href="`)
			list.WriteString(esc(s._pth))
			list.WriteString(`?p=`)
			list.WriteString(strconv.Itoa(page))
			if s.Type != 0 {
				list.WriteString("&type=")
				list.WriteRune(s.Type)
			}
			if s.Query != "" {
				list.WriteString("&q=")
				list.WriteString(esc(s.Query))
			}
			if f := s.Args.Get("folder"); f != "" {
				list.WriteString("&folder=")
				list.WriteString(esc(f))
			}
			list.WriteString(`">`)
			list.WriteString(msg)
			list.WriteString("</a> ")
		} else {
			list.WriteString(strconv.Itoa(page))
			list.WriteString(" ")
		}
	}

	p := s.Page

	if p > 1 {
		prevrev("<= Prev |", p-1, false)
	} else {
		p = 1
	}

	// The window runs to the last page or the current one, whichever is further
	// out. Callers that cannot count pages pass Pages: 0 — the comment list on an
	// artwork is one — and bounding purely by Pages then ended the loop before
	// i reached 1, so page one rendered no numbers at all. With nothing before it
	// to link back to and no further page to link on, the whole panel came out as
	// a bare <br>.
	last := max(c.Pages, p)

	for i, x := p-6, 0; (i <= last && i <= p+6) && x < 12; i++ {
		if i > 0 {
			var onPage bool
			if i == p {
				onPage = true
			}

			prevrev(strconv.Itoa(i), i, onPage)
			x++
		}
	}

	if c.More {
		prevrev("| Next =>", p+1, false)
	}

	return list.String()
}
