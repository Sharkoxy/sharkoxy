package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleIndex)
	mux.HandleFunc("/api/state", a.guard(a.handleState))
	mux.HandleFunc("/api/update/apply", a.guard(a.handleUpdateApply))
	mux.HandleFunc("/api/profile", a.guard(a.handleProfile))
	mux.HandleFunc("/api/connect", a.guard(a.handleConnect))
	mux.HandleFunc("/api/disconnect", a.guard(a.handleDisconnect))
	mux.HandleFunc("/api/test", a.guard(a.handleTest))
	mux.HandleFunc("/api/check", a.guard(a.handleCheck))
	mux.HandleFunc("/api/select", a.guard(a.handleSelect))
	mux.HandleFunc("/api/settings", a.guard(a.handleSettings))
	mux.HandleFunc("/api/core/install", a.guard(a.handleCoreInstall))
	mux.HandleFunc("/api/core/folder", a.guard(a.handleCoreFolder))
	mux.HandleFunc("/api/quit", a.guard(a.handleQuit))
	mux.HandleFunc("/_show", a.handleShow)
	mux.HandleFunc("/_quit", a.handleQuitExt)
	return mux
}

func hostOK(r *http.Request) bool {
	return r.Host == fmt.Sprintf("127.0.0.1:%d", uiPort) || r.Host == fmt.Sprintf("localhost:%d", uiPort)
}

// guard blocks other web pages from driving the local API (DNS rebinding / CSRF).
func (a *App) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !hostOK(r) || r.Header.Get("X-Token") != a.token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" || !hostOK(r) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(strings.Replace(strings.Replace(indexHTML, "__TOKEN__", a.token, 1), "__THEME__", a.themeAttr(), 1)))
}

func (a *App) reply(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.stateView())
}

func fail(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusBadRequest)
}

func (a *App) handleState(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.lastHB = time.Now()
	a.mu.Unlock()
	a.reply(w)
}

func (a *App) handleConnect(w http.ResponseWriter, r *http.Request) {
	if err := a.begin("connecting"); err != nil {
		fail(w, err)
		return
	}
	a.mu.Lock()
	a.restart = 0
	a.mu.Unlock()
	go func() {
		defer a.end()
		if err := a.connect(); err != nil {
			a.disconnect()
			a.setErr(err)
		}
	}()
	a.reply(w)
}

func (a *App) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.restart = 99 // user choice: never auto-restart
	a.mu.Unlock()
	a.disconnect()
	a.reply(w)
}

func (a *App) handleTest(w http.ResponseWriter, r *http.Request) {
	go a.testAll(true)
	time.Sleep(60 * time.Millisecond)
	a.reply(w)
}

func (a *App) handleCheck(w http.ResponseWriter, r *http.Request) {
	go a.runChecks()
	time.Sleep(60 * time.Millisecond)
	a.reply(w)
}

