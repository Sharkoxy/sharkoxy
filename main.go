package main

import (
	_ "embed"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

//go:embed ui/index.html
var indexHTML string

const (
	uiPort  = 17800
	version = "1.7.2"
)

var dataDir string

// showHook brings the app window to the front (set by the native shell).
var showHook func() bool

// pingShow asks an already-running instance to show its window.
func pingShow() bool {
	req, err := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/_show", uiPort), nil)
	if err != nil {
		return false
	}
	req.Header.Set("X-Sharkoxy", "1")
	resp, err := (&http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}).Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func main() {
	noBrowser := flag.Bool("nobrowser", false, "don't open the app window")
	background := flag.Bool("background", false, "start without a window (used by launch-at-login)")
	flag.Parse()

	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	dataDir = filepath.Join(base, "Sharkoxy")
	if old := filepath.Join(base, "FastNode"); dirExists(old) && !dirExists(dataDir) {
		_ = os.Rename(old, dataDir) // settings from the pre-rename app
	}
	if d := envAny("SHARKOXY_DATA", "FASTNODE_DATA"); d != "" {
		dataDir = d
	}
	_ = os.MkdirAll(dataDir, 0o700)
	if exe, err := os.Executable(); err == nil {
		_ = os.Remove(exe + ".old") // left over from the last self-update
	}
	if applyPendingUpdate(!*background) {
		return // a newer, verified version was installed and started
	}

	uiURL := fmt.Sprintf("http://127.0.0.1:%d/", uiPort)
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", uiPort))
	for i := 0; err != nil && os.Getenv("SHARKOXY_RESTART") != "" && i < 40; i++ {
		time.Sleep(500 * time.Millisecond) // we were started by an update: wait for the old copy to let go of the port
		ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", uiPort))
	}
	if err != nil {
		// already running: hand over a clicked link, or just bring up another window for it
		if l := firstDeepLink(flag.Args()); l != "" && postImport(l) {
			return
		}
		if !*noBrowser && !*background && !pingShow() {
			openWindow(uiURL, nil)
		}
		return
	}
	if f, err := os.OpenFile(filepath.Join(dataDir, "app.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		log.SetOutput(f)
	}

	cleanupStaleProxy()
	app := newApp()
	registerProtocols(app.set.LinkAll)
	if l := firstDeepLink(flag.Args()); l != "" {
		_ = app.queueImport(l)
	}
	srv := &http.Server{Handler: app.routes(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	go func() { log.Println("server:", srv.Serve(ln)) }()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() { <-sig; app.quit() }()

	go app.healthLoop()
	go app.fastestLoop()
	go app.autoUpdateLoop()
	go app.updateLoop()
	app.mu.Lock()
	auto := app.set.AutoConnect && len(app.nodes) > 0
	app.mu.Unlock()
	if auto && corePath() != "" {
		go func() {
			// right after login the network may not be up yet, so keep trying for a few minutes
			var err error
			for i := 0; i < 30; i++ { // up to ~5 minutes
				if app.begin("connecting") != nil {
					return
				}
				err = app.connect()
				if err != nil {
					app.disconnect()
				}
				app.end()
				if err == nil {
					return
				}
				time.Sleep(10 * time.Second)
			}
			app.setErr(err)
		}()
	}

	if !*noBrowser && runShell(uiURL, app, !*background) {
		return // the native shell ran until the app quit
	}
	if !*noBrowser && !*background {
		// window closed while disconnected -> nothing left to do, exit.
		// While connected the proxy keeps running; start the exe again to reopen the window.
		openWindow(uiURL, func() {
			app.mu.Lock()
			idle := !app.running && app.busy == ""
			app.mu.Unlock()
			if idle {
				app.quit()
			}
		})
		go func() { // backstop if the window process can't be tracked
			for range time.Tick(15 * time.Second) {
				app.mu.Lock()
				stale := !app.running && app.busy == "" && !app.lastHB.IsZero() && time.Since(app.lastHB) > 3*time.Minute
				app.mu.Unlock()
				if stale {
					app.quit()
				}
			}
		}()
	}
	select {}
}

func dirExists(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }

func envAny(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}
