package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

func itoa(i int) string { return strconv.Itoa(i) }

type Settings struct {
	Profiles    []Profile `json:"profiles"`
	SubInput    string    `json:"subInput,omitempty"` // pre-1.5 single subscription, migrated into Profiles
	Links       []string  `json:"links,omitempty"`
	Updated     string    `json:"updated,omitempty"`
	Bypass      bool      `json:"bypass"` // true = smart routing (mainland sites go direct)
	SysProxy    bool      `json:"sysProxy"`
	TestURL     string    `json:"testUrl"`
	Mirror      string    `json:"mirror"`
	AutoStart   bool      `json:"autoStart"`
	AutoConnect bool      `json:"autoConnect"`
	AutoUpdate  bool      `json:"autoUpdate"`
	LAN         bool      `json:"lan"`
	DirectList  string    `json:"directList"`
	ProxyList   string    `json:"proxyList"`
	CheckSecs   int       `json:"checkSecs"` // how often the fastest server is re-checked
	ClashAPI    bool      `json:"clashApi"`  // expose a Clash-compatible controller for dashboards
	APISecret   string    `json:"apiSecret"`
	Welcomed    bool      `json:"welcomed"`   // first-run thank-you popup already shown
	Theme       string    `json:"theme"`      // "", "pixel" or "apple" ("" = not chosen yet)
	NoAnim      bool      `json:"noAnim"`     // turn off the connecting animation popup
	LinkAll     bool      `json:"linkAll"`    // also open clash:// and sing-box:// links
	SelfUpdate  bool      `json:"selfUpdate"` // download signed app updates
	V           int       `json:"v"`          // settings version, for upgrades
}

func defaultSettings() Settings {
	return Settings{Bypass: true, SysProxy: true, AutoUpdate: true, SelfUpdate: true, AutoConnect: true, CheckSecs: 60,
		TestURL: defaultTestURL, V: 3}
}

func loadSettings() Settings {
	s := defaultSettings()
	if b, err := os.ReadFile(filepath.Join(dataDir, "settings.json")); err == nil {
		s.V = 0 // an existing file without a version is from an older release
		_ = json.Unmarshal(b, &s)
	}
	if s.TestURL == "" {
		s.TestURL = defaultTestURL
	}
	if s.V < 2 { // upgrade: always connect on start, check every minute
		s.AutoConnect, s.V = true, 2
	}
	if s.V < 3 { // v1.3: plain-HTTP latency probe (one round trip less than HTTPS)
		if s.TestURL == oldTestURL {
			s.TestURL = defaultTestURL
		}
		s.V = 3
	}
	migrateProfiles(&s)
	if s.CheckSecs < 15 {
		s.CheckSecs = 60
	}
	if s.APISecret == "" {
		s.APISecret = randHex(12)
	}
	return s
}

type checkResult struct {
	Name string `json:"name"`
	MS   int    `json:"ms"` // -1 = unreachable
}

type App struct {
	mu      sync.Mutex
	set     Settings
	nodes   []Node
	delays  map[string]int // id -> ms; -1 = failed/unreachable; missing = not tested yet
	inCfg   map[string]bool
	cmd     *exec.Cmd
	running bool
	busy    string
	testing bool
	err     string
	restart int

	mixed, apiPort int
	secret         string
	active         string
	pinned         bool
	userPin        string // node the user picked by hand ("" = automatic)
	liveMS         int    // last real request through the proxy
	hist           []histPt
	importReq      *pendingImport
	lastRefresh    time.Time
	lastSwitch     time.Time
	startedAt      time.Time
	netDown        bool
	proxyFixes     int
	switchMu       sync.Mutex
	coreVer        string

	up, down      int64
	cancelTraffic context.CancelFunc

	checks   []checkResult
	checking bool

	geoCache map[string]*Geo
	geoTried map[string]bool

	dl struct {
		active      bool
		done, total int64
		err         string
	}

	upd updateState

	token  string
	lastHB time.Time
}

func newApp() *App {
	a := &App{
		set: loadSettings(), delays: map[string]int{}, inCfg: map[string]bool{},
		token: randHex(16), geoCache: loadGeoCache(), geoTried: map[string]bool{},
	}
	a.nodes = nodesFromProfiles(a.set.Profiles)
	if p := corePath(); p != "" {
		a.coreVer = coreVersion(p)
	}
	return a
}

func (a *App) saveLocked() {
	b, _ := json.MarshalIndent(a.set, "", "  ")
	_ = os.WriteFile(filepath.Join(dataDir, "settings.json"), b, 0o600)
}

