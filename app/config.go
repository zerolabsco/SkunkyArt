package app

import (
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"skunkyart/static"
	"strconv"
	"time"

	"github.com/krazywarez/devianter"
)

// Release carries the build's version and description, set at link time and
// shown by --help and the API.
var Release struct {
	Version     string
	Description string
}

type cacheConfig struct {
	Enabled        bool   `json:"enabled"`
	MemCache       bool   `json:"memcache"`
	Path           string `json:"path"`
	MaxSize        int64  `json:"max-size"`
	Lifetime       string `json:"lifetime"`
	UpdateInterval int64  `json:"update-interval"`
}

type apiCacheConfig struct {
	Enabled bool   `json:"enabled"`
	MaxSize int64  `json:"max-size"`
	TTL     string `json:"ttl"`
}

type rateLimitConfig struct {
	PerMinute int `json:"per-minute"`
	Burst     int `json:"burst"`
}

type config struct {
	cfg           string
	Listen        string          `json:"listen"`
	URI           string          `json:"uri"`
	Cache         cacheConfig     `json:"cache"`
	APICache      apiCacheConfig  `json:"api-cache"`
	RateLimit     rateLimitConfig `json:"rate-limit"`
	Proxy         bool            `json:"proxy"`
	Nsfw          bool            `json:"nsfw"`
	HideAI        bool            `json:"hide-ai"`
	Theme         string          `json:"theme"`
	Language      string          `json:"language"`
	UserAgent     string          `json:"user-agent"`
	DownloadProxy string          `json:"download-proxy"`
	StaticPath    string          `json:"static-path"`
}

// CFG is the running instance's configuration, holding the defaults below until
// ExecuteConfig overwrites them from the config file.
var CFG = config{
	cfg:      "config.json",
	Listen:   "127.0.0.1:3003",
	Theme:    "auto",
	Language: "auto",
	URI:      "/",
	Cache: cacheConfig{
		Enabled:        true,
		Path:           "cache",
		Lifetime:       "1w",
		MaxSize:        200,
		UpdateInterval: 1,
	},
	APICache: apiCacheConfig{
		Enabled: true,
		MaxSize: 64,
		TTL:     "5i",
	},
	RateLimit: rateLimitConfig{
		PerMinute: 60,
		Burst:     20,
	},
	StaticPath: "static",
	UserAgent:  "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36",
	Proxy:      true,
	Nsfw:       true,
}

var lifetimeParsed int64

// apiCacheTTL is api-cache.ttl parsed, set by ExecuteConfig.
var apiCacheTTL time.Duration

// parseLifetime reads a duration in the config's unit syntax: a number
// followed by i (minutes), h (hours), d (days), w (weeks), m (30-day
// months) or y (360-day years).
func parseLifetime(s string) (time.Duration, error) {
	if s == "" {
		return 0, errors.New("empty lifetime")
	}
	numstr := regexp.MustCompile("[0-9]+").FindAllString(s, -1)
	if len(numstr) == 0 {
		return 0, errors.New("lifetime has no number: " + s)
	}
	num, _ := strconv.Atoi(numstr[len(numstr)-1])

	day := 24 * time.Hour
	var unit time.Duration
	switch s[len(s)-1:] {
	case "i":
		unit = time.Minute
	case "h":
		unit = time.Hour
	case "d":
		unit = day
	case "w":
		unit = 7 * day
	case "m":
		unit = 30 * day
	case "y":
		unit = 360 * day
	default:
		return 0, errors.New("invalid unit specified: " + s[len(s)-1:])
	}
	return unit * time.Duration(num), nil
}

// checkCacheWritable creates the cache directory if it is missing and confirms
// this process can actually write into it, returning the error that a real cache
// write would hit.
//
// An unwritable cache directory is otherwise a silent cliff: every media request
// still succeeds by re-downloading from the CDN, so the only symptom is one
// "permission denied" line per request and a cache that never fills.
func checkCacheWritable(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	probe := path + "/.skunkyart-write-probe"
	if err := os.WriteFile(probe, nil, 0600); err != nil {
		return err
	}
	return os.Remove(probe)
}

