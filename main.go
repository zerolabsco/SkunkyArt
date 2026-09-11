package main

import (
	"skunkyart/app"
	"skunkyart/static"
	"time"

	"github.com/krazywarez/devianter"
)

// version is the release this binary was built from. The release workflow links
// it in from the git tag so that --help and /api/instance cannot drift from the
// tag the image was built at:
//
//	go build -ldflags "-X main.version=1.3.7"
//
// A plain `go build` leaves it as "dev".
var version = "dev"

func main() {
	app.Release.Version = version
	app.Release.Description = "Upstream request caching and rate limiting, escaped output, a valid Atom feed, and a translated interface"

	app.ExecuteCommandLineArguments()
	app.ExecuteConfig()
	static.CopyTemplatesToMemory()

	// After the copy, not before: the catalogues are assets, and ExecuteConfig
	// runs while static/ is still unread.
	app.LoadLanguages()
	app.ParseTemplates()

	// Rate/concurrency-limit + time-out outbound DeviantArt requests so bot floods
	// can't exhaust the process or get our egress IP banned by CloudFront/WAF.
	app.InstallDAThrottle()

	// Only once the config is loaded and the throttle installed: this fetches over
	// the network, so starting it earlier both raced ExecuteConfig's writes to CFG
	// and let the request escape the throttle and the configured User-Agent.
	go app.RefreshInstances()

	// The first session bootstrap runs before the listener opens: requests
	// that arrive before it finishes go to DeviantArt without a token and
	// fail, which showed up as 502s for the first seconds after a restart.
	if err := devianter.UpdateCSRF(); err != nil {
		println(err.Error())
	}
	go func() {
		for {
			time.Sleep(12 * time.Hour)
			if err := devianter.UpdateCSRF(); err != nil {
				println(err.Error())
			}
		}
	}()

	app.Router()
}
