# Roadmap

Findings from a full review on 2026-09-10, ordered for work. Each item is
sized to be one gitbay issue and one merge request. Sizes: S is under an
hour, M is a few hours, L needs a design pass first.

Verified on: local build from `main` at 028903a, live instance
art.krz.sh (v1.4.0), devianter v0.3.4. DeviantArt's WAF blocks the
reviewing machine's egress IP, so DA-backed pages were checked on the live
instance.

Existing gitbay issues: #1 cleanup, #2 search filters, #3 description
parsing, #4 instance checker, #5 Makefile, #6 emote bug. They are mapped
below where they overlap.

## Ordering principle

Frontend proxies get blocked upstream. The existing throttle
(`app/httpclient.go`) spreads requests out; it does not reduce them. Tier 1
reduces them. Tier 0 lands first because everything after it needs CI.
Tier 2's escaping fix lands before other template work because it touches
every template and builder.

## Upstream cost today

Nothing from DeviantArt's JSON API is cached. Only wixmp media is cached,
and only when `cache.enabled` is on, which defaults to off.

| Page | Upstream calls per view |
|---|---|
| Post | 2 API (deviation, comments) + 1 avatar per commenter |
| User about | 2 API (gruser, comments) + avatars |
| Gallery, favourites, search, DD | 1 API, plus thumbnails when the media cache is off |
| `/api/random` | up to 3 searches |
| Atom feed | 1 API per poll, per reader |

Avatars come from a.deviantart.net and are fetched fresh on every view.
Concurrent requests for the same page each go upstream. No response carries
`Cache-Control`. There is no robots.txt and no per-client rate limit.

## Tier 0: process

### 0.1 CI on merge requests (S, #7)

No pipeline runs `go vet`, `go test` or `golangci-lint`. The only workflow
is the GitHub release build; gitbay has zero builds. `.golangci.yml` exists
but is never run.

Fix: add a gitbay build that runs vet, test with `-race`, and golangci-lint
on every MR. Check `gitbay build --help` and `ssh git@gitbay.org help build`
for the pipeline format. Optionally mirror the same job to
`.github/workflows/` so the GitHub mirror shows status too.

Verify: an MR with a failing test shows a failed build.

## Tier 1: reduce upstream requests

### 1.1 Cache DeviantArt API responses (L, #8)

Add an in-memory cache in front of every devianter call: DD, search,
deviation, gruser, gallery, favourites, comments. Keyed by endpoint plus
arguments. Bounded by entry count or bytes. Singleflight so concurrent
requests for one key share a single upstream call. Per-endpoint TTLs, in
config with defaults on the order of: DD and search a few minutes,
deviations longer, comments shorter.

This is the item the TODO at `app/cache.go:3` names. It makes feed polling
and `/api/random` close to free.

Design first: cache interface, key derivation, TTL config keys, eviction,
what the hit/miss log looks like. Write the spec to
`docs/superpowers/specs/` and review before code.

Files: new `app/apicache.go`, call sites in `app/wrapper.go`,
`app/api.go`, `app/api_json.go`, config in `app/config.go`, docs in
`SETUP.md`.

Verify: unit tests for TTL expiry, singleflight, and bounded size. Two
sequential requests for one page make one upstream call.

### 1.2 Cache avatars and emotes, add Cache-Control (M, #9)

`Emojitar` in `app/wrapper.go` fetches from a.deviantart.net or
e.deviantart.net on every request and never stores the result. Route it
through the same disk and memory cache path as `DownloadAndSendMedia`.

Add `Cache-Control` headers: long `max-age` with `immutable` on
`/media/file` (token-signed wixmp URLs do not change), a day on avatars and
emotes, a day on `/stylesheet` and `/favicon.ico`, a short `max-age` on HTML
matching the API cache TTL.

Depends on: nothing, but pairs with 1.1.

Verify: second avatar request is served without an upstream fetch; headers
present in `curl -I` output.

### 1.3 robots.txt and per-client rate limit (S, #10)

Serve `/robots.txt` disallowing `/search`, `/api`, `/group_user`,
`/media` and any path with `?p=`. Add a per-client-IP token bucket ahead of
the upstream throttle so one crawler cannot consume the whole DA budget and
turn it into latency for everyone else. Honour `X-Forwarded-For` only when
the request came from a configured trusted proxy.

Files: `app/router.go`, new `app/ratelimit.go`, `app/config.go`,
`SETUP.md`.

