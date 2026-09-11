package app

import (
	"bytes"
	"container/list"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// apiCache holds DeviantArt API responses so that repeat and concurrent
// requests for one URL cost one upstream call. It sits in front of the
// throttle: a hit never touches DeviantArt or the throttle's budget.
//
// The store is bounded by body bytes with least-recently-used eviction. It
// is safe for concurrent use.
type apiCache struct {
	maxBytes int64
	ttl      time.Duration
	// stale is how long past ttl an entry is kept to be served when upstream
	// is blocked or unreachable. Zero keeps nothing past ttl.
	stale time.Duration
	now   func() time.Time

	mu        sync.Mutex
	entries   map[string]*cacheEntry
	lru       *list.List // front is most recently used
	held      int64
	hits      int64
	misses    int64
	staleHits int64

	// blockedUntil is set when DeviantArt answers with a block. Until then a
	// miss with nothing stale to serve gets blockResp back without an
	// upstream call, so a banned instance stops hammering the WAF.
	blockedUntil time.Time
	blockResp    *cacheEntry

	flight singleflight.Group
}

// blockBackoff is how long upstream is left alone after a block response.
const blockBackoff = time.Minute

// cacheEntry is one buffered response. header is a clone of the upstream
// header; body is the whole body, read once.
type cacheEntry struct {
	key     string
	status  int
	header  http.Header
	body    []byte
	expires time.Time
	elem    *list.Element
}

func newAPICache(maxBytes int64, ttl, stale time.Duration) *apiCache {
	return &apiCache{
		maxBytes: maxBytes,
		ttl:      ttl,
		stale:    stale,
		now:      time.Now,
		entries:  map[string]*cacheEntry{},
		lru:      list.New(),
	}
}

// cacheable reports whether a request is one the cache handles: a GET to
// DeviantArt's API or its group search page. The session bootstrap (/_puppy
// with no path), the homepage, avatars and media all pass through.
func cacheable(req *http.Request) bool {
	if req.Method != http.MethodGet || req.URL.Host != "www.deviantart.com" {
		return false
	}
	p := req.URL.Path
	return (strings.HasPrefix(p, "/_puppy/") && len(p) > len("/_puppy/")) ||
		strings.HasPrefix(p, "/groups/")
}

// cacheKey is the URL without csrf_token, which changes every twelve hours
// and would otherwise empty the cache on each refresh.
func cacheKey(req *http.Request) string {
	u := *req.URL
	q := u.Query()
	q.Del("csrf_token")
	u.RawQuery = q.Encode()
	return u.String()
}

// transport returns a RoundTripper that answers from this cache and sends
// misses to base. Several transports may share one cache.
func (c *apiCache) transport(base http.RoundTripper) http.RoundTripper {
	return &cachedTransport{cache: c, base: base}
}

type cachedTransport struct {
	cache *apiCache
	base  http.RoundTripper
}

// RoundTrip serves a fresh hit from memory. A miss is fetched once per key
// however many callers are waiting, buffered, stored if it is a 200, and
// handed to every waiter as its own response.
//
// When upstream fails or answers with a block, a stale entry is served
// instead if one is still held, so a short ban does not take the popular
// pages down. A block also starts a backoff during which misses with nothing
// stale get the block response back without an upstream call.
func (t *cachedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !cacheable(req) {
		return t.base.RoundTrip(req)
	}
	key := cacheKey(req)
	old, fresh := t.cache.get(key)
	if fresh {
		return old.response(req), nil
	}
	if blocked := t.cache.blockedResponse(); blocked != nil {
		if old != nil {
			t.cache.countStale()
			return old.response(req), nil
		}
		return blocked.response(req), nil
	}

	v, err, _ := t.cache.flight.Do(key, func() (any, error) {
		resp, err := t.base.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		e := &cacheEntry{key: key, status: resp.StatusCode, header: resp.Header.Clone(), body: body}
		switch e.status {
		case http.StatusOK:
			t.cache.put(e)
		case http.StatusForbidden, http.StatusTooManyRequests:
			t.cache.block(e)
		}
		return e, nil
	})
	if err != nil {
		if old != nil {
			t.cache.countStale()
			return old.response(req), nil
		}
		return nil, err
	}
	e, ok := v.(*cacheEntry)
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}
	if e.status != http.StatusOK && old != nil {
		t.cache.countStale()
		return old.response(req), nil
	}
	return e.response(req), nil
}

// block records a block response and starts the backoff.
func (c *apiCache) block(e *cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blockedUntil = c.now().Add(blockBackoff)
	c.blockResp = e
}

// blockedResponse returns the last block response while the backoff runs,
// or nil once it is over.
func (c *apiCache) blockedResponse() *cacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.blockResp != nil && c.now().Before(c.blockedUntil) {
		return c.blockResp
	}
	return nil
}

func (c *apiCache) countStale() {
	c.mu.Lock()
	c.staleHits++
	c.mu.Unlock()
}

// response builds a fresh http.Response over the buffered body, so each
// caller can read and close its own.
func (e *cacheEntry) response(req *http.Request) *http.Response {
	return &http.Response{
		Status:        http.StatusText(e.status),
		StatusCode:    e.status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        e.header.Clone(),
		Body:          io.NopCloser(bytes.NewReader(e.body)),
		ContentLength: int64(len(e.body)),
		Request:       req,
	}
}

// get returns the entry for key and whether it is still fresh. A fresh hit is
// marked most recently used. An entry past ttl but within the stale window is
// returned as not fresh, for the caller to fall back on; one past the stale
// window is dropped.
func (c *apiCache) get(key string) (*cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e := c.entries[key]
	if e == nil {
		c.misses++
		return nil, false
	}
	now := c.now()
	if now.Before(e.expires) {
		c.lru.MoveToFront(e.elem)
		c.hits++
		return e, true
	}
	c.misses++
	if now.Before(e.expires.Add(c.stale)) {
		return e, false
	}
	c.remove(e)
	return nil, false
}

// put stores e, evicting from the least recently used end until it fits. A
// body larger than the whole bound is not stored.
func (c *apiCache) put(e *cacheEntry) {
	size := int64(len(e.body))
	if size > c.maxBytes {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if old := c.entries[e.key]; old != nil {
		c.remove(old)
	}
	for c.held+size > c.maxBytes {
		back, ok := c.lru.Back().Value.(*cacheEntry)
		if !ok {
			break
		}
		c.remove(back)
	}
	e.expires = c.now().Add(c.ttl)
	e.elem = c.lru.PushFront(e)
	c.entries[e.key] = e
	c.held += size
}

// remove drops e. The caller holds mu.
func (c *apiCache) remove(e *cacheEntry) {
	c.lru.Remove(e.elem)
	delete(c.entries, e.key)
	c.held -= int64(len(e.body))
}

// stats reports the counters for the hourly log line. stale counts the
// misses that were answered from an expired entry because upstream failed.
func (c *apiCache) stats() (hits, misses, stale int64, entries int, held int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses, c.staleHits, len(c.entries), c.held
}
