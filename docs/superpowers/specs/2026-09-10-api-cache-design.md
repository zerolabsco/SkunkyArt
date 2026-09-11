# API response cache

Issue #8. Roadmap item 1.1.

## Problem

Nothing from DeviantArt's JSON API is cached. Every page view re-fetches
its JSON, concurrent requests for one page each go upstream, and feed
readers and `/api/random` add calls on a schedule. DeviantArt fronts the
API with a WAF that bans egress IPs, so the number of upstream requests,
not their pacing, is what gets an instance blocked. The throttle in
`app/httpclient.go` spaces requests out; it does not reduce them.

## Placement

A caching `http.RoundTripper` in `app/apicache.go`. `InstallDAThrottle`
builds the chain

    devianter -> cache -> throttle -> base transport

so a hit is answered before the throttle's rate limiter and semaphore are
consulted, and a miss goes through them as today. `ProxiedTransport`
builds the same chain over its proxied base, sharing the one cache
instance.

## Scope

Cached: GET requests to host `www.deviantart.com` whose path starts with
`/_puppy/` (with something after it) or `/groups/`, when the response
status is 200.

Passed through, never stored: every other method, host or path. That
includes the session bootstrap (`/_puppy` bare), the homepage CSRF scrape,
avatars and emotes on `a.deviantart.net` and `e.deviantart.net` (issue #9),
and wixmp media. Non-200 responses and transport errors are returned
unchanged and not stored, so a WAF block is not remembered.

## Key

The request URL with the `csrf_token` query parameter removed, otherwise
verbatim. The token changes every twelve hours and would otherwise empty
the cache on each refresh. The guest cookie is the same for every request
and is not part of the key.

## Entry

Status, `Content-Type`, body bytes, expiry time. A hit returns a new
`*http.Response` with those headers, `ContentLength` set, and the body as
a `bytes.Reader`. devianter reads the body and closes it as with a live
response.

## Coalescing

`golang.org/x/sync/singleflight` keyed the same as the cache. Concurrent
misses for one key make one upstream request; every waiter receives the
same stored entry. A miss whose upstream result is not cacheable is still
shared with the waiters of that flight, then not stored.

## Bounds

Bounded by total body bytes. Least-recently-used eviction over a map plus
`container/list`, one mutex. A lookup that finds an expired entry removes
it and reports a miss. An insert that would exceed the bound evicts from
the least recently used end until it fits. A body larger than the bound is
served but not stored. No background goroutine.

## Configuration

New block in `config.json`, beside `cache`:

    "api-cache": {
        "enabled": true,
        "max-size": 64,
        "ttl": "5i"
    }

`enabled` defaults to true. `max-size` is megabytes, default 64. `ttl`
uses the same unit syntax as `cache.lifetime` (`i` minutes, `h` hours,
`d` days, `w` weeks, `m` months, `y` years), default five minutes. The
lifetime parser in `config.go` becomes a function both blocks call; an
unparseable value exits at startup with the same message as today.

One TTL for every endpoint. Per-endpoint values are not in scope.

## Observability

Every hour, one line on stdout: hits, misses, entries, bytes held. Nothing
else. No endpoint.

## Errors

Upstream errors and non-200 statuses pass through unchanged. A body read
error is returned as an error to the caller and nothing is stored.

## Testing

`app/apicache_test.go`, with a fake base `RoundTripper` that counts calls
and returns scripted responses:

- second request for one key makes no upstream call
- a request after expiry refetches
- a non-200 response is not stored; the next request refetches
- `/_puppy` bare, the homepage, and a `.net` host bypass the cache
- two URLs differing only in `csrf_token` share one entry
- inserting past `max-size` evicts the least recently used entry
- a burst of concurrent misses for one key produces one upstream call
- a hit through the full `cached(throttled(base))` chain does not consume
  a throttle slot: hold the semaphore full, make the hit, observe it
  return

## Files

- new `app/apicache.go`, `app/apicache_test.go`
- `app/httpclient.go`: build the chain
- `app/config.go`: the block, defaults, shared lifetime parser
- `config.example.json`, `SETUP.md`
- `go.mod`, `go.sum`: `golang.org/x/sync`