Verify: test that N+1 requests from one address within the window get 429.

### 1.4 Fewer calls per page (M, #11)

Post view is two API calls because comments are fetched inline. Move
comments behind a link (`/post/{author}/{name}/comments` or `?comments=1`)
so the default post view is one call. Same for the user about page.

Rebuild `/api/random` to pick from cached DD or search results (1.1) instead
of issuing up to three fresh searches per hit.

Depends on: 1.1.

Verify: post view makes exactly one upstream call in a test with a fake
transport.

### 1.5 Media cache on by default (S, #12)

A proxying instance with no cache re-fetches every image from wixmp on every
view. Set `cache.enabled: true` in the built-in defaults in `app/config.go`
and in `config.example.json`, with a sane `lifetime` and `max-size`. Keep
`memcache` off. Document the change in `SETUP.md`.

Verify: fresh start with no config writes to the cache directory.

## Tier 2: security and correctness

### 2.1 Escape template output (M, #13)

`app/util.go` imports `text/template`. Nothing interpolated is escaped: the
search query in `static/html/search.htm` and `head.htm`, and every DA
username, title and description written by `DeviationList`,
`ParseComments`, `BuildUserPlate` and `ParseDescription`. The CSP blocks
scripts but not markup, inline styles, meta refresh or injected forms; any
title containing `<` corrupts the page.

Fix: switch to `html/template`; wrap the pre-built HTML fragments in
`template.HTML`; escape strings in the Go builders with
`html.EscapeString` and attribute-escape URLs. Add tests that a query and a
title containing `"><b>` render as text.

Land before other template work.

### 2.2 Restore the user About branch (S, #14)

`app/wrapper.go:35` has `else if false`, inherited from upstream commit
048bb47. Registration date, interests, social links and bio never render for
users. Find out why it was disabled (likely a devianter struct change),
restore the branch, add a test with a fixture.

### 2.3 Group search pagination (S, #15)

`app/wrapper.go:274` increments the page and requests offset `10*page`, so
page two starts at result 20 and results 10 to 19 are never shown. The nav
bar also shows the incremented number. Use `10*(page-1)` and do not mutate
`s.Page` before `NavBase`.

### 2.4 Emojitar writes a body after 404 (S, #16)

`app/wrapper.go:344` lacks a `return` after `ReturnHTTPError(404)`.

### 2.5 Valid Atom feed (S, #17)

`DeviationList` in `app/parsers.go` emits no feed-level `<id>` or
`<updated>`, bare integer entry ids, RFC 1123 `<published>` instead of RFC
3339, and `media:thumbinal`. Verified on the live feed. Fix all five and add
a test that parses the output with an Atom library or checks the required
elements.

### 2.6 `-c` bounds check (S, #18)

`app/cli.go:29` checks `len(a) >= 2` instead of `n+1 < len(a)`;
`skunkyart -x -c` panics.

### 2.7 Sanitize the 502 page (S, #19)

`Error` in `app/util.go` writes the upstream error, including the full
CloudFront block page, into an `<h3>` unescaped. Truncate to one line and
escape. Folds into 2.1 if done together.

### 2.8 Parse templates once (S, #20)

`ExecuteTemplate` calls `ParseFS` on every request. Parse at startup;
supply the per-request `T` function through the data struct or a per-request
`Funcs` clone. Template errors then fail at boot instead of as 500s.

## Tier 3: config, docs, i18n

### 3.1 Config-less start and default alignment (S, #21)

`ExecuteConfig` exits if `config.json` is missing even though defaults
exist. Start with defaults when no `-c` is given and the default file is
absent. Align the built-in `nsfw: true` with the example's `false`, or
document why they differ.

### 3.2 Cache documentation (S, #22)

`SETUP.md`: `update-interval` is in seconds (the example scans every 5s);
the `d` unit works but is unlisted; `y` is 360 days; exceeding `max-size`
deletes the whole cache directory; `lifetime: null` in the example.

### 3.3 API and search type docs (S, #23)

`API.md` says `t` is text search; devianter defines it as tag. The
"Folders" option in `static/html/gruser.htm` maps to `f`, which is
favourites. Fix the doc and rename or remove the option.

### 3.4 i18n coverage (M, #24)

Go-built HTML hardcodes English: comment headers, "In reply to",
pagination, folder and content headings, "No results", "[ TEXT ]".
`gruser.htm` section headings and the index blurb are untranslated. Every
template declares `lang="en"`. `Languages()` in `app/i18n.go` is unused.
Move the strings into the catalogues, set `lang` from the resolved
language, and either use or remove `Languages()`.

