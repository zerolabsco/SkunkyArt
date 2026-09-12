package app

import (
	"encoding/json"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/krazywarez/devianter"
	"golang.org/x/net/html"
)

// ParseComments renders a comment thread, nesting replies under the comment they
// answer. It returns a placeholder message rather than failing if the upstream
// fetch errored.
func (s skunkyart) ParseComments(c devianter.Comments, daError devianter.Error) string {
	if daError.RAW != nil {
		return esc(T(s.Lang, "comments.failed"))
	}

	var cmmts strings.Builder
	replied := make(map[int]string)

	cmmts.WriteString("<details><summary>")
	cmmts.WriteString(esc(T(s.Lang, "deviation.comments")))
	cmmts.WriteString(": <b>")
	cmmts.WriteString(strconv.Itoa(c.Total))
	cmmts.WriteString("</b></summary>")
	for _, x := range c.Thread {
		replied[x.ID] = x.User.Username
		cmmts.WriteString(`<div class="msg`)
		if x.Parent > 0 {
			cmmts.WriteString(` reply`)
		}
		cmmts.WriteString(`"><p id="`)
		cmmts.WriteString(strconv.Itoa(x.ID))
		cmmts.WriteString(`"><img src="`)
		cmmts.WriteString(esc(URLBuilder(s.Host, "media", "emojitar", x.User.Username, "?type=a")))
		cmmts.WriteString(`" width="30px" height="30px" alt="`)
		cmmts.WriteString(esc(x.User.Username))
		cmmts.WriteString(`"><a href="`)
		cmmts.WriteString(esc(URLBuilder(s.Host, "group_user", "?q=", x.User.Username, "&type=a")))
		cmmts.WriteString(`"><b`)
		cmmts.WriteString(` class="`)
		if x.User.Banned {
			cmmts.WriteString(`banned`)
		}
		if x.Author {
			cmmts.WriteString(`author`)
		}
		cmmts.WriteString(`">`)
		cmmts.WriteString(esc(x.User.Username))
		cmmts.WriteString("</b></a> ")

		if x.Parent > 0 {
			cmmts.WriteString(" ")
			cmmts.WriteString(esc(T(s.Lang, "comments.reply_to")))
			cmmts.WriteString(` <a href="`)
			cmmts.WriteString(esc(s._pth))
			cmmts.WriteString("#")
			cmmts.WriteString(strconv.Itoa(x.Parent))
			cmmts.WriteString(`">`)
			if replied[x.Parent] == "" {
				cmmts.WriteString("???")
			} else {
				cmmts.WriteString(esc(replied[x.Parent]))
			}
			cmmts.WriteString("</a>")
		}
		cmmts.WriteString(" [")
		cmmts.WriteString(x.Posted.UTC().String())
		cmmts.WriteString("]<p>")

		cmmts.WriteString(ParseDescription(s.Host, x.TextContent))
		cmmts.WriteString("<p>👍: ")
		cmmts.WriteString(strconv.Itoa(x.Likes))
		cmmts.WriteString(" ⏩: ")
		cmmts.WriteString(strconv.Itoa(x.Replies))
		cmmts.WriteString("</p></div>\n")
	}
	cmmts.WriteString(s.NavBase(DeviationList{
		Pages: 0,
		More:  c.HasMore,
	}))
	cmmts.WriteString("</details>")
	return cmmts.String()
}

// VisibleDeviation reports whether a deviation may be shown by this instance.
//
// Both the HTML listing and the JSON API ask this, deliberately: an API that
// returned what the pages hide would leak exactly what hide-ai and the NSFW
// setting exist to withhold, and a second copy of the rule is a second thing to
// forget to update.
func VisibleDeviation(d *devianter.Deviation) bool {
	if d.AI && CFG.HideAI {
		return false
	}
	return !d.NSFW || CFG.Nsfw
}

