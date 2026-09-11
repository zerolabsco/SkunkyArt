# Units
Maximum file size in megabytes, requires numeric value.<br>
Time units:
* `i` — minutes
* `h` — hours
* `d` — days
* `w` — weeks
* `m` — months
* `y` — years

# Config
* `listen` — IP and port to listen on in the following form: ip:port
* `uri` — Instance URI. Example: `"uri":"/art/"` -> https://skunky.ebloid.ru/art/
* `cache` — Caching system; default is off.
  * `enabled` — Caching system state, requires boolean value
  * `path` — Path to cache directory. It must be writable by the user SkunkyArt
    runs as, and SkunkyArt refuses to start if it is not. The container image
    runs as uid 10000, so a bind-mounted cache needs
    `sudo chown -R 10000:10000 <dir>` on the host.
  * `memcache` — Also keep served media in RAM, on top of the on-disk cache.
    Entries are scored by how often they are requested and dropped once they go
    a round unused. The cache is bounded only by that scoring, so leave it off
    unless you have RAM to spare for your traffic.
  * `lifetime` — Cached file life time, requires numeric value, followed by multiplicative suffix (see Time Units for details)
  * `max-size` — Maximum file size in megabytes
  * `update-interval` — Automatic rotation interval
* `api-cache` — In-memory cache of DeviantArt API responses. Every page,
  feed poll and API call that asks DeviantArt the same question within the
  TTL is answered from memory, and concurrent requests for one thing make
  one upstream call. On by default; DeviantArt bans egress IPs that ask too
  often, so leave it on unless you are debugging.
  * `enabled` — boolean, default true
  * `max-size` — megabytes of response bodies to hold, default 64. Least
    recently used entries are dropped past this.
  * `ttl` — how long a response is reused, in the time units above. Default
    `5i`.
* `static-path` — This setting determines path to static, which will be copied to RAM when SkunkyArt is started. Useless if you're use binary compiled with 'embed' tag.
* `download-proxy` — Outbound proxy used when fetching media from DeviantArt's
  CDN. Leave empty (`""`) unless you actually run a proxy: if this points at
  something that isn't listening, every image 502s while pages still render,
  because only media fetches go through it. Inside a container `127.0.0.1` is
  the container itself, so a host-side proxy must be addressed by service name
  or host IP, not loopback.
* `user-agent` — String, which SkunkyArt uses as UA
* `proxy` — Serve media through this instance instead of linking straight to
  DeviantArt's CDN. Required by `cache`; when off, clients fetch images from
  wixmp directly.
* `nsfw` — Show mature content.
* `hide-ai` — Omit AI-generated deviations (those flagged `[🤖]`) from all
  listings: search, daily deviations, galleries and favourites.

# Setting up reverse proxy
Pretty much business as usual, except for the [`X-Forwarded-Proto`](https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/X-Forwarded-Proto) header setting.

Nginx example configuration:
```apache
server {
    listen 443 ssl;
    server_name skunky.example.com;
    
    # In case of subdomain, use / instend of ((BASE_URL))
    location ((BASE_URL)) {
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Host $host;
        proxy_http_version 1.1;
        proxy_pass http://((IP)):((PORT));
    }
}
```

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
  translation is useful immediately. To add a language, copy `en.json`,translate it,
  and name it after the code.


