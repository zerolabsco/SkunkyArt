package app

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/krazywarez/devianter"
)

// docNode is one node of the document format DeviantArt's current editor
// stores: a tree of typed nodes, text leaves carrying marks. Only the fields
// the renderer reads are declared; attrs is decoded per node type.
type docNode struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Attrs   json.RawMessage `json:"attrs"`
	Marks   []docMark       `json:"marks"`
	Content []docNode       `json:"content"`
}

type docMark struct {
	Type  string `json:"type"`
	Attrs struct {
		Href string `json:"href"`
	} `json:"attrs"`
}

// parseDocument reports whether markup is a document-format description and
// returns its root. A JSON payload without a "document" key, such as the
// older Draft.js form, is not one.
func parseDocument(markup string) (docNode, bool) {
	if len(markup) == 0 || markup[0] != '{' {
		return docNode{}, false
	}
	var d struct {
		Document *docNode `json:"document"`
	}
	if json.Unmarshal([]byte(markup), &d) != nil || d.Document == nil {
		return docNode{}, false
	}
	return *d.Document, true
}

// deleteTrackingFromURL strips DeviantArt's outgoing-link redirector, so a
// link goes where it says rather than through deviantart.com first.
func deleteTrackingFromURL(u string) string {
	return strings.TrimPrefix(u, "https://www.deviantart.com/users/outgoing?")
}

// renderDocument renders a document-format description as HTML. Every string
// from the payload is escaped here, since the result is handed to the
// template as trusted HTML. Unknown container nodes render their children;
// unknown leaves render nothing.
func renderDocument(host string, root docNode) string {
	var b strings.Builder
	renderDocNode(&b, host, root)
	return b.String()
}

func renderDocNode(b *strings.Builder, host string, n docNode) {
	children := func() {
		for _, c := range n.Content {
			renderDocNode(b, host, c)
		}
	}
	wrap := func(opening, closing string) {
		b.WriteString(opening)
		children()
		b.WriteString(closing)
	}

	switch n.Type {
	case "text":
		renderDocText(b, host, n)
	case "paragraph":
		wrap("<p>", "</p>")
	case "heading":
		var a struct {
			Level int `json:"level"`
		}
		_ = json.Unmarshal(n.Attrs, &a)
		// The page's own title is its h1; keep description headings below it.
		level := min(max(a.Level+1, 2), 4)
		tag := "h" + strconv.Itoa(level)
		wrap("<"+tag+">", "</"+tag+">")
	case "bulletList":
		wrap("<ul>", "</ul>")
	case "orderedList":
		wrap("<ol>", "</ol>")
	case "listItem":
		wrap("<li>", "</li>")
	case "blockquote":
		wrap("<blockquote>", "</blockquote>")
	case "codeBlock":
		wrap("<pre>", "</pre>")
	case "hardBreak":
		b.WriteString("<br>")
	case "horizontalRule":
		b.WriteString("<hr>")
	case "da-emote":
		renderDocEmote(b, host, n.Attrs)
	case "da-deviation", "da-deviation-thumb":
		renderDocDeviation(b, host, n.Attrs)
	case "da-gif":
		var a struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(n.Attrs, &a)
		if a.URL != "" {
			b.WriteString(`<a target="_blank" href="`)
			b.WriteString(esc(a.URL))
			b.WriteString(`">[GIF]</a>`)
		}
	case "da-mention":
		var a struct {
			Username string `json:"username"`
			User     struct {
				Username string `json:"username"`
			} `json:"user"`
		}
		_ = json.Unmarshal(n.Attrs, &a)
		name := a.Username
		if name == "" {
			name = a.User.Username
		}
		if name != "" {
			b.WriteString(`<a href="`)
			b.WriteString(esc(URLBuilder(host, "group_user", "?type=about&q=", name)))
			b.WriteString(`">@`)
			b.WriteString(esc(name))
			b.WriteString("</a>")
		}
	default:
		children()
	}
}

// docMarkTags maps the inline marks to the tags that render them. textStyle
// carries colour and font choices, which the instance's own stylesheet
// decides, so it is not listed.
var docMarkTags = map[string]string{
	"bold": "b", "strong": "b",
	"italic": "i", "em": "i",
	"underline": "u",
	"strike":    "s", "strikethrough": "s",
	"code": "code",
}

func renderDocText(b *strings.Builder, _ string, n docNode) {
	var opening strings.Builder
	var closing []string // pushed in mark order, emitted in reverse
	for _, m := range n.Marks {
		if m.Type == "link" && m.Attrs.Href != "" {
			opening.WriteString(`<a target="_blank" href="`)
			opening.WriteString(esc(deleteTrackingFromURL(m.Attrs.Href)))
			opening.WriteString(`">`)
			closing = append(closing, "</a>")
			continue
		}
		if tag, ok := docMarkTags[m.Type]; ok {
			opening.WriteString("<" + tag + ">")
			closing = append(closing, "</"+tag+">")
		}
	}
	b.WriteString(opening.String())
	b.WriteString(esc(n.Text))
	for _, c := range slices.Backward(closing) {
		b.WriteString(c)
	}
}

// renderDocEmote renders an emoticon through the instance's emote route. An
// official emote names its image; when it does not, the emote code without
// its colons is the best guess at the file name.
func renderDocEmote(b *strings.Builder, host string, attrs json.RawMessage) {
	var a struct {
		Code  string `json:"data-emote"`
		Src   string `json:"src"`
		Title string `json:"title"`
	}
	_ = json.Unmarshal(attrs, &a)
	src := emoticonURL(host, a.Src)
	if src == "" {
		if name := strings.Trim(a.Code, ":"); name != "" {
			src = URLBuilder(host, "media", "emojitar", name, "?type=e")
		}
	}
	if src == "" {
		return
	}
	label := a.Title
	if label == "" {
		label = a.Code
	}
	b.WriteString(`<img src="`)
	b.WriteString(esc(src))
	b.WriteString(`" alt="`)
	b.WriteString(esc(label))
	b.WriteString(`" title="`)
	b.WriteString(esc(label))
	b.WriteString(`">`)
}

// renderDocDeviation renders an embedded artwork as its thumbnail linking to
// the post on this instance, or as a text link when it has no media.
func renderDocDeviation(b *strings.Builder, host string, attrs json.RawMessage) {
	var a struct {
		Deviation devianter.Deviation `json:"deviation"`
	}
	if json.Unmarshal(attrs, &a) != nil {
		return
	}
	d := &a.Deviation
	if !VisibleDeviation(d) {
		return
	}
	link := ConvertDeviantArtURLToSkunkyArt(host, d.Url)
	label := esc(d.Author.Username + " - " + d.Title)
	img := ParseMedia(host, d.Media, 320)
	if link != "" {
		b.WriteString(`<a href="`)
		b.WriteString(esc(link))
		b.WriteString(`">`)
	}
	if img != "" {
		b.WriteString(`<img width="50%" src="`)
		b.WriteString(esc(img))
		b.WriteString(`" alt="`)
		b.WriteString(label)
		b.WriteString(`" title="`)
		b.WriteString(label)
		b.WriteString(`">`)
	} else {
		b.WriteString(label)
	}
	if link != "" {
		b.WriteString("</a>")
	}
}
