package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Links that open Sharkoxy from a browser or another app:
//
//	sharkoxy://add?url=https%3A%2F%2F...&name=Work
//	clash://install-config?url=...&name=...
//	sing-box://import-remote-profile?url=...#Name
//
// Nothing is added until the person confirms it inside the app.

type pendingImport struct {
	URL  string
	Name string
}

var deepSchemes = map[string]bool{"sharkoxy": true, "clash": true, "clashmeta": true, "mihomo": true, "sing-box": true}

// parseDeepLink extracts the subscription URL and an optional name from one of the links above.
func parseDeepLink(raw string) (subURL, name string, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !deepSchemes[strings.ToLower(u.Scheme)] {
		return "", "", errors.New("not a Sharkoxy link")
	}
	q := u.Query()
	subURL = q.Get("url")
	name = q.Get("name")
	if name == "" {
		name = u.Fragment // sing-box puts the name after the #
	}
	su, err := url.Parse(subURL)
	if err != nil || (su.Scheme != "http" && su.Scheme != "https") || su.Host == "" || len(subURL) > 2048 {
		return "", "", errors.New("the link does not contain a valid subscription address")
	}
	name = strings.TrimSpace(name)
	if len([]rune(name)) > 40 {
		name = string([]rune(name)[:40])
	}
	return subURL, name, nil
}

// firstDeepLink finds a Sharkoxy-style link among command-line arguments.
func firstDeepLink(args []string) string {
	for _, a := range args {
		if i := strings.Index(a, "://"); i > 0 && deepSchemes[strings.ToLower(a[:i])] {
			return a
		}
	}
	return ""
}

func (a *App) queueImport(raw string) error {
	u, name, err := parseDeepLink(raw)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.importReq = &pendingImport{URL: u, Name: name}
	a.mu.Unlock()
	return nil
}

// postImport hands a link to the copy of Sharkoxy that is already running.
func postImport(link string) bool {
	body, _ := json.Marshal(map[string]string{"link": link})
	req, err := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/_import", uiPort), bytes.NewReader(body))
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

// handleImportExt: another Sharkoxy process (started by clicking a link) passes it on.
func (a *App) handleImportExt(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || !hostOK(r) || r.Header.Get("X-Sharkoxy") == "" {
		http.NotFound(w, r)
		return
	}
	var req struct {
		Link string `json:"link"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req)
	if err := a.queueImport(req.Link); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if showHook != nil {
		showHook()
	}
	_, _ = w.Write([]byte("ok"))
}

// handleImport: the person's answer to "add this subscription?" inside the app.
func (a *App) handleImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Accept bool `json:"accept"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	a.mu.Lock()
	p := a.importReq
	a.importReq = nil
	a.mu.Unlock()
	if p == nil || !req.Accept {
		a.reply(w)
		return
	}
	if err := a.begin("updating"); err != nil {
		fail(w, err)
		return
	}
	go func() {
		defer a.end()
		a.setErr(a.addOrRefreshProfile(p.URL, p.Name))
	}()
	a.reply(w)
}

func (a *App) handleClipboard(w http.ResponseWriter, r *http.Request) {
	t := strings.TrimSpace(readClipboard())
	if len(t) > 16384 {
		t = t[:16384]
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"text": t})
}
