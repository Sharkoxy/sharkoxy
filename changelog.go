package main

type whatsNew struct {
	Version string   `json:"version"`
	Notes   []string `json:"notes"`
}

// changelog is shown once, in a popup, the first time a new version starts.
var changelog = map[string][]string{
	"1.7.2": {
		"Fixed: the app window could stop loading (\"can't reach this page\") after many hours of use",
		"Fixed: false \"no internet\" and disconnects in very long sessions; the connection now repairs itself",
		"New: this \"what's new\" note after every update",
		"New: live speed graph, paste from clipboard, and add subscriptions from browser links",
	},
	"1.7.1": {
		"Fixed: after many hours the app could stop opening and show \"can't reach this page\"",
		"Fixed: false \"no internet\" and disconnects during very long sessions",
		"The connection now repairs itself if the network changes or the core gets stuck",
		"New: live speed graph on the main screen",
		"New: paste a subscription from the clipboard, or add one from a browser link (sharkoxy://)",
	},
}
