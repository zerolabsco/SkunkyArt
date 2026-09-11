# API

JSON endpoints under `/api`. Read-only, no authentication, no state.

Every response is `application/json`. Errors are `{"error":"..."}` with a real
HTTP status — 400 for a bad request, 403 when the instance forbids the content,
404 for an unknown endpoint or deviation, 502 when DeviantArt fails.

An instance's settings apply here exactly as they do to the pages. `hide-ai`
omits AI work from listings, `nsfw` gates mature content, and both are decided
by the same predicate the HTML listing uses, so the API cannot serve what the
site withholds.

Media URLs point back at this instance when `proxy` is on, so a consumer never
has to talk to wixmp itself.

## GET /api/instance

Version and the instance's settings.

    {"version":"1.4.0","settings":{"nsfw":false,"proxy":true,"hide-ai":false,"theme":"auto"}}

## GET /api/search

Parameters:

* `q` — required. The search query.
* `type` — `a` art (default), `t` tag, `g` gallery, `f` favourites.
* `usr` — required for `g` and `f`; the user whose gallery or favourites to read.
* `p` — page number, default 0.

    {
      "query": "fox",
      "type": "a",
      "page": 0,
      "results": [
        {
          "id": 123456789,
          "title": "A Title",
          "author": "alice",
          "url": "https://instance/post/alice/a-title-123456789",
          "published": "2026-01-02T15:04:05Z",
          "nsfw": false,
          "ai": false,
          "daily_deviation": false,
          "tags": ["cats"],
          "preview": "https://instance/media/file/...",
          "fullview": "https://instance/media/file/...",
          "favourites": 7,
          "views": 99
        }
      ]
    }

`results` is always an array; an empty page is `[]`, never `null`.

## GET /api/post/{author}/{postname}

One deviation. `postname` carries the numeric id the way the site's own URLs do,
e.g. `a-title-123456789`.

Returns the fields above plus `description`, `downloads`, `filesize`, `width`
and `height`.

Gated on `nsfw` only, matching the page: `hide-ai` omits AI work from listings,
and a reader following a direct link to one still gets it.

## GET /api/random

A random artwork's media — the image itself, not JSON. The pick is made
among the current daily deviations, which the instance already caches, so
the call costs no extra upstream request. Honours `nsfw` and `hide-ai`.
