package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"html/template"
	"os"
	"time"
)

// ExecuteCommandLineArguments parses argv, applying the flags that override
// config and running one-shot commands such as --help and --add-instance. Some
// of those commands exit the process rather than return.
func ExecuteCommandLineArguments() {
	var helpmsg = `SkunkyArt v{{.Version}} [{{.Description}}]
Usage:
	- [-c|--config] 	| path to config
	- [-a|--add-instance]	| generates 'instances.json' and 'INSTANCES.md' files with ur instance
	- [-h|--help]		| returns this message
Example:
	./skunkyart -c config.json
Copyright lost+skunk and zerolabs, X11. https://github.com/krazywarez/skunky-art/releases/tag/v{{.Version}}`

	a := os.Args[1:]
	for n, x := range a {
		switch x {
		case "-c", "--config":
			if n+1 < len(a) {
				CFG.cfg = a[n+1]
				cfgExplicit = true
			} else {
				exit("Not enought arguments", 1)
			}
		case "-h", "--help":
			var buf bytes.Buffer
			t := template.New("help")
			tryWithExitStatus(func() error {
				if _, err := t.Parse(helpmsg); err != nil {
					return err
				}
				return t.Execute(&buf, &Release)
			}(), 1)
			exit(buf.String(), 0)
		case "-a", "--add-instance":
			addInstance()
		}
	}
}

type settingsUrls struct {
	I2P      string `json:"i2p,omitempty"`
	Ygg      string `json:"ygg,omitempty"`
	Tor      string `json:"tor,omitempty"`
	Clearnet string `json:"clearnet,omitempty"`
}

type settingsParams struct {
	Nsfw   bool   `json:"nsfw"`
	Proxy  bool   `json:"proxy"`
	HideAI bool   `json:"hide-ai"`
	Theme  string `json:"theme"`
}

type settings struct {
	Title       string         `json:"title"`
	Country     string         `json:"country"`
	ModifiedSrc string         `json:"modified-src,omitempty"`
	Urls        settingsUrls   `json:"urls"`
	Settings    settingsParams `json:"settings"`
}

func addInstance() {
	prompt := func(txt string, necessary bool) string {
		input := bufio.NewScanner(os.Stdin)
		for {
			print(txt)
			print(": ")
			input.Scan()

			if i := input.Text(); necessary && i == "" {
				println("Please specify the", txt)
			} else {
				return i
			}
		}
	}

	var settingsVar struct {
		Instances []settings `json:"instances"`
	}
	// 0644: both files are committed to the repository and are meant to be
	// world-readable, so gosec's 0600 default does not apply.
	instancesJSON, err := os.OpenFile("instances.json", os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec // G302
	if err != nil {
		exit(err.Error(), 1)
	}
	defer func() { try(instancesJSON.Close()) }()

	instancesFile, err := os.OpenFile("INSTANCES.md", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec // G302
	if err != nil {
		exit(err.Error(), 1)
	}
	defer func() { try(instancesFile.Close()) }()

	for {
		if string(instances) == "" {
			print("\rDownloading instance list...")
		} else {
			println("\r\033[2KDownloaded!")
			try(json.Unmarshal(instances, &settingsVar))

			settingsVar.Instances = append(settingsVar.Instances, settings{
				Title:       prompt("Title", true),
				Country:     prompt("Country", true),
				ModifiedSrc: prompt("Link to modified sources", false),
				Settings: settingsParams{
					Nsfw:   CFG.Nsfw,
					Proxy:  CFG.Proxy,
					HideAI: CFG.HideAI,
					Theme:  CFG.Theme,
				},
				Urls: settingsUrls{
					Clearnet: prompt("Clearnet link", false),
					Ygg:      prompt("Yggdrasil link", false),
					Tor:      prompt("Onion link", false),
					I2P:      prompt("I2P link", false),
				},
			})

			j, err := json.MarshalIndent(&settingsVar, "", "    ")
			try(err)

			try(func() error { _, err := instancesJSON.Write(j); return err }())

			settingsVar := &settingsVar.Instances[len(settingsVar.Instances)-1]
			var mdstr bytes.Buffer

			mdbuilder := func(yes bool, link string, title string) {
				switch {
				case yes && (title != "" && link != ""):
					mdstr.WriteString("[")
					mdstr.WriteString(title)
					mdstr.WriteString("](")
					mdstr.WriteString(link)
					mdstr.WriteString(")")
				case yes && link != "":
					mdstr.WriteString("[Yes](")
					mdstr.WriteString(link)
					mdstr.WriteString(")")
				case yes:
					mdstr.WriteString("Yes")
				default:
					mdstr.WriteString("No")
				}
				mdstr.WriteString("|")
			}

			mdstr.WriteString("\n|")
			mdbuilder(settingsVar.Urls.Clearnet != "", settingsVar.Urls.Clearnet, settingsVar.Title)

			urls := []string{settingsVar.Urls.Ygg, settingsVar.Urls.I2P, settingsVar.Urls.Tor}
			for i, l := 0, len(urls); i < l; i++ {
				url := urls[i]
				mdbuilder(url != "", url, "")
			}

			settings := []bool{settingsVar.Settings.Nsfw, settingsVar.Settings.Proxy}
			for i, l := 0, len(settings); i < l; i++ {
				mdbuilder(settings[i], "", "")
			}

			mdbuilder(settingsVar.ModifiedSrc != "", settingsVar.ModifiedSrc, "")

			mdstr.WriteString(settingsVar.Country)
			mdstr.WriteString("|")

			try(func() error { _, err := instancesFile.Write(mdstr.Bytes()); return err }())
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	exit("Done! Now add the files 'instances.json' and 'INSTANCES.md' to the 'main' branch in the repository https://github.com/krazywarez/skunky-art", 0)
}
