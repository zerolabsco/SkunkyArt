# Config

SkunkyArt reads `config.json` from the working directory, or the file named
with `-c`. The default file is optional: without it the built-in defaults
below apply. A file named with `-c` must exist.

* `listen` — IP and port to listen on, `ip:port`. Default `127.0.0.1:3003`.
* `uri` — Instance URI. Example: `"uri":"/art/"` -> https://skunky.example.com/art/.
  Default `/`.
* `cache` — On-disk cache for proxied media, avatars and emotes. On by
  default. A proxying instance with no cache re-fetches every image from
  wixmp on every view, which is the pattern that gets an egress IP blocked.
  * `enabled` — boolean, default true.
  * `path` — Cache directory, default `cache`. It must be writable by the
    user SkunkyArt runs as, and SkunkyArt refuses to start if it is not. The
    container image runs as uid 10000, so a bind-mounted cache needs
    `sudo chown -R 10000:10000 <dir>` on the host.
  * `memcache` — Also keep served media in RAM, on top of the on-disk cache.
    Entries are scored by how often they are requested and dropped once they
    go a round unused. The cache is bounded only by that scoring, so leave it
    off unless you have RAM to spare for your traffic. Default false.
  * `lifetime` — How long a cached file is kept, in the time units below.
    Default `1w`.
  * `max-size` — Cache size cap in megabytes, default 200. When the cache
    grows past it the whole directory is emptied, not trimmed.
  * `update-interval` — Seconds between rotation passes, default 1. Each
    pass reads the whole cache directory, so raise it on a large cache.
* `api-cache` — In-memory cache of DeviantArt API responses. Every page,
  feed poll and API call that asks DeviantArt the same question within the
  TTL is answered from memory, and concurrent requests for one thing make
  one upstream call. On by default; DeviantArt bans egress IPs that ask too
  often, so leave it on unless you are debugging.
  * `enabled` — boolean, default true.
  * `max-size` — Megabytes of response bodies to hold, default 64. Least
    recently used entries are dropped past this.
  * `ttl` — How long a response is reused, in the time units below. Default
    `5i`.
  * `stale` — How long past `ttl` a response is kept to be served when
    DeviantArt fails or blocks the instance, so a short ban does not take
    the daily deviations, popular searches and feeds down. Default `1h`;
    `0i` keeps nothing past `ttl`. After a block the instance also leaves
    DeviantArt alone for a minute instead of retrying every request.
* `rate-limit` — Per-client budget for page, feed and API requests, so one
  crawler cannot spend the whole upstream budget. Media, avatars and static
  files are not counted. Over budget answers 429 with `Retry-After`.
  Behind a reverse proxy the client is taken from the rightmost
  `X-Forwarded-For` entry, but only when the connection itself comes from a
  loopback or private address; a direct client's header is ignored.
  * `per-minute` — Sustained requests per minute per client, default 60.
    `0` turns the limit off.
  * `burst` — How many requests a client can make at once before the rate
    applies, default 20.
* `static-path` — Directory of templates, styles and catalogues, read into
  memory at startup. Default `static`. Ignored by a binary built with the
  `embed` tag.
* `download-proxy` — Outbound proxy used when fetching media from DeviantArt's
  CDN. Leave empty (`""`) unless you actually run a proxy: if this points at
  something that isn't listening, every image 502s while pages still render,
  because only media fetches go through it. Inside a container `127.0.0.1` is
  the container itself, so a host-side proxy must be addressed by service name
  or host IP, not loopback.
* `user-agent` — The User-Agent SkunkyArt sends to DeviantArt.
* `proxy` — Serve media through this instance instead of linking straight to
  DeviantArt's CDN. Required by `cache`; when off, clients fetch images from
  wixmp directly. Default true.
* `nsfw` — Show mature content. Default false.
* `hide-ai` — Omit AI-generated deviations (those flagged `[🤖]`) from all
  listings: search, daily deviations, galleries and favourites. Default false.
* `theme` — Palette for the interface. `auto` (default) serves the dark theme
  and lets a visitor whose system asks for light get the light one, through
  `prefers-color-scheme` — no cookie, no query string, no JavaScript. `dark` or
  `light` pins one for everybody. An unrecognised value stops startup rather
  than quietly falling back.
* `language` — Interface language. `auto` (default) reads the browser's own
  `Accept-Language` header, which it sends on every request anyway, so nothing
  extra is stored or asked for. A language code (`en`, `es`) pins one for
  everybody. An unknown code falls back to English rather than refusing to
  start, since a missing catalogue is a worse reason to be down than to be in
  the wrong language.

  Catalogues live in `static/lang/*.json`, keyed by the strings the templates
  ask for. `en.json` is the reference and is always complete; a catalogue that
  is missing a key shows the English for that one string, so a partial
  translation is useful immediately. To add a language, copy `en.json`,
  translate it, and name it after the code.

# Time units

A number followed by one of:

* `i` — minutes
* `h` — hours
* `d` — days
* `w` — weeks
* `m` — months (30 days)
* `y` — years (360 days)

# robots.txt

`/robots.txt` is served automatically. It disallows search, the API, user
and group pages, media and paginated URLs, and asks for a 10 second crawl
delay; the index, daily deviations and posts stay crawlable.

# Setting up reverse proxy

Pretty much business as usual, except for the [`X-Forwarded-Proto`](https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/X-Forwarded-Proto) header setting.

Nginx example configuration:
```apache
server {
    listen 443 ssl;
    server_name skunky.example.com;

    # In case of subdomain, use / instead of ((BASE_URL))
    location ((BASE_URL)) {
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Host $host;
        proxy_http_version 1.1;
        proxy_pass http://((IP)):((PORT));
    }
}
```
