package app

import (
	"encoding/json"
	"math/rand"
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

// fetchDailyDeviations is devianter.GetDailyDeviations behind a variable so
// tests can script it.
var fetchDailyDeviations = devianter.GetDailyDeviations

// Random responds with a random artwork's media, picked from the current daily
// deviations. That page is one upstream call the API cache answers for its
// TTL, where the previous random searches were up to three uncacheable calls
// per hit and a cheap way for a bot to burn the instance's upstream budget.
//
// TODO: add filters.
func (a API) Random() {
	dd, daErr := fetchDailyDeviations(0)
	if daErr.RAW != nil {
		a.Error("deviantart returned an error", 502)
		return
	}

	var pool []*devianter.Deviation
	for i := range dd.Deviations {
		if d := &dd.Deviations[i]; VisibleDeviation(d) {
			pool = append(pool, d)
		}
	}
	for s := range dd.Strips {
		for i := range dd.Strips[s].Deviations {
			if d := &dd.Strips[s].Deviations[i]; VisibleDeviation(d) {
				pool = append(pool, d)
			}
		}
	}
	if len(pool) == 0 {
		a.Error("no daily deviation this instance can show", 404)
		return
	}

	// math/rand is deliberate: this picks a random artwork to show, which is not
	// a security decision and does not need a cryptographic source.
	a.sendMedia(pool[rand.Intn(len(pool))]) //nolint:gosec // G404
}
