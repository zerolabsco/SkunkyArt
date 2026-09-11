package app

import (
	"os"
	"testing"
	"time"
)

func TestParseLifetimeUnits(t *testing.T) {
	cases := map[string]time.Duration{
		"5i":  5 * time.Minute,
		"2h":  2 * time.Hour,
		"3d":  72 * time.Hour,
		"1w":  7 * 24 * time.Hour,
		"1m":  30 * 24 * time.Hour,
		"1y":  360 * 24 * time.Hour,
		"12i": 12 * time.Minute,
	}
	for in, want := range cases {
		got, err := parseLifetime(in)
		if err != nil || got != want {
			t.Errorf("parseLifetime(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
}

func TestParseLifetimeRejectsBadInput(t *testing.T) {
	for _, in := range []string{"", "5", "5x", "h"} {
		if _, err := parseLifetime(in); err == nil {
			t.Errorf("parseLifetime(%q) accepted, want an error", in)
		}
	}
}

func TestAPICacheDefaults(t *testing.T) {
	if !CFG.APICache.Enabled || CFG.APICache.MaxSize != 64 || CFG.APICache.TTL != "5i" || CFG.APICache.Stale != "1h" {
		t.Errorf("defaults are %+v, want enabled, 64 MB, 5i, stale 1h", CFG.APICache)
	}
}

func TestMediaCacheDefaults(t *testing.T) {
	c := CFG.Cache
	if !c.Enabled || c.Lifetime != "1w" || c.MaxSize != 200 || c.MemCache {
		t.Errorf("defaults are %+v, want enabled, 1w, 200 MB, memcache off", c)
	}
}

// withScratchConfig points CFG at a config path under a temporary directory,
// with the media cache off so no rotation goroutine starts, and restores the
// whole configuration afterwards.
func withScratchConfig(t *testing.T, path string, explicit bool) {
	t.Helper()
	cfg, limiter, exp := CFG, daLimiter, cfgExplicit
	CFG.cfg = path
	CFG.Cache.Enabled = false
	cfgExplicit = explicit
	t.Cleanup(func() { CFG, daLimiter, cfgExplicit = cfg, limiter, exp })
}

func TestExecuteConfigRunsWithoutTheDefaultFile(t *testing.T) {
	withScratchConfig(t, t.TempDir()+"/config.json", false)
	msgs := captureExit(t)

	ExecuteConfig()

	if len(*msgs) != 0 {
		t.Errorf("exit called with %v; want a start on the built-in defaults", *msgs)
	}
	if CFG.Listen != "127.0.0.1:3003" || CFG.Nsfw {
		t.Errorf("defaults not in effect: listen %q nsfw %v", CFG.Listen, CFG.Nsfw)
	}
}

func TestExecuteConfigExitsOnAMissingExplicitFile(t *testing.T) {
	withScratchConfig(t, t.TempDir()+"/named.json", true)
	msgs := captureExit(t)

	ExecuteConfig()

	if len(*msgs) == 0 {
		t.Error("a missing file named with -c started the instance, want an exit")
	}
}

func TestUpstreamDefaults(t *testing.T) {
	if CFG.Upstream.MinIntervalMS != 400 || CFG.Upstream.MaxConcurrent != 2 {
		t.Errorf("defaults are %+v, want 400 ms and 2 in flight", CFG.Upstream)
	}
}

// TestUpstreamConfigSetsTheThrottle pins that the file's values reach the
// throttle: the tunables used to be source constants.
func TestUpstreamConfigSetsTheThrottle(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.json"
	if err := os.WriteFile(path, []byte(`{"upstream": {"min-interval-ms": 1500, "max-concurrent": 1}}`), 0600); err != nil {
		t.Fatal(err)
	}
	withScratchConfig(t, path, true)
	interval, concurrent := daMinInterval, daMaxConcurrent
	t.Cleanup(func() { daMinInterval, daMaxConcurrent = interval, concurrent })
	captureExit(t)

	ExecuteConfig()

	if daMinInterval != 1500*time.Millisecond || daMaxConcurrent != 1 {
		t.Errorf("throttle is %v / %d, want 1.5s / 1 from the file", daMinInterval, daMaxConcurrent)
	}
}
