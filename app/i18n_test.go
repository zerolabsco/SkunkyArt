package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnglishCatalogueCoversEveryKeyTheTemplatesUse is the guard against a
// template asking for a key nobody defined: T falls back to the key itself, so
// the failure renders as "nav.home" on the page rather than crashing.
func TestEnglishCatalogueCoversEveryKeyTheTemplatesUse(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "static", "lang", "en.json"))
	if err != nil {
		t.Fatalf("read en.json: %v", err)
	}
	var cat map[string]string
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("en.json is not valid JSON: %v", err)
	}

	files, err := filepath.Glob(filepath.Join("..", "static", "html", "*.htm"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no templates found: %v", err)
	}

	for _, f := range files {
		src, err := os.ReadFile(f) //nolint:gosec // G304: the test reads the repository's own template files
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, key := range templateKeys(string(src)) {
			if _, ok := cat[key]; !ok {
				t.Errorf("%s uses %q, which en.json does not define", filepath.Base(f), key)
			}
		}
	}
}

// templateKeys pulls the key out of every {{T "..."}} action in src.
func templateKeys(src string) []string {
	var out []string
	for rest := src; ; {
		i := strings.Index(rest, `{{T "`)
		if i < 0 {
			return out
		}
		rest = rest[i+len(`{{T "`):]
		j := strings.Index(rest, `"`)
		if j < 0 {
			return out
		}
		out = append(out, rest[:j])
		rest = rest[j:]
	}
}

// TestTFallsBackRatherThanBlanking: a catalogue that has not caught up must show
// English, and an unknown key must show itself, because a blank label in the UI
// gives nobody anything to search for.
func TestTFallsBackRatherThanBlanking(t *testing.T) {
	saved := catalogues
	defer func() { catalogues = saved }()

	catalogues = map[string]map[string]string{
		"en": {"nav.home": "HOME", "nav.about": "About"},
		"xx": {"nav.home": "INICIO"},
	}

	if got := T("xx", "nav.home"); got != "INICIO" {
		t.Errorf("translated key = %q, want INICIO", got)
	}
	if got := T("xx", "nav.about"); got != "About" {
		t.Errorf("untranslated key = %q, want the English fallback", got)
	}
	if got := T("xx", "nav.missing"); got != "nav.missing" {
		t.Errorf("unknown key = %q, want the key itself", got)
	}
	if got := T("zz", "nav.home"); got != "HOME" {
		t.Errorf("unknown language = %q, want the English fallback", got)
	}
}

// TestResolveLangReadsAcceptLanguage covers the header parsing: quality values,
// regional tags falling back to their base, and an instance that has pinned one.
func TestResolveLangReadsAcceptLanguage(t *testing.T) {
	saved, savedCfg := catalogues, CFG.Language
	defer func() { catalogues, CFG.Language = saved, savedCfg }()

	catalogues = map[string]map[string]string{"en": {}, "xx": {}}
	CFG.Language = "auto"

	for _, tc := range []struct{ header, want string }{
		{"xx", "xx"},
		{"xx-XX,xx;q=0.9", "xx"},
		{"fr-FR,fr;q=0.9,en;q=0.8", "en"},
		{"", "en"},
		{"zz", "en"},
	} {
		if got := ResolveLang(tc.header); got != tc.want {
			t.Errorf("ResolveLang(%q) = %q, want %q", tc.header, got, tc.want)
		}
	}

	// A pinned language ignores the header entirely.
	CFG.Language = "xx"
	if got := ResolveLang("fr-FR"); got != "xx" {
		t.Errorf("pinned language = %q, want xx", got)
	}

	// Pinned to something that did not load: English rather than nothing.
	CFG.Language = "nope"
	if got := ResolveLang("xx"); got != DefaultLang {
		t.Errorf("pinned-but-missing = %q, want %q", got, DefaultLang)
	}
}
