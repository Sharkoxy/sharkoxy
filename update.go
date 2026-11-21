package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Self-update. Every release publishes update.json (version, download URL, SHA-256 of the exe) plus
// update.json.sig, an Ed25519 signature made with the project's private key. The matching public key is
// compiled in below. An update is installed only if the signature verifies and the hash matches, so a
// hacked mirror or a man in the middle cannot push anything onto people's PCs.
var (
	updateBase = "https://github.com/Sharkoxy/sharkoxy/releases/latest/download/"
	// Ed25519 public key (hex). Empty = updates disabled. Replace it with your own: tools/signrelease keygen.
	updatePubHex = "cfebe7e188351768e9f7c8949488fb7225e0f48f4d6557b42853060ae1d21365"
)

const maxUpdateSize = 150 << 20

type manifest struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	Notes   string `json:"notes"`
}

type updateState struct {
	Available string // newer version found
	Notes     string
	Ready     bool   // downloaded, verified, waiting for a restart
	Err       string // last check error (quiet, shown only in Settings)
	Checking  bool
}

func (a *App) updateDir() string { return filepath.Join(dataDir, "update") }

// cmpVersion compares dotted numeric versions: -1, 0, 1.
func cmpVersion(x, y string) int {
	px, py := strings.Split(strings.TrimPrefix(x, "v"), "."), strings.Split(strings.TrimPrefix(y, "v"), ".")
	for i := 0; i < 3; i++ {
		var a, b int
		if i < len(px) {
			a, _ = strconv.Atoi(strings.TrimRightFunc(px[i], func(r rune) bool { return r < '0' || r > '9' }))
		}
		if i < len(py) {
			b, _ = strconv.Atoi(strings.TrimRightFunc(py[i], func(r rune) bool { return r < '0' || r > '9' }))
		}
		if a != b {
			if a < b {
				return -1
			}
			return 1
		}
	}
	return 0
}

func verifyManifest(pubHex string, body, sig []byte) (manifest, error) {
	var m manifest
	pub, err := hex.DecodeString(pubHex)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return m, errors.New("updates are not configured in this build")
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || !ed25519.Verify(pub, body, raw) {
		return m, errors.New("update signature is not valid")
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return m, err
	}
	if !strings.HasPrefix(m.URL, "https://") || len(m.SHA256) != 64 || m.Size <= 0 || m.Size > maxUpdateSize || m.Version == "" {
		return m, errors.New("update manifest is incomplete")
	}
	return m, nil
}

// updateClients: through our own proxy first (GitHub is often blocked without it), then directly.
func (a *App) updateClients() []*http.Client {
	mk := func(proxy string) *http.Client {
		tr := &http.Transport{DisableKeepAlives: true}
		if proxy != "" {
			pu, _ := url.Parse(proxy)
			tr.Proxy = http.ProxyURL(pu)
		}
		return &http.Client{Timeout: 5 * time.Minute, Transport: tr}
	}
	a.mu.Lock()
	running, port := a.running, a.mixed
	a.mu.Unlock()
	var cs []*http.Client
	if running {
		cs = append(cs, mk("http://127.0.0.1:"+itoa(port)))
	}
	return append(cs, mk(""))
}

func (a *App) fetchURLs(rel string) []string {
	a.mu.Lock()
	mirror := a.set.Mirror
	a.mu.Unlock()
	base := updateBase + rel
	urls := []string{base}
	if mirror != "" {
		urls = append(urls, strings.TrimRight(mirror, "/")+"/"+base)
	}
	return urls
}

