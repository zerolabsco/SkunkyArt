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
	now      func() time.Time

	mu      sync.Mutex
	entries map[string]*cacheEntry
	lru     *list.List // front is most recently used
	held    int64
	hits    int64
	misses  int64

	flight singleflight.Group
}

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

func newAPICache(maxBytes int64, ttl time.Duration) *apiCache {
	return &apiCache{
		maxBytes: maxBytes,
		ttl:      ttl,
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

// RoundTrip serves a hit from memory. A miss is fetched once per key however
// many callers are waiting, buffered, stored if it is a 200, and handed to
// every waiter as its own response.
func (t *cachedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !cacheable(req) {
		return t.base.RoundTrip(req)
	}
	key := cacheKey(req)
	if e := t.cache.get(key); e != nil {
		return e.response(req), nil
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
		if e.status == http.StatusOK {
			t.cache.put(e)
		}
		return e, nil
	})
	if err != nil {
		return nil, err
	}
	e, ok := v.(*cacheEntry)
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}
	return e.response(req), nil
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

// get returns the live entry for key, marking it most recently used, or nil.
// An expired entry is dropped on the way out.
func (c *apiCache) get(key string) *cacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()

	e := c.entries[key]
	if e == nil {
		c.misses++
		return nil
	}
	if !c.now().Before(e.expires) {
		c.remove(e)
		c.misses++
		return nil
	}
	c.lru.MoveToFront(e.elem)
	c.hits++
	return e
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

// stats reports the counters for the hourly log line.
func (c *apiCache) stats() (hits, misses int64, entries int, held int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses, len(c.entries), c.held
}