func (a *App) setErr(err error) {
	a.mu.Lock()
	if err != nil {
		a.err = err.Error()
		log.Println("error:", err)
	}
	a.mu.Unlock()
}

func (a *App) begin(op string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.busy != "" {
		return errors.New("still " + a.busy + ", one moment")
	}
	a.busy = op
	a.err = ""
	return nil
}

func (a *App) end() {
	a.mu.Lock()
	a.busy = ""
	a.mu.Unlock()
}

// ---- system proxy bookkeeping ----------------------------------------------

func proxyMarker() string { return filepath.Join(dataDir, "sysproxy.on") }

func applySysProxy(on bool, addr string) {
	if err := setSystemProxy(on, addr); err != nil {
		log.Println("system proxy:", err)
		return
	}
	if on {
		_ = os.WriteFile(proxyMarker(), []byte(addr), 0o600)
	} else {
		_ = os.Remove(proxyMarker())
	}
}

// cleanupStaleProxy undoes a system proxy left behind by a crash.
func cleanupStaleProxy() {
	if _, err := os.Stat(proxyMarker()); err == nil {
		applySysProxy(false, "")
	}
}

// ---- core control ------------------------------------------------------------

func (a *App) apiCall(method, path string, body any, timeout time.Duration, out any) error {
	a.mu.Lock()
	port, secret := a.apiPort, a.secret
	a.mu.Unlock()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c := &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: nil}}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("core answered %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func tailFile(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (a *App) startCore(exe, cfgPath string) error {
	logPath := filepath.Join(dataDir, "core.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "run", "-c", cfgPath, "-D", dataDir)
	cmd.Dir = dataDir
	cmd.Stdout, cmd.Stderr = logf, logf
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		logf.Close()
		return err
	}
	exited := make(chan struct{})
	a.mu.Lock()
	a.cmd = cmd
	a.mu.Unlock()
	go func() {
		_ = cmd.Wait()
		logf.Close()
		close(exited)
		a.onCoreExit(cmd)
	}()

	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			msg := tailFile(logPath, 4)
			if msg == "" {
				msg = "the core exited right after starting"
			}
			return errors.New("core failed to start: " + msg)
		default:
		}
		if err := a.apiCall("GET", "/version", nil, 1500*time.Millisecond, nil); err == nil {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	a.killCore()
	return errors.New("core did not become ready in time")
}