func (a *App) fetchSmall(rel string) ([]byte, error) {
	var last error
	for _, u := range a.fetchURLs(rel) {
		for _, c := range a.updateClients() {
			c.Timeout = 25 * time.Second
			resp, err := c.Get(u)
			if err != nil {
				last = err
				continue
			}
			b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if err == nil && resp.StatusCode == 200 {
				return b, nil
			}
			last = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
	}
	return nil, last
}

// checkUpdate looks for a newer signed release and downloads it. It never installs by itself.
func (a *App) checkUpdate() {
	a.mu.Lock()
	if a.upd.Checking || a.upd.Ready {
		a.mu.Unlock()
		return
	}
	a.upd.Checking = true
	a.mu.Unlock()
	err := a.doCheckUpdate()
	a.mu.Lock()
	a.upd.Checking = false
	if err != nil {
		a.upd.Err = err.Error()
		log.Println("update check:", err)
	} else {
		a.upd.Err = ""
	}
	a.mu.Unlock()
}

func (a *App) doCheckUpdate() error {
	body, err := a.fetchSmall("update.json")
	if err != nil {
		return err
	}
	sig, err := a.fetchSmall("update.json.sig")
	if err != nil {
		return err
	}
	m, err := verifyManifest(updatePubHex, body, sig)
	if err != nil {
		return err
	}
	if cmpVersion(m.Version, version) <= 0 {
		return nil
	}
	a.mu.Lock()
	a.upd.Available, a.upd.Notes = m.Version, m.Notes
	a.mu.Unlock()

	dir := a.updateDir()
	_ = os.MkdirAll(dir, 0o755)
	dst := filepath.Join(dir, "Sharkoxy-"+m.Version+".exe")
	if fileHash(dst) == m.SHA256 {
		a.markReady(dst, m)
		return nil
	}
	var last error
	for _, c := range a.updateClients() {
		if last = downloadVerified(c, m, dst); last == nil {
			a.markReady(dst, m)
			return nil
		}
	}
	return last
}

func (a *App) markReady(path string, m manifest) {
	_ = os.WriteFile(filepath.Join(a.updateDir(), "pending.json"), mustJSON(map[string]string{"path": path, "sha256": m.SHA256, "version": m.Version}), 0o644)
	a.mu.Lock()
	a.upd.Ready, a.upd.Available, a.upd.Notes = true, m.Version, m.Notes
	a.mu.Unlock()
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func fileHash(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func downloadVerified(c *http.Client, m manifest, dst string) error {
	c.Timeout = 10 * time.Minute
	resp, err := c.Get(m.URL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, m.Size+1))
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if n != m.Size || hex.EncodeToString(h.Sum(nil)) != m.SHA256 {
		os.Remove(tmp)
		return errors.New("downloaded update does not match its signed hash")
	}
	return os.Rename(tmp, dst)
}

// applyPendingUpdate runs at startup: if a verified update is waiting, swap it in before anything else.
// Returns true when the new version was started and this process should exit.
func applyPendingUpdate(show bool) bool {
	dir := filepath.Join(dataDir, "update")
	b, err := os.ReadFile(filepath.Join(dir, "pending.json"))
	if err != nil {
		return false
	}
	var p struct{ Path, Sha256, Version string }
	var raw map[string]string
	if json.Unmarshal(b, &raw) != nil {
		return false
	}
	p.Path, p.Sha256, p.Version = raw["path"], raw["sha256"], raw["version"]
	clean := func() { _ = os.Remove(filepath.Join(dir, "pending.json")) }
	if cmpVersion(p.Version, version) <= 0 { // already running that (or newer): tidy up
		clean()
		_ = os.Remove(p.Path)
		return false
	}
	if fileHash(p.Path) != p.Sha256 {
		clean()
		return false
	}
	if err := swapExe(p.Path); err != nil {
		log.Println("update: swap failed:", err)
		return false
	}
	clean()
	return restartSelf(show)
}

func (a *App) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	ready := a.upd.Ready
	a.mu.Unlock()
	if !ready {
		fail(w, errors.New("no update is ready yet"))
		return
	}
	a.reply(w)
	go func() {
		time.Sleep(300 * time.Millisecond)
		if applyPendingUpdate(true) { // new exe started: put the proxy back to normal and leave
			a.quit()
		}
	}()
}

func (a *App) updateLoop() {
	time.Sleep(90 * time.Second)
	for {
		a.mu.Lock()
		on := a.set.SelfUpdate
		a.mu.Unlock()
		if on {
			a.checkUpdate()
		}
		time.Sleep(6 * time.Hour)
	}
}