### 3.5 systemd unit (S, #25)

`services/skunkyart.example.service` uses `Directory=` (not a valid key),
placeholder paths, and says it was never tested. Write a working unit with
`WorkingDirectory`, `User`, `DynamicUser` or a dedicated user,
`NoNewPrivileges`, and `Restart=on-failure`. Test it once on a Linux host.

### 3.6 SETUP.md structure (S, #26)

The nginx section sits between config keys; `theme` and `language` come
after it. Reorder: config keys, units, reverse proxy.

### 3.7 README (S, #27)

Add: endpoints and what they do, running the binary without Docker with
the service files, what `REDIRECTS.md` is for (redirector rules), and a
screenshot.

## Tier 4: UI

### 4.1 Viewport and mobile CSS (S, #28)

`static/html/head.htm` and `index.htm` use `initial-scale=0.4` and
`height=device-height`; `skunky.css` then compensates with
`* { font-size: 120% }` in portrait. Use `width=device-width,
initial-scale=1` and adjust the portrait rules to match. Check on a phone
width before and after.

### 4.2 Accessibility (S, #29)

Listing and avatar images in `DeviationList`, `ParseComments` and
`BuildUserPlate` have no `alt`. The post page has no heading element for
the title. Add both.

### 4.3 Index stylesheet (S, #30)

`static/html/index.htm` carries an inline stylesheet duplicating layout
rules. Move it into `skunky.css`.

## Tier 5: identity and reach

### 5.1 One canonical forge (S, #31)

Origin and issues are on gitbay; releases, the image, Dependabot, the
instances.json fetch at `app/util.go:64`, the About page "Report an issue"
link, the index source link, and the `--add-instance` exit message all
point at GitHub. Decide which is canonical. If gitbay: fetch
`instances.json` from gitbay, point the links there, keep the GitHub mirror
for the image build only. If GitHub stays the public face: say so in the
README and leave the links.

### 5.2 Instance checker (M, issue #4, #32)

A scheduled job that fetches each instance's `/api/instance` and marks dead
ones in `INSTANCES.md`, or a CI job that fails when one is down.

### 5.3 LibRedirect listing (S, #33)

`REDIRECTS.md` already describes the URL mapping. Check whether LibRedirect
lists SkunkyArt with the dead upstream instances and submit the fork and
art.krz.sh. This is the cheapest way to get users.

### 5.4 Makefile and binary releases (S, issue #5, #34)

Targets for build with the embed tag and version stamp, test, lint.
Publish binaries alongside the image on release tags.

### 5.5 Existing issues

- #1 cleanup: the TODOs at `app/parsers.go:222` and `app/cache.go:3`; the
  second is 1.1. `sendMedia` in `app/api.go` duplicates `ParseMedia`'s
  magic string offsets (`[21:]`, `dot+11`); share one function.
- #2 search filters: blocked on what devianter exposes; scope after 1.1.
- #3 description parsing: `ParseDescription` drops `header-two`, ordered
  lists and nested styles. Needs fixtures from real descriptions.
- #6 emote bug: the `a.Val[8:9] == "e"` and `[37:len-4]` offsets in the
  HTML branch of `ParseDescription`. Parse the URL instead of slicing.

## Stacked MR order

Each MR branches from the previous one's tip and is merged in order.

1. `ci/pipeline` (0.1)
2. `fix/escape-templates` (2.1 + 2.7)
3. `feat/api-cache` (1.1, after its spec is approved)
4. `feat/avatar-cache-headers` (1.2)
5. `feat/robots-ratelimit` (1.3)
6. `feat/fewer-calls` (1.4)
7. `chore/cache-default-on` (1.5)
8. `fix/small-bugs` (2.2, 2.3, 2.4, 2.6, 2.8; one MR, one commit each)
9. `fix/atom-feed` (2.5)
10. `docs/config-and-setup` (3.1, 3.2, 3.3, 3.6)
11. `feat/i18n-coverage` (3.4)
12. `chore/services-readme` (3.5, 3.7)
13. `ui/viewport-a11y` (4.1, 4.2, 4.3)
14. `chore/canonical-forge` (5.1)
15. 5.2 through 5.5 as independent MRs off `main`

Items 8 through 15 do not depend on the cache stack and can be reordered or
interleaved when the cache work stalls on design.