// ExecuteConfig loads the config file into CFG, validates it, and starts the
// cache rotation loop if caching is on. It exits the process on a config that
// cannot be read, that asks for caching without proxying, or that points caching
// at a directory this process cannot write.
func ExecuteConfig() {
	if CFG.cfg != "" {
		f, err := os.ReadFile(CFG.cfg)
		tryWithExitStatus(err, 1)
		tryWithExitStatus(json.Unmarshal(f, &CFG), 1)
		if CFG.Cache.Enabled && !CFG.Proxy {
			exit("Incompatible settings detected: cannot use caching media content without proxy", 1)
		}

		if CFG.Cache.Enabled {
			if err := checkCacheWritable(CFG.Cache.Path); err != nil {
				exit("Cache directory is not writable by this process (uid "+
					strconv.Itoa(os.Getuid())+"): "+err.Error()+
					"\nGrant that uid write access to the directory, or set cache.enabled to false."+
					"\nThe official container image runs as uid 10000, so a bind-mounted cache needs:"+
					"\n  chown -R 10000:10000 <cache dir on the host>", 1)
			}

			if CFG.Cache.Lifetime != "" {
				d, err := parseLifetime(CFG.Cache.Lifetime)
				if err != nil {
					exit("config: cache.lifetime: "+err.Error(), 1)
				}
				lifetimeParsed = d.Milliseconds()
			}
			// max-size is documented in megabytes. This was 1024^2, which in Go is
			// XOR (1026), not exponentiation — so the cap was ~1000x too small.
			CFG.Cache.MaxSize *= 1024 * 1024
			go InitCacheSystem()
			if CFG.Cache.MemCache {
				go InitMemCacheJanitor()
			}
		}

		About = instanceAbout{
			Proxy:  CFG.Proxy,
			Nsfw:   CFG.Nsfw,
			HideAI: CFG.HideAI,
			Theme:  CFG.Theme,
		}

		// A theme the stylesheet cannot honour would silently fall back to auto,
		// so say so instead.
		switch CFG.Theme {
		case "auto", "dark", "light":
		default:
			exit("config: theme must be one of auto, dark, light; got "+CFG.Theme, 1)
		}

		if CFG.APICache.Enabled {
			d, err := parseLifetime(CFG.APICache.TTL)
			if err != nil {
				exit("config: api-cache.ttl: "+err.Error(), 1)
			}
			apiCacheTTL = d
		}

		// per-minute 0 turns the limit off; a burst below one token would
		// refuse every request, so it is floored to one.
		if CFG.RateLimit.PerMinute > 0 {
			daLimiter = newRateLimiter(CFG.RateLimit.PerMinute, max(CFG.RateLimit.Burst, 1))
		}

		static.StaticPath = CFG.StaticPath
		devianter.UserAgent = CFG.UserAgent
	}
}

// forcedThemeCSS returns a block that pins the palette when the instance has
// chosen a theme, or "" for "auto". Light repeats what the prefers-color-scheme
// block already holds; dark repeats :root. Both are emitted after the
// stylesheet so they win on order rather than on !important.
func forcedThemeCSS() string {
	switch CFG.Theme {
	case "light":
		return `
:root{--bg:#f4f1ee;--fg:#1f2421;--fg-strong:#0d100e;--link:#1c6b78;--link-hover:#5a6b00;--edge:#8fbcae;--edge-strong:#258268;--accent:#4d27d6;--surface:#d9e8e1;--surface-sunken:#e8f0ec;--surface-alt:#e2e4f2;--surface-deep:#dbe7ef;--status-bad:#a11;--status-good:#157a3a;--status-mild:#2e8b57;--status-note:#8a007f}`
	case "dark":
		return `
:root{--bg:black;--fg:rgb(234,216,216);--fg-strong:whitesmoke;--link:cadetblue;--link-hover:#d0ff00;--edge:#164e3e;--edge-strong:#258268;--accent:#4d27d6;--surface:#134134;--surface-sunken:#091f19;--surface-alt:#060820;--surface-deep:#011522;--status-bad:red;--status-good:green;--status-mild:seagreen;--status-note:rgb(160,0,147)}`
	}
	return ""
}
