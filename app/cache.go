package app

// TODO: implement JSON caching and clean up the code.

import (
	"crypto/sha1" //nolint:gosec // G505: SHA-1 is a cache-key hash here, not a security primitive
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type file struct {
	Score   int
	Content []byte
}

// tempFS is the in-memory media cache, guarded by mx. A plain Mutex rather than
// an RWMutex on purpose: every operation here mutates something (a read bumps
// Score), and the previous code took an RLock to write, which is not exclusive.
var tempFS = make(map[[20]byte]*file)
var mx sync.Mutex

// memGet returns the cached body for key and raises its score so that popular
// entries outlive the janitor, or nil when the entry is absent or still empty.
func memGet(key [20]byte) []byte {
	mx.Lock()
	defer mx.Unlock()

	f := tempFS[key]
	if f == nil || f.Content == nil {
		return nil
	}
	f.Score += 2
	return f.Content
}

// memPut caches body under key. An empty body is not cached, so a failed fetch
// cannot poison the cache with a zero-length image.
func memPut(key [20]byte, body []byte) {
	if len(body) == 0 {
		return
	}

	mx.Lock()
	defer mx.Unlock()
	tempFS[key] = &file{Content: body}
}

// InitMemCacheJanitor ages the in-memory cache forever, dropping entries whose
// score has run out. Run it in its own goroutine, once, and only when memcache
// is enabled.
//
// One loop ages the whole map. The previous design started a goroutine per
// cached file, each looping until its own entry was evicted, and each touching
// the map without holding mx — a concurrent map read and write, which the Go
// runtime treats as a fatal error that recover cannot catch.
func InitMemCacheJanitor() {
	for {
		time.Sleep(1 * time.Minute)
		ageMemCache()
	}
}

// ageMemCache runs one round of aging: every entry loses a point, and entries
// that are already out of points are dropped. An entry starts at zero, so a body
// nothing asks for again is gone within a round.
func ageMemCache() {
	mx.Lock()
	defer mx.Unlock()

	for k, f := range tempFS {
		if f.Score <= 0 {
			delete(tempFS, k)
			continue
		}
		f.Score--
	}
}

// mediaSubdomain matches the one hostname label wixmp media URLs vary: a hex
// string, sometimes with dashes. Anything outside that set is rejected rather
// than escaped, because this label is what selects the host to fetch from.
var mediaSubdomain = regexp.MustCompile(`^[a-zA-Z0-9-]+$`)

// blurConstraint reports the minimum blur radius a wixmp media token demands, or
// 0 if it demands none.
//
// DeviantArt signs mature-content media with a watermark-service token whose obj
// carries a "blur": ">=N" constraint. wixmp then rejects a plain /v1/fit
// transform with 403 unless it includes a matching blur_N operation, so this is
// what tells buildMediaURL when to add one. A token it cannot parse yields 0,
// leaving the URL untouched — the same behaviour as before this check existed.
func blurConstraint(token string) int {
	// A JWT is header.payload.signature; the claims are the middle segment,
	// base64url-encoded without padding.
	parts := strings.SplitN(token, ".", 3)
	if len(parts) < 2 {
		return 0
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0
	}

	var claims struct {
		Obj [][]struct {
			Blur string `json:"blur"`
		} `json:"obj"`
	}
	if json.Unmarshal(payload, &claims) != nil ||
		len(claims.Obj) == 0 || len(claims.Obj[0]) == 0 {
		return 0
	}

	// The constraint reads like ">=10"; take its digits as the radius, which is
	// the minimum the token accepts.
	n := 0
	for _, c := range claims.Obj[0][0].Blur {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

// addBlurToTransform inserts a blur_n operation into a wixmp /v1/fit transform,
// turning e.g. w_1280,h_1920 into w_1280,h_1920,blur_n. It returns path
// unchanged when it carries no /v1/fit transform (GIFs and oversized originals
// are served without one) or already blurs.
func addBlurToTransform(path string, n int) string {
	const marker = "/v1/fit/"
	start := strings.Index(path, marker)
	if start < 0 {
		return path
	}
	ops := start + len(marker)
	end := strings.IndexByte(path[ops:], '/')
	if end < 0 {
		return path
	}
	end += ops
	if strings.Contains(path[ops:end], "blur_") {
		return path
	}
	return path[:end] + ",blur_" + strconv.Itoa(n) + path[end:]
}

// buildMediaURL returns the wixmp CDN URL for one media item, reporting false
// when subdomain is not a bare hostname label.
//
// subdomain and path arrive already percent-decoded from the request path, so
// they can carry the characters that end a host. Concatenated into a URL string,
// a subdomain of "x@attacker.example#" reparses as host attacker.example, with
// "images-wixmp-x" demoted to userinfo and the intended host to a fragment —
// pointing the fetch at whatever the caller names, including addresses reachable
// only from the instance itself.
func buildMediaURL(subdomain, path, token string) (string, bool) {
	if !mediaSubdomain.MatchString(subdomain) {
		return "", false
	}

	// Mature media is signed with a token that only authorizes a blurred render;
	// without a matching blur op in the transform wixmp answers 403. Add the op
	// the token demands, and only then, so unconstrained media is left as-is.
	if n := blurConstraint(token); n > 0 {
		path = addBlurToTransform(path, n)
	}

	// Fields rather than concatenation: String escapes the path, so a decoded
	// "#" or "?" in it stays part of the path instead of ending it. The host is
	// checked above rather than escaped, because url.URL passes it through
	// verbatim.
	u := url.URL{
		Scheme: "https",
		Host:   "images-wixmp-" + subdomain + ".wixmp.com",
		Path:   "/" + path,
	}
	if token != "" {
		u.RawQuery = url.Values{"token": {token}}.Encode()
	}
	return u.String(), true
}

// DownloadAndSendMedia proxies one image from DeviantArt's wixmp CDN to the
// client, serving it from the on-disk or in-memory cache when enabled. It
// responds 403 when proxying is turned off for this instance.
func (s skunkyart) DownloadAndSendMedia(subdomain, path string) {
	mediaURL, ok := buildMediaURL(subdomain, path, s.Args.Get("token"))
	if !ok {
		s.ReturnHTTPError(400)
		return
	}

	var response []byte

	switch {
	case CFG.Cache.Enabled:
		key := sha1.Sum([]byte(subdomain + path)) //nolint:gosec // G401: cache-key hash, not a security primitive
		filePath := cacheFilePath(key)

		if CFG.Cache.MemCache {
			if cached := memGet(key); cached != nil {
				response = cached
				break
			}
		}

		body, ok := s.loadOrFetchMedia(filePath, mediaURL)
		if !ok {
			// loadOrFetchMedia has already written the error response.
			return
		}
		response = body

		if CFG.Cache.MemCache {
			memPut(key, response)
		}
	case CFG.Proxy:
		dwnld := Download(mediaURL)
		if dwnld.Status != 200 {
			s.ReturnHTTPError(dwnld.Status)
			return
		}
		response = dwnld.Body
	default:
		s.Writer.Header().Del("Cache-Control")
		s.Writer.WriteHeader(403)
		response = []byte("Sorry, butt proxy on this instance are disabled.")
	}

	_, _ = s.Writer.Write(response)
}

// loadOrFetchMedia returns the media body for filePath, preferring the on-disk
// cache and falling back to fetching mediaURL, which it then writes back to the
// cache. It reports false when it has already written an error response, so the
// caller must not write anything further.
func (s skunkyart) loadOrFetchMedia(filePath, mediaURL string) ([]byte, bool) {
	// filePath is built from a SHA-1 of the request, not from user input, so it
	// cannot escape the cache directory.
	if f, err := os.Open(filePath); err == nil { //nolint:gosec // G304: path is a hash, not user-controlled
		defer func() { try(f.Close()) }()

		if body, err := io.ReadAll(f); err == nil {
			return body, true
		} else {
			// An unreadable cache entry is not fatal; re-fetch it instead.
			try(err)
		}
	}

	dwnld := Download(mediaURL)
	if dwnld.Status != 200 || !strings.HasPrefix(dwnld.Headers.Get("Content-Type"), "image") {
		s.ReturnHTTPError(dwnld.Status)
		return nil, false
	}

	try(os.WriteFile(filePath, dwnld.Body, 0600))
	return dwnld.Body, true
}

// InitCacheSystem runs the cache rotation loop forever, evicting files past
// their lifetime and emptying the cache when it outgrows max-size. Run it in its
// own goroutine.
func InitCacheSystem() {
	c := &CFG.Cache
	for {
		dir, err := os.ReadDir(c.Path)
		if err != nil {
			if os.IsNotExist(err) {
				try(os.Mkdir(c.Path, 0700))
				continue
			}
			println(err.Error())
		}

		var total int64
		for _, file := range dir {
			fileName := c.Path + "/" + file.Name()
			fileInfo, err := file.Info()
			try(err)

			if c.Lifetime != "" {
				now := time.Now().UnixMilli()

				// Sys() is platform-specific and only documented to be a
				// *syscall.Stat_t on unix; skip rotation rather than panic
				// if the filesystem reports something else.
				if stat, ok := fileInfo.Sys().(*syscall.Stat_t); ok {
					if statTime(stat)+lifetimeParsed <= now {
						try(os.RemoveAll(fileName))
					}
				}
			}

			total += fileInfo.Size()
			// if c.MaxSize != 0 && fileInfo.Size() > c.MaxSize {
			// 	try(os.RemoveAll(fileName))
			// }
		}

		if c.MaxSize != 0 && total > c.MaxSize {
			try(os.RemoveAll(c.Path))
			try(os.Mkdir(c.Path, 0700))
		}

		time.Sleep(time.Second * time.Duration(c.UpdateInterval))
	}
}

// cacheFilePath is where the body cached under key lives on disk.
func cacheFilePath(key [20]byte) string {
	return CFG.Cache.Path + "/" + hex.EncodeToString(key[:])
}

// cachedBody returns the body stored under key, from memory when memcache is
// on and otherwise from disk, or nil when there is none.
func cachedBody(key [20]byte) []byte {
	if CFG.Cache.MemCache {
		if body := memGet(key); body != nil {
			return body
		}
	}
	// The path is a hash of the key, not user input.
	body, err := os.ReadFile(cacheFilePath(key)) //nolint:gosec // G304
	if err != nil || len(body) == 0 {
		return nil
	}
	if CFG.Cache.MemCache {
		memPut(key, body)
	}
	return body
}

// storeBody writes body under key to disk and, when memcache is on, memory.
func storeBody(key [20]byte, body []byte) {
	try(os.WriteFile(cacheFilePath(key), body, 0600))
	if CFG.Cache.MemCache {
		memPut(key, body)
	}
}
