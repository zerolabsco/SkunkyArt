package app

import (
	"encoding/json"
	"io"
	"skunkyart/static"
	"sort"
	"strings"
	"sync"
)

// DefaultLang is the catalogue every other one falls back to, key by key. It is
// also the only catalogue guaranteed complete: a translation that has not caught
// up shows English for the strings it is missing rather than a blank or a key.
const DefaultLang = "en"

var (
	catalogues = map[string]map[string]string{}
	langOnce   sync.Once
)

// LoadLanguages reads static/lang/*.json into memory. Called once, from the same
// startup path that copies the templates.
//
// A malformed or missing catalogue is not fatal: the interface falls back to
// English, which is a worse experience than a correct translation but a better
// one than refusing to start.
func LoadLanguages() {
	langOnce.Do(func() {
		for _, name := range static.LanguageFiles() {
			f, err := static.Templates.Open("lang/" + name)
			if err != nil {
				continue
			}
			body, err := io.ReadAll(f)
			_ = f.Close()
			if err != nil {
				continue
			}
			var c map[string]string
			if json.Unmarshal(body, &c) != nil {
				continue
			}
			catalogues[strings.TrimSuffix(name, ".json")] = c
		}
	})
}

// Languages lists the catalogues that loaded, sorted, for the About page.
func Languages() []string {
	out := make([]string, 0, len(catalogues))
	for k := range catalogues {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// T returns the string for key in lang, falling back to English and finally to
// the key itself. Returning the key rather than "" makes a missing translation
// visible in the page instead of silently blank.
func T(lang, key string) string {
	if c, ok := catalogues[lang]; ok {
		if s, ok := c[key]; ok && s != "" {
			return s
		}
	}
	if c, ok := catalogues[DefaultLang]; ok {
		if s, ok := c[key]; ok {
			return s
		}
	}
	return key
}

// ResolveLang picks the catalogue for a request.
//
// When the instance pins a language, that wins. Otherwise the browser's own
// Accept-Language header decides — a header it already sends on every request,
// so reading it adds nothing to what the instance could fingerprint, and needs
// no cookie or query string to remember.
func ResolveLang(acceptLanguage string) string {
	if CFG.Language != "auto" {
		if _, ok := catalogues[CFG.Language]; ok {
			return CFG.Language
		}
		return DefaultLang
	}

	for part := range strings.SplitSeq(acceptLanguage, ",") {
		tag := strings.TrimSpace(part)
		if i := strings.Index(tag, ";"); i >= 0 {
			tag = tag[:i]
		}
		if tag == "" {
			continue
		}
		tag = strings.ToLower(tag)
		if _, ok := catalogues[tag]; ok {
			return tag
		}
		// en-GB and en-US both mean the en catalogue when there is no regional one.
		if i := strings.Index(tag, "-"); i > 0 {
			if _, ok := catalogues[tag[:i]]; ok {
				return tag[:i]
			}
		}
	}
	return DefaultLang
}