func (a *App) killCore() {
	a.mu.Lock()
	cmd := a.cmd
	a.cmd = nil
	a.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// onCoreExit recovers when the core dies on its own (see recoverCore in stable.go).
func (a *App) onCoreExit(cmd *exec.Cmd) {
	a.mu.Lock()
	unexpected := a.cmd == cmd
	was := false
	if unexpected {
		a.cmd = nil
		was = a.running
		if was {
			a.running = false
			if a.cancelTraffic != nil {
				a.cancelTraffic()
			}
		}
	}
	a.mu.Unlock()
	if !unexpected {
		return
	}
	applySysProxy(false, "") // never leave apps pointed at a dead proxy
	if was {
		go a.recoverCore()
	}
}

func (a *App) connect() error {
	exe := corePath()
	if exe == "" {
		return errors.New("the proxy core isn't installed yet")
	}
	a.mu.Lock()
	nodes := append([]Node(nil), a.nodes...)
	set := a.set
	ver := a.coreVer
	a.mu.Unlock()
	if len(nodes) == 0 {
		return errors.New("add a subscription first")
	}
	if ver == "" {
		ver = coreVersion(exe)
		a.mu.Lock()
		a.coreVer = ver
		a.mu.Unlock()
	}

	alive := probeAlive(nodes)
	mixed, err := freePortPrefer(7890) // the usual Clash port: stable across restarts
	if err != nil {
		return err
	}
	api, err := freePort()
	if set.ClashAPI {
		api, err = freePortPrefer(9090) // the port Clash dashboards expect
	}
	if err != nil {
		return err
	}
	secret := randHex(16)
	if set.ClashAPI {
		secret = set.APISecret
	}
	cfg, err := buildConfig(alive, set, mixed, api, secret, ver)
	if err != nil {
		return err
	}
	cfgPath := filepath.Join(dataDir, "config.json")
	if err := os.WriteFile(cfgPath, cfg, 0o600); err != nil {
		return err
	}
	inCfg := map[string]bool{}
	for _, n := range alive {
		inCfg[n.ID] = true
	}
	delays := map[string]int{}
	for _, n := range nodes {
		if !inCfg[n.ID] {
			delays[n.ID] = -1
		}
	}
	a.mu.Lock()
	a.mixed, a.apiPort, a.secret = mixed, api, secret
	a.delays, a.inCfg = delays, inCfg
	a.active, a.pinned, a.liveMS = "", false, 0
	if !inCfg[a.userPin] {
		a.userPin = ""
	}
	a.up, a.down = 0, 0
	a.checks = nil
	a.mu.Unlock()

	if err := a.startCore(exe, cfgPath); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.running = true
	a.startedAt = time.Now()
	a.cancelTraffic = cancel
	pin := a.userPin
	a.mu.Unlock()
	go a.streamTraffic(ctx, api, secret)
	if pin != "" {
		_ = a.apiCall("PUT", "/proxies/proxy", map[string]string{"name": pin}, 5*time.Second, nil)
	}
	// Pick a working server and prove it carries traffic BEFORE the system proxy points at us.
	if err := a.initialSelect(); err != nil {
		return err
	}
	if set.SysProxy {
		applySysProxy(true, fmt.Sprintf("127.0.0.1:%d", mixed))
	}
	go a.testAll(true) // refine: best-of-3 on every server
	go a.resolveGeo()
	return nil
}

func (a *App) disconnect() {
	a.mu.Lock()
	a.running = false
	a.testing = false
	a.active, a.pinned, a.liveMS = "", false, 0
	a.up, a.down = 0, 0
	if a.cancelTraffic != nil {
		a.cancelTraffic()
		a.cancelTraffic = nil
	}
	a.mu.Unlock()
	applySysProxy(false, "")
	a.killCore()
}

// streamTraffic follows the core's live speed feed.
func (a *App) streamTraffic(ctx context.Context, port int, secret string) {
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d/traffic", port), nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		var t struct {
			Up   int64 `json:"up"`
			Down int64 `json:"down"`
		}
		if json.Unmarshal(sc.Bytes(), &t) == nil {
			a.mu.Lock()
			a.up, a.down = t.Up, t.Down
			a.hist = append(a.hist, histPt{t.Down, t.Up, a.liveMS})
			if len(a.hist) > 90 {
				a.hist = a.hist[len(a.hist)-90:]
			}
			a.mu.Unlock()
		}
	}
}

type proxiesResp struct {
	Proxies map[string]struct {
		Now     string `json:"now"`
		History []struct {
			Delay int `json:"delay"`
		} `json:"history"`
	} `json:"proxies"`
}

// refresh reads which node the core is currently routing through.
func (a *App) refresh() {
	var pr proxiesResp
	if err := a.apiCall("GET", "/proxies", nil, 3*time.Second, &pr); err != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.running {
		return
	}
	if sel, ok := pr.Proxies["proxy"]; ok {
		a.active = sel.Now
	}
	a.pinned = a.userPin != ""
	a.lastRefresh = time.Now()
}

func (a *App) selectNode(id string) error {
	if id == "" || id == "auto" {
		a.mu.Lock()
		a.userPin = ""
		a.pinned = false
		a.mu.Unlock()
		a.pickBest()
		go a.testAll(false)
		a.refresh()
		return nil
	}
	if err := a.apiCall("PUT", "/proxies/proxy", map[string]string{"name": id}, 5*time.Second, nil); err != nil {
		return err
	}
	a.mu.Lock()
	a.userPin = id
	a.mu.Unlock()
	a.refresh()
	return nil
}

// reconnect restarts the core with the current nodes/settings.
func (a *App) reconnect() error {
	a.mu.Lock()
	was := a.running
	a.mu.Unlock()
	if !was {
		return nil
	}
	a.disconnect()
	return a.connect()
}

// ---- stability watchdog --------------------------------------------------------