// DeviationList renders devs as an HTML grid, or as an Atom feed when the
// request asked for one and allowAtom permits it. NSFW entries are dropped
// unless the instance allows them. Passing content adds a navigation bar.
func (s skunkyart) DeviationList(devs []devianter.Deviation, allowAtom bool, content ...DeviationList) string {
	if s.Atom && s.Page > 1 {
		s.ReturnHTTPError(400)
		return ""
	}

	var list, listContent strings.Builder
	var newest time.Time // the latest entry, for the feed's own <updated>

	for i, l := 0, len(devs); i < l; i++ {
		data := &devs[i]
		if !VisibleDeviation(data) {
			continue
		}
		// Escaped once here: the same values go into both the HTML grid and the
		// Atom feed, and html.EscapeString produces entities XML accepts too.
		author, title := esc(data.Author.Username), esc(data.Title)
		postURL := esc(ConvertDeviantArtURLToSkunkyArt(s.Host, data.Url))
		if preview, fullview := esc(ParseMedia(s.Host, data.Media, 320)), esc(ParseMedia(s.Host, data.Media)); true {
			if allowAtom && s.Atom {
				s.Writer.Header().Add("Content-Type", "application/atom+xml")
				// The entry id is the post's URL here: Atom wants an IRI, and a
				// bare number is not one. Timestamps are RFC 3339, the only form
				// the format allows.
				entryURL := esc(URLBuilder(s.Host, "post", data.Author.Username, "atom-"+strconv.Itoa(data.ID)))
				published := data.PublishedTime.UTC()
				if published.After(newest) {
					newest = published
				}
				listContent.WriteString(`<entry><author><name>`)
				listContent.WriteString(author)
				listContent.WriteString(`</name></author><title>`)
				listContent.WriteString(title)
				listContent.WriteString(`</title><link rel="alternate" type="text/html" href="`)
				listContent.WriteString(entryURL)
				listContent.WriteString(`"/><id>`)
				listContent.WriteString(entryURL)
				listContent.WriteString(`</id><published>`)
				listContent.WriteString(published.Format(time.RFC3339))
				listContent.WriteString(`</published><updated>`)
				listContent.WriteString(published.Format(time.RFC3339))
				listContent.WriteString(`</updated>`)
				listContent.WriteString(`<media:group><media:title>`)
				listContent.WriteString(title)
				listContent.WriteString(`</media:title><media:thumbnail url="`)
				listContent.WriteString(preview)
				listContent.WriteString(`"/></media:group><content type="xhtml"><div xmlns="http://www.w3.org/1999/xhtml"><a href="`)
				listContent.WriteString(postURL)
				listContent.WriteString(`"><img src="`)
				listContent.WriteString(fullview)
				listContent.WriteString(`"/></a><p>`)
				listContent.WriteString(ParseDescription(s.Host, data.TextContent))
				listContent.WriteString(`</p></div></content></entry>`)
			} else {
				listContent.WriteString(`<div class="block">`)
				if fullview != "" && preview != "" {
					listContent.WriteString(`<a title="` + esc(T(s.Lang, "deviation.open")) + `" href="`)
					listContent.WriteString(fullview)
					listContent.WriteString(`"><img loading="lazy" src="`)
					listContent.WriteString(preview)
					listContent.WriteString(`" width="15%" alt="`)
					listContent.WriteString(author)
					listContent.WriteString(" - ")
					listContent.WriteString(title)
					listContent.WriteString(`"></a>`)
				} else {
					listContent.WriteString("<h1>[ " + esc(T(s.Lang, "list.text")) + " ]</h1>")
				}
				listContent.WriteString(`<br><a href="`)
				listContent.WriteString(postURL)
				listContent.WriteString(`">`)
				listContent.WriteString(author)
				listContent.WriteString(" - ")
				listContent.WriteString(title)

				if data.NSFW {
					listContent.WriteString(` [<span class="nsfw">NSFW</span>]`)
				}
				if data.AI {
					listContent.WriteString(" [🤖]")
				}
				if data.DD {
					listContent.WriteString(` [<span class="dd">DD</span>]`)
				}

				listContent.WriteString("</a></div>")
			}
		}
	}

	if allowAtom && s.Atom {
		list.WriteString(`<?xml version="1.0" encoding="UTF-8"?><feed xmlns:media="http://search.yahoo.com/mrss/" xmlns="http://www.w3.org/2005/Atom">`)

		list.WriteString(`<title>`)
		switch {
		case s.Type == 0:
			list.WriteString("Daily Deviations")
		case s.Type == 'g' && len(devs) != 0:
			list.WriteString(esc(devs[0].Author.Username))
		default:
			list.WriteString("SkunkyArt")
		}
		list.WriteString(`</title>`)

		// Atom requires a feed id and updated time; readers reject a feed
		// without them. The id is the feed's own URL on this instance.
		feedURL := esc(s.Host + s._pth + "?" + s.Args.Encode())
		if newest.IsZero() {
			newest = time.Now().UTC()
		}
		list.WriteString(`<id>`)
		list.WriteString(feedURL)
		list.WriteString(`</id><updated>`)
		list.WriteString(newest.Format(time.RFC3339))
		list.WriteString(`</updated><link rel="self" type="application/atom+xml" href="`)
		list.WriteString(feedURL)
		list.WriteString(`"/>`)

		list.WriteString(`<link rel="alternate" href="`)
		list.WriteString(esc(s.Host))
		list.WriteString(`"/>`)

		list.WriteString(listContent.String())

		list.WriteString("</feed>")
		wr(s.Writer, list.String())
	} else {
		list.WriteString(`<div class="content">`)

		list.WriteString(listContent.String())

		list.WriteString("</div>")
		if content != nil {
			list.WriteString(s.NavBase(content[0]))
		}
	}

	return list.String()
}

