package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// A profile is one subscription (or one set of pasted links). Several can be active at once:
// their servers are merged into one list and Sharkoxy picks the fastest across all of them.
type Profile struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Input   string   `json:"input"`
	Links   []string `json:"links"`
	Updated string   `json:"updated"`
	Off     bool     `json:"off"`
}

const timeFmt = "2006-01-02 15:04"

func profileName(input string) string {
	in := strings.TrimSpace(input)
	if isURLInput(in) {
		if u, err := url.Parse(in); err == nil && u.Hostname() != "" {
			return u.Hostname()
		}
	}
	return "Pasted links"
}

// migrateProfiles turns the single subscription of older versions into the first profile.
func migrateProfiles(s *Settings) {
	if len(s.Profiles) == 0 && (len(s.Links) > 0 || s.SubInput != "") {
		s.Profiles = []Profile{{ID: "p" + randHex(3), Name: profileName(s.SubInput), Input: s.SubInput, Links: s.Links, Updated: s.Updated}}
	}
	s.Links, s.SubInput, s.Updated = nil, "", ""
}

// nodesFromProfiles merges every active profile, dropping duplicate servers.
func nodesFromProfiles(ps []Profile) []Node {
	var all []Node
	seen := map[string]bool{}
	for _, p := range ps {
		if p.Off {
			continue
		}
		for _, l := range p.Links {
			if seen[l] {
				continue
			}
			if n, err := parseLink(l); err == nil {
				seen[l] = true
				n.Src = p.Name
				all = append(all, n)
			}
		}
	}
	assignIDs(all)
	return all
}

func activeLinks(ps []Profile) []string {
	var out []string
	for _, p := range ps {
		if !p.Off {
			out = append(out, p.Links...)
		}
	}
	return out
}

// applyProfiles rebuilds the merged server list. It only touches the live connection when the list really changed.
func (a *App) applyProfiles(before []string) error {
	a.mu.Lock()
	a.nodes = nodesFromProfiles(a.set.Profiles)
	changed := !sameLinks(before, activeLinks(a.set.Profiles))
	if changed {
		a.delays = map[string]int{}
		a.inCfg = map[string]bool{}
	}
	a.saveLocked()
	a.mu.Unlock()
	if !changed {
		return nil
	}
	go a.resolveGeo()
	return a.reconnect()
}

func (a *App) findProfile(id string) int {
	for i := range a.set.Profiles {
		if a.set.Profiles[i].ID == id {
			return i
		}
	}
	return -1
}

// addOrRefreshProfile adds a subscription, or refreshes it when the same source is already there.
func (a *App) addOrRefreshProfile(input, name string) error {
	input = strings.TrimSpace(input)
	nodes, err := loadSubscription(input)
	if err != nil {
		return err
	}
	links := make([]string, len(nodes))
	for i, n := range nodes {
		links[i] = n.Link
	}
	a.mu.Lock()
	before := activeLinks(a.set.Profiles)
	idx := -1
	for i, p := range a.set.Profiles {
		if p.Input == input {
			idx = i
		}
	}
	if idx < 0 {
		if name == "" {
			name = profileName(input)
		}
		a.set.Profiles = append(a.set.Profiles, Profile{ID: "p" + randHex(3), Name: name})
		idx = len(a.set.Profiles) - 1
	}
	p := &a.set.Profiles[idx]
	p.Input, p.Links, p.Off, p.Updated = input, links, false, time.Now().Format(timeFmt)
	if name != "" {
		p.Name = name
	}
	a.mu.Unlock()
	return a.applyProfiles(before)
}

// refreshProfile re-downloads one subscription. On failure the old servers stay as they were.
func (a *App) refreshProfile(id string) error {
	a.mu.Lock()
	i := a.findProfile(id)
	if i < 0 {
		a.mu.Unlock()
		return errors.New("that profile no longer exists")
	}
	input := a.set.Profiles[i].Input
	a.mu.Unlock()
	if !isURLInput(input) {
		return errors.New("pasted links can't be refreshed: add the link again to change them")
	}
	nodes, err := loadSubscription(input)
	if err != nil {
		return err
	}
	links := make([]string, len(nodes))
	for k, n := range nodes {
		links[k] = n.Link
	}
	a.mu.Lock()
	i = a.findProfile(id)
	if i < 0 {
		a.mu.Unlock()
		return nil
	}
	before := activeLinks(a.set.Profiles)
	a.set.Profiles[i].Links = links
	a.set.Profiles[i].Updated = time.Now().Format(timeFmt)
	a.mu.Unlock()
	return a.applyProfiles(before)
}

func (a *App) refreshAllProfiles() error {
	a.mu.Lock()
	var ids []string
	for _, p := range a.set.Profiles {
		if !p.Off && isURLInput(p.Input) {
			ids = append(ids, p.ID)
		}
	}
	a.mu.Unlock()
	var first error
	for _, id := range ids {
		if err := a.refreshProfile(id); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (a *App) editProfile(id, action, name string) error {
	a.mu.Lock()
	i := a.findProfile(id)
	if i < 0 {
		a.mu.Unlock()
		return errors.New("that profile no longer exists")
	}
	before := activeLinks(a.set.Profiles)
	switch action {
	case "remove":
		a.set.Profiles = append(a.set.Profiles[:i], a.set.Profiles[i+1:]...)
	case "toggle":
		a.set.Profiles[i].Off = !a.set.Profiles[i].Off
	case "rename":
		if n := strings.TrimSpace(name); n != "" {
			a.set.Profiles[i].Name = n
		}
	}
	a.mu.Unlock()
	return a.applyProfiles(before)
}

func (a *App) handleProfile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"` // add | update | updateAll | remove | toggle | rename
		ID     string `json:"id"`
		Input  string `json:"input"`
		Name   string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := a.begin("updating"); err != nil {
		fail(w, err)
		return
	}
	go func() {
		defer a.end()
		var err error
		switch req.Action {
		case "add":
			if strings.TrimSpace(req.Input) == "" {
				err = errors.New("paste a subscription link first")
			} else {
				err = a.addOrRefreshProfile(req.Input, req.Name)
			}
		case "update":
			err = a.refreshProfile(req.ID)
		case "updateAll":
			err = a.refreshAllProfiles()
		case "remove", "toggle", "rename":
			err = a.editProfile(req.ID, req.Action, req.Name)
		default:
			err = errors.New("unknown action")
		}
		a.setErr(err)
	}()
	a.reply(w)
}

// autoUpdateLoop refreshes URL subscriptions every 12 hours, each profile on its own clock.
func (a *App) autoUpdateLoop() {
	for {
		time.Sleep(30 * time.Minute)
		a.mu.Lock()
		on := a.set.AutoUpdate
		var due []string
		for _, p := range a.set.Profiles {
			if p.Off || !isURLInput(p.Input) {
				continue
			}
			if t, err := time.ParseInLocation(timeFmt, p.Updated, time.Local); err == nil && time.Since(t) < 12*time.Hour {
				continue
			}
			due = append(due, p.ID)
		}
		a.mu.Unlock()
		if !on || len(due) == 0 || a.begin("updating") != nil {
			continue
		}
		for _, id := range due {
			if err := a.refreshProfile(id); err != nil {
				log.Println("auto-update:", err)
			}
		}
		a.end()
	}
}