func (a *App) handleSelect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := a.selectNode(req.ID); err != nil {
		fail(w, err)
		return
	}
	a.reply(w)
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Bypass      *bool   `json:"bypass"`
		SysProxy    *bool   `json:"sysProxy"`
		TestURL     *string `json:"testUrl"`
		Mirror      *string `json:"mirror"`
		AutoStart   *bool   `json:"autoStart"`
		AutoConnect *bool   `json:"autoConnect"`
		AutoUpdate  *bool   `json:"autoUpdate"`
		LAN         *bool   `json:"lan"`
		DirectList  *string `json:"directList"`
		ProxyList   *string `json:"proxyList"`
		CheckSecs   *int    `json:"checkSecs"`
		ClashAPI    *bool   `json:"clashApi"`
		Welcomed    *bool   `json:"welcomed"`
		SelfUpdate  *bool   `json:"selfUpdate"`
		Theme       *string `json:"theme"`
		NoAnim      *bool   `json:"noAnim"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	a.mu.Lock()
	restart, sysChanged, autoStartChanged := false, false, false
	if req.Bypass != nil && *req.Bypass != a.set.Bypass {
		a.set.Bypass, restart = *req.Bypass, true
	}
	if req.LAN != nil && *req.LAN != a.set.LAN {
		a.set.LAN, restart = *req.LAN, true
	}
	if req.ClashAPI != nil && *req.ClashAPI != a.set.ClashAPI {
		a.set.ClashAPI, restart = *req.ClashAPI, true
	}
	if req.CheckSecs != nil && *req.CheckSecs >= 15 && *req.CheckSecs <= 3600 && *req.CheckSecs != a.set.CheckSecs {
		a.set.CheckSecs, restart = *req.CheckSecs, true
	}
	if req.TestURL != nil {
		u := strings.TrimSpace(*req.TestURL)
		if u == "" {
			u = defaultTestURL
		}
		if u != a.set.TestURL {
			a.set.TestURL, restart = u, true
		}
	}
	if req.DirectList != nil && strings.TrimSpace(*req.DirectList) != strings.TrimSpace(a.set.DirectList) {
		a.set.DirectList, restart = strings.TrimSpace(*req.DirectList), true
	}
	if req.ProxyList != nil && strings.TrimSpace(*req.ProxyList) != strings.TrimSpace(a.set.ProxyList) {
		a.set.ProxyList, restart = strings.TrimSpace(*req.ProxyList), true
	}
	if req.SysProxy != nil && *req.SysProxy != a.set.SysProxy {
		a.set.SysProxy, sysChanged = *req.SysProxy, true
	}
	if req.AutoStart != nil && *req.AutoStart != a.set.AutoStart {
		a.set.AutoStart, autoStartChanged = *req.AutoStart, true
	}
	if req.AutoConnect != nil {
		a.set.AutoConnect = *req.AutoConnect
	}
	if req.AutoUpdate != nil {
		a.set.AutoUpdate = *req.AutoUpdate
	}
	if req.Theme != nil && (*req.Theme == "pixel" || *req.Theme == "apple") {
		if a.set.Theme != *req.Theme {
			a.set.Theme = *req.Theme
			if themeHook != nil {
				defer themeHook()
			}
		}
	}
	if req.NoAnim != nil {
		a.set.NoAnim = *req.NoAnim
	}
	if req.SelfUpdate != nil {
		a.set.SelfUpdate = *req.SelfUpdate
	}
	if req.Welcomed != nil {
		a.set.Welcomed = *req.Welcomed
	}
	if req.Mirror != nil {
		a.set.Mirror = strings.TrimSpace(*req.Mirror)
	}
	a.saveLocked()
	running, sysOn, mixed, autoOn := a.running, a.set.SysProxy, a.mixed, a.set.AutoStart
	a.mu.Unlock()

	if autoStartChanged {
		if err := setAutostart(autoOn); err != nil {
			a.setErr(err)
		}
	}
	if running && sysChanged {
		applySysProxy(sysOn, fmt.Sprintf("127.0.0.1:%d", mixed))
	}
	if running && restart {
		if err := a.begin("reconnecting"); err == nil {
			go func() {
				defer a.end()
				if err := a.reconnect(); err != nil {
					a.disconnect()
					a.setErr(err)
				}
			}()
		}
	}
	a.reply(w)
}

func (a *App) handleCoreInstall(w http.ResponseWriter, r *http.Request) {
	if err := a.installCoreAsync(); err != nil {
		fail(w, err)
		return
	}
	a.reply(w)
}

func (a *App) handleCoreFolder(w http.ResponseWriter, r *http.Request) {
	openFolder(coreDir())
	a.reply(w)
}

func (a *App) handleQuit(w http.ResponseWriter, r *http.Request) {
	a.reply(w)
	go func() {
		time.Sleep(150 * time.Millisecond)
		a.quit()
	}()
}

// handleShow lets a second launch bring the first window to the front. It only shows a window,
// and the custom header stops other web pages from poking it.
// handleQuitExt lets the uninstaller/installer close a running copy cleanly (proxy settings restored, tray icon removed).
func (a *App) handleQuitExt(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || !hostOK(r) || r.Header.Get("X-Sharkoxy") == "" {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write([]byte("ok"))
	go func() { time.Sleep(200 * time.Millisecond); a.quit() }()
}

func (a *App) handleShow(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || !hostOK(r) || r.Header.Get("X-Sharkoxy") == "" || showHook == nil {
		http.NotFound(w, r)
		return
	}
	showHook()
	_, _ = w.Write([]byte("ok"))
}

// themeAttr is the theme the page should open with (no flash of the wrong look). Not chosen yet = pixel.
func (a *App) themeAttr() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.set.Theme == "apple" {
		return "apple"
	}
	return "pixel"
}
