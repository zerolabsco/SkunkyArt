package app

import (
	"encoding/json"
	"math/rand"
	"strconv"
	"strings"

	"github.com/krazywarez/devianter"
)

// API serves the JSON endpoints under /api, backed by the request its main
// field points at.
type API struct {
	main *skunkyart
}

type info struct {
	Version  string         `json:"version"`
	Settings settingsParams `json:"settings"`
}

// Info responds with this instance's version and its proxy/NSFW/hide-ai settings.
func (a API) Info() {
	json, err := json.Marshal(info{
		Version: a.main.Version,
		Settings: settingsParams{
			Nsfw:   CFG.Nsfw,
			Proxy:  CFG.Proxy,
			HideAI: CFG.HideAI,
			Theme:  CFG.Theme,
		},
	})
	try(err)
	_, _ = a.main.Writer.Write(json)
}

// Error responds with a JSON error body and the given HTTP status.
func (a API) Error(description string, status int) {
	a.main.Writer.Header().Del("Cache-Control")
	a.main.Writer.WriteHeader(status)
	var response strings.Builder
	response.WriteString(`{"error":"`)
	response.WriteString(description)
	response.WriteString(`"}`)
	wr(a.main.Writer, response.String())
}

func (a API) sendMedia(d *devianter.Deviation) {
	mediaURL, name := devianter.UrlFromMedia(d.Media)
	a.main.SetFilename(name)
	if len(mediaURL) == 0 {
		return
	}

	if CFG.Proxy {
		mediaURL = mediaURL[21:]
		dot := strings.Index(mediaURL, ".")
		a.main.Writer.Header().Del("Content-Type")
		a.main.DownloadAndSendMedia(mediaURL[:dot], mediaURL[dot+11:])
	} else {
		a.main.Writer.Header().Add("Location", mediaURL)
		a.main.Writer.WriteHeader(302)
	}
}

// Random responds with a random artwork's media, retrying a bounded number of
// times when a search comes back empty or NSFW-filtered.
//
// TODO: add filters.
func (a API) Random() {
	// Bounded retries: the loop used to be unbounded, and the DeviantArt-error
	// path never incremented attempt, so a single request could spin forever
	// hammering the API (and get this instance's egress IP banned).
	const maxAttempts = 3

	// math/rand is deliberate: this picks a random artwork to show, which is not
	// a security decision and does not need a cryptographic source.
	for range maxAttempts {
		// strconv.Itoa, not string(): string(65) is "A", not "65".
		s, daErr, err := devianter.PerformSearch(strconv.Itoa(rand.Intn(999)), rand.Intn(30), 'a') //nolint:gosec // G404
		try(err)
		if daErr.RAW != nil {
			continue
		}

		// rand.Intn panics on 0, so an empty result set must be skipped.
		if len(s.Results) == 0 {
			continue
		}

		deviation := &s.Results[rand.Intn(len(s.Results))] //nolint:gosec // G404: see above
		if deviation.NSFW && !CFG.Nsfw {
			continue
		}

		a.sendMedia(deviation)
		return
	}

	a.Error("Sorry, butt NSFW on this are disabled, and the instance failed to find a random art without NSFW", 500)
}