func proxyClient(port int, timeout time.Duration) *http.Client {
	pu, _ := url.Parse("http://127.0.0.1:" + itoa(port))
	return &http.Client{
		Timeout:       timeout,
		Transport:     &http.Transport{Proxy: http.ProxyURL(pu), DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// ---- connectivity check (useful for "is X reachable right now?") ----------------

var checkTargets = []struct {
	name, url string
	direct    bool
}{
	{"Google", "https://www.google.com/generate_204", false},
	{"YouTube", "https://www.youtube.com/generate_204", false},
	{"GitHub", "https://github.com", false},
	{"ChatGPT", "https://chatgpt.com", false},
	{"Telegram", "https://telegram.org", false},
	{"Baidu", "https://www.baidu.com", true},
}

func (a *App) runChecks() {
	a.mu.Lock()
	if a.checking || !a.running {
		a.mu.Unlock()
		return
	}
	a.checking = true
	port := a.mixed
	a.mu.Unlock()

	res := make([]checkResult, len(checkTargets))
	var wg sync.WaitGroup
	for i, t := range checkTargets {
		wg.Add(1)
		go func(i int, name, u string, direct bool) {
			defer wg.Done()
			var c *http.Client
			if direct {
				c = &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
					CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			} else {
				c = proxyClient(port, 8*time.Second)
			}
			start := time.Now()
			resp, err := c.Get(u)
			ms := int(time.Since(start).Milliseconds())
			if err != nil {
				ms = -1
			} else {
				resp.Body.Close()
				if ms < 1 {
					ms = 1
				}
			}
			res[i] = checkResult{Name: name, MS: ms}
		}(i, t.name, t.url, t.direct)
	}
	wg.Wait()
	a.mu.Lock()
	a.checks = res
	a.checking = false
	a.mu.Unlock()
}

// ---- subscriptions ---------------------------------------------------------------

func sameLinks(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- core download ------------------------------------------------------------------

func (a *App) installCoreAsync() error {
	a.mu.Lock()
	if a.dl.active {
		a.mu.Unlock()
		return errors.New("already downloading")
	}
	a.dl.active, a.dl.done, a.dl.total, a.dl.err = true, 0, 0, ""
	mirror := a.set.Mirror
	a.mu.Unlock()
	go func() {
		err := installCore(mirror, func(done, total int64) {
			a.mu.Lock()
			a.dl.done, a.dl.total = done, total
			a.mu.Unlock()
		})
		a.mu.Lock()
		a.dl.active = false
		if err != nil {
			a.dl.err = err.Error()
		} else if p := corePath(); p != "" {
			a.mu.Unlock()
			v := coreVersion(p)
			a.mu.Lock()
			a.coreVer = v
		}
		a.mu.Unlock()
	}()
	return nil
}

// quitHook removes the tray icon (set by the native shell) so no ghost icon is left behind.
var quitHook func()

// themeHook recolours the native window frame when the theme changes (set by the native shell).
var themeHook func()

func (a *App) quit() {
	a.disconnect()
	if quitHook != nil {
		quitHook()
	}
	os.Exit(0)
}

// ---- state for the UI ------------------------------------------------------------------

type nodeView struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Type   string  `json:"type"`
	Server string  `json:"server"`
	Delay  int     `json:"delay"` // 0 = not tested, -1 = failed
	HasGeo bool    `json:"hasGeo"`
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
	Place  string  `json:"place"`
	Src    string  `json:"src"`
}

type histPt struct {
	D int64 `json:"d"`
	U int64 `json:"u"`
	P int   `json:"p"`
}

type importView struct {
	URL  string `json:"url"`
	Name string `json:"name"`
}

type stateView struct {
	Hist    []histPt    `json:"hist"`
	Import  *importView `json:"import,omitempty"`
	Version string      `json:"version"`
	Core    struct {
		Installed   bool   `json:"installed"`
		Version     string `json:"version"`
		Dir         string `json:"dir"`
		Downloading bool   `json:"downloading"`
		Done        int64  `json:"done"`
		Total       int64  `json:"total"`
		Error       string `json:"error"`
	} `json:"core"`
	Connected bool          `json:"connected"`
	Busy      string        `json:"busy"`
	Testing   bool          `json:"testing"`
	Error     string        `json:"error"`
	Pinned    bool          `json:"pinned"`
	NetDown   bool          `json:"netDown"`
	Live      int           `json:"live"`
	Active    string        `json:"active"`
	Nodes     []nodeView    `json:"nodes"`
	Up        int64         `json:"up"`
	Down      int64         `json:"down"`
	Checks    []checkResult `json:"checks"`
	Checking  bool          `json:"checking"`
	Profiles  []profileView `json:"profiles"`
	Settings  struct {
		Bypass      bool   `json:"bypass"`
		SysProxy    bool   `json:"sysProxy"`
		TestURL     string `json:"testUrl"`
		Mirror      string `json:"mirror"`
		AutoStart   bool   `json:"autoStart"`
		AutoConnect bool   `json:"autoConnect"`
		AutoUpdate  bool   `json:"autoUpdate"`
		LAN         bool   `json:"lan"`
		DirectList  string `json:"directList"`
		ProxyList   string `json:"proxyList"`
		CheckSecs   int    `json:"checkSecs"`
		ClashAPI    bool   `json:"clashApi"`
		SelfUpdate  bool   `json:"selfUpdate"`
		Theme       string `json:"theme"`
		NoAnim      bool   `json:"noAnim"`
		LinkAll     bool   `json:"linkAll"`
	} `json:"settings"`
	Update struct {
		Available string `json:"available"`
		Ready     bool   `json:"ready"`
		Notes     string `json:"notes"`
		Err       string `json:"err"`
	} `json:"update"`
	Welcomed   bool   `json:"welcomed"`
	Proxy      string `json:"proxy"`
	Controller string `json:"controller"` // Clash-compatible API address (when enabled)
	Secret     string `json:"secret"`
}

func (a *App) stateView() stateView {
	// keep delays fresh without hammering the core
	a.mu.Lock()
	need := a.running && time.Since(a.lastRefresh) > 1500*time.Millisecond
	a.mu.Unlock()
	if need {
		a.refresh()
	}

	var v stateView
	a.mu.Lock()
	defer a.mu.Unlock()
	v.Version = version
	v.Core.Installed = corePath() != ""
	v.Core.Version = a.coreVer
	v.Core.Dir = filepath.Join(dataDir, "core")
	v.Core.Downloading, v.Core.Done, v.Core.Total, v.Core.Error = a.dl.active, a.dl.done, a.dl.total, a.dl.err
	v.Connected, v.Busy, v.Testing, v.Error = a.running, a.busy, a.testing, a.err
	v.Pinned, v.Active, v.Live = a.pinned, a.active, a.liveMS
	v.NetDown = a.netDown
	v.Up, v.Down = a.up, a.down
	v.Hist = append([]histPt(nil), a.hist...)
	if p := a.importReq; p != nil {
		v.Import = &importView{URL: p.URL, Name: p.Name}
	}
	v.Settings.LinkAll = a.set.LinkAll
	v.Checks, v.Checking = a.checks, a.checking
	v.Nodes = make([]nodeView, 0, len(a.nodes))
	for _, n := range a.nodes {
		nv := nodeView{ID: n.ID, Name: n.Name, Type: n.Type, Server: n.Server, Delay: a.delays[n.ID], Src: n.Src}
		g := n.Geo
		if g == nil {
			g = a.geoCache[n.Server]
		}
		if g != nil {
			nv.HasGeo, nv.Lat, nv.Lon, nv.Place = true, g.Lat, g.Lon, g.Place
		}
		v.Nodes = append(v.Nodes, nv)
	}
	for _, p := range a.set.Profiles {
		v.Profiles = append(v.Profiles, profileView{ID: p.ID, Name: p.Name, Source: profileSource(p), Count: len(p.Links), Updated: p.Updated, Off: p.Off, Refreshable: isURLInput(p.Input)})
	}
	s := a.set
	v.Welcomed = s.Welcomed
	v.Update.Available, v.Update.Ready, v.Update.Notes, v.Update.Err = a.upd.Available, a.upd.Ready, a.upd.Notes, a.upd.Err
	v.Settings.SelfUpdate = s.SelfUpdate
	v.Settings.Theme = s.Theme
	v.Settings.NoAnim = s.NoAnim
	v.Settings.Bypass, v.Settings.SysProxy = s.Bypass, s.SysProxy
	v.Settings.TestURL, v.Settings.Mirror = s.TestURL, s.Mirror
	v.Settings.AutoStart, v.Settings.AutoConnect, v.Settings.AutoUpdate, v.Settings.LAN =
		s.AutoStart, s.AutoConnect, s.AutoUpdate, s.LAN
	v.Settings.DirectList, v.Settings.ProxyList = s.DirectList, s.ProxyList
	v.Settings.CheckSecs, v.Settings.ClashAPI = s.CheckSecs, s.ClashAPI
	if a.running {
		v.Proxy = fmt.Sprintf("127.0.0.1:%d", a.mixed)
		if s.ClashAPI {
			v.Controller, v.Secret = fmt.Sprintf("127.0.0.1:%d", a.apiPort), a.secret
		}
	}
	return v
}

type profileView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Source      string `json:"source"`
	Count       int    `json:"count"`
	Updated     string `json:"updated"`
	Off         bool   `json:"off"`
	Refreshable bool   `json:"refreshable"`
}

func profileSource(p Profile) string {
	if isURLInput(p.Input) {
		return profileName(p.Input)
	}
	return "pasted links"
}