/* DESCRIPTION/COMMENT PARSER */

// emoticonURL maps an e.deviantart.net emoticon image URL to this instance's
// emote route, by the file's base name without its extension, which is what
// devianter.AEmedia takes. Anything else yields "".
func emoticonURL(host, raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host != "e.deviantart.net" {
		return ""
	}
	name := path.Base(u.Path)
	name = strings.TrimSuffix(name, path.Ext(name))
	if name == "" || name == "." || name == "/" {
		return ""
	}
	return URLBuilder(host, "media", "emojitar", name, "?type=e")
}

// text is one styled run within a description: the rendered HTML, the raw source
// it came from, and the offsets it spans in the original block.
type text struct {
	Txt    string
	TxtRaw string
	From   int
	To     int
}

// ParseDescription renders a DeviantArt description into HTML, handling both the
// Draft.js-style JSON payload and the plain HTML markup DeviantArt returns, and
// rewriting embedded links and artwork references to point at this instance.
// host is the request's scheme and host, as taken by URLBuilder.
//
// TODO: rewrite this whole mess.
func ParseDescription(host string, dscr devianter.Text) string {
	var parsedDescription strings.Builder
	TagBuilder := func(content string, tags ...string) string {
		l := len(tags)
		for x := range l {
			var htm strings.Builder
			htm.WriteString("<")
			htm.WriteString(tags[x])
			htm.WriteString(">")

			htm.WriteString(content)

			htm.WriteString("</")
			htm.WriteString(tags[x])
			htm.WriteString(">")
			content = htm.String()
		}
		return content
	}
	DeleteTrackingFromURL := func(url string) string {
		if len(url) > 42 && url[:42] == "https://www.deviantart.com/users/outgoing?" {
			url = url[42:]
		}
		return url
	}

	if description, dl := dscr.Html.Markup, len(dscr.Html.Markup); dl != 0 &&
		description[0] == '{' &&
		description[dl-1] == '}' {
		var descr struct {
			Blocks []struct {
				Text              string `json:"text"`
				Type              string `json:"type"`
				InlineStyleRanges []struct {
					Offset int    `json:"offset"`
					Length int    `json:"length"`
					Style  string `json:"style"`
				} `json:"inlineStyleRanges"`
				EntityRanges []struct {
					Offset int `json:"offset"`
					Length int `json:"length"`
					Key    int `json:"key"`
				} `json:"entityRanges"`
				Data struct {
					TextAlignment string `json:"textAlignment"`
				} `json:"data"`
			} `json:"blocks"`
			EntityMap map[string]struct {
				Type string `json:"type"`
				Data struct {
					URL    string `json:"url"`
					Config struct {
						// "aligment" is DeviantArt's own spelling; do not correct it.
						Aligment string `json:"aligment"`
						Width    int    `json:"width"`
					} `json:"config"`
					Data devianter.Deviation `json:"data"`
				} `json:"data"`
			} `json:"entityMap"`
		}
		e := json.Unmarshal([]byte(description), &descr)
		try(e)

		entities := make(map[int]devianter.Deviation)
		urls := make(map[int]string)
		for n, x := range descr.EntityMap {
			num, _ := strconv.Atoi(n)
			if x.Data.URL != "" {
				urls[num] = DeleteTrackingFromURL(x.Data.URL)
			}
			entities[num] = x.Data.Data
		}

		for _, x := range descr.Blocks {
			Styles := make([]text, len(x.InlineStyleRanges))

			if len(x.InlineStyleRanges) != 0 {
				var tags = make(map[int][]string)
				for n, rngs := range x.InlineStyleRanges {
					Styles := &Styles[n]
					switch rngs.Style {
					case "BOLD":
						rngs.Style = "b"
					case "UNDERLINE":
						rngs.Style = "u"
					case "ITALIC":
						rngs.Style = "i"
					}
					Styles.From = rngs.Offset
					Styles.To = rngs.Offset + rngs.Length
					FT := Styles.From * Styles.To
					tags[FT] = append(tags[FT], rngs.Style)
				}
				for n := range Styles {
					Styles := &Styles[n]
					Styles.TxtRaw = x.Text[Styles.From:Styles.To]
					Styles.Txt = TagBuilder(esc(Styles.TxtRaw), tags[Styles.From*Styles.To]...)
				}
			}

			switch x.Type {
			case "atomic":
				if len(x.EntityRanges) != 0 {
					d := entities[x.EntityRanges[0].Key]
					parsedDescription.WriteString(`<a href="`)
					parsedDescription.WriteString(esc(ConvertDeviantArtURLToSkunkyArt(host, d.Url)))
					parsedDescription.WriteString(`"><img width="50%" src="`)
					parsedDescription.WriteString(esc(ParseMedia(host, d.Media)))
					parsedDescription.WriteString(`" alt="`)
					parsedDescription.WriteString(esc(d.Author.Username))
					parsedDescription.WriteString(" - ")
					parsedDescription.WriteString(esc(d.Title))
					parsedDescription.WriteString(`" title="`)
					parsedDescription.WriteString(esc(d.Author.Username))
					parsedDescription.WriteString(" - ")
					parsedDescription.WriteString(esc(d.Title))
					parsedDescription.WriteString(`"></a>`)
				}
			case "unstyled":
				if l := len(Styles); l != 0 {
					for n, r := range Styles {
						var tag string
						if x.Type == "header-two" {
							tag = "h2"
						}

						parsedDescription.WriteString(esc(x.Text[:r.From]))
						if len(urls) != 0 && len(x.EntityRanges) != 0 {
							ra := &x.EntityRanges[0]

							parsedDescription.WriteString(`<a target="_blank" href="`)
							parsedDescription.WriteString(esc(urls[ra.Key]))
							parsedDescription.WriteString(`">`)
							parsedDescription.WriteString(r.Txt)
							parsedDescription.WriteString(`</a>`)
						} else if l > n+1 {
							parsedDescription.WriteString(r.Txt)
						}
						parsedDescription.WriteString(TagBuilder(tag, esc(x.Text[r.To:])))
					}
				} else {
					parsedDescription.WriteString(esc(x.Text))
				}
			}
			parsedDescription.WriteString("<br>")
		}
	} else if dl != 0 {
		for tt := html.NewTokenizer(strings.NewReader(dscr.Html.Markup)); ; {
			switch tt.Next() {
			case html.ErrorToken:
				return parsedDescription.String()
			case html.CommentToken, html.DoctypeToken:
				// No renderable content; skip.
			case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
				token := tt.Token()
				switch token.Data {
				case "a":
					for _, a := range token.Attr {
						if a.Key == "href" {
							url := DeleteTrackingFromURL(a.Val)
							parsedDescription.WriteString(`<a target="_blank" href="`)
							parsedDescription.WriteString(esc(url))
							parsedDescription.WriteString(`">`)
							parsedDescription.WriteString(esc(GetValueOfTag(tt)))
							parsedDescription.WriteString("</a> ")
						}
					}
				case "img":
					// Only DeviantArt's emoticons are carried over, served
					// through this instance; any other image is dropped.
					var uri, title string
					for _, a := range token.Attr {
						switch a.Key {
						case "src":
							uri = emoticonURL(host, a.Val)
						case "title":
							title = a.Val
						}
					}
					if uri != "" {
						parsedDescription.WriteString(`<img src="`)
						parsedDescription.WriteString(esc(uri))
						parsedDescription.WriteString(`" alt="`)
						parsedDescription.WriteString(esc(title))
						parsedDescription.WriteString(`" title="`)
						parsedDescription.WriteString(esc(title))
						parsedDescription.WriteString(`">`)
					}
				case "br", "li", "ul", "p", "b":
					// The bare tag, not token.String(): that would carry over
					// whatever attributes DeviantArt's markup put on it.
					if token.Type == html.EndTagToken {
						parsedDescription.WriteString("</")
					} else {
						parsedDescription.WriteString("<")
					}
					parsedDescription.WriteString(token.Data)
					parsedDescription.WriteString(">")
				case "div":
					parsedDescription.WriteString("<p> ")
				}
			case html.TextToken:
				parsedDescription.WriteString(esc(string(tt.Text())))
			}
		}
	}

	return parsedDescription.String()
}
