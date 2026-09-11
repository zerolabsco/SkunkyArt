package app

import (
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
	if !CFG.APICache.Enabled || CFG.APICache.MaxSize != 64 || CFG.APICache.TTL != "5i" {
		t.Errorf("defaults are %+v, want enabled, 64 MB, 5i", CFG.APICache)
	}
}

func TestMediaCacheDefaults(t *testing.T) {
	c := CFG.Cache
	if !c.Enabled || c.Lifetime != "1w" || c.MaxSize != 200 || c.MemCache {
		t.Errorf("defaults are %+v, want enabled, 1w, 200 MB, memcache off", c)
	}
}
