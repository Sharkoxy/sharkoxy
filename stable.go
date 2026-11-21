package main

// Stability engine: choosing the server, keeping it healthy, and recovering from failures.
//
//  connect      initialSelect: time all servers, pick the fastest, prove it carries traffic, only then set the system proxy
//  health loop  every 8s check the live server; two failures in a row -> fail over to the next best at once
//  scheduler    light re-test of the best few every CheckSecs, full re-test every 10 min and after sleep/network return
//  core crash   recoverCore restarts the core with back-off instead of giving up
//  no internet  never blames the servers for a dead local network and never flaps

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"sync"
	"time"
)

var netProbeURLs = []string{
	"http://connect.rom.miui.com/generate_204",
	"http://www.msftconnecttest.com/connecttest.txt",
	"http://www.baidu.com",
}

// netUp reports whether the PC itself has internet (checked WITHOUT the proxy).
func netUp() bool {
	if os.Getenv("SHARKOXY_NETCHECK") == "off" {
		return true
	}
	c := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res := make(chan bool, len(netProbeURLs))
	for _, u := range netProbeURLs {
		go func(u string) {
			resp, err := c.Get(u)
			if err == nil {
				resp.Body.Close()
			}
			res <- err == nil
		}(u)
	}
	for range netProbeURLs {
		if <-res {
			return true
		}
	}
	return false
}

func (a *App) setNet(down bool) {
	a.mu.Lock()
	if a.netDown != down {
		log.Printf("network %s", map[bool]string{true: "is down", false: "is back"}[down])
	}
	a.netDown = down
	a.mu.Unlock()
}

func (a *App) closeConnections() {
	_ = a.apiCall("DELETE", "/connections", nil, 3*time.Second, nil)
}

// measure times one node through the real proxy path. Zero/negative = failed.
func (a *App) measure(id, testURL string) int {
	var r struct {
		Delay int `json:"delay"`
	}
	err := a.apiCall("GET", "/proxies/"+url.PathEscape(id)+"/delay?url="+url.QueryEscape(testURL)+"&timeout=4500", nil, 7*time.Second, &r)
	if err != nil || r.Delay <= 0 {
		return -1
	}
	return r.Delay
}

// measureMany times the given nodes once, 12 at a time.
func (a *App) measureMany(ids []string, testURL string) []int {
	got := make([]int, len(ids))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 12)
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			got[i] = a.measure(id, testURL)
		}(i, id)
	}
	wg.Wait()
	return got
}

func (a *App) allIDs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var ids []string
	for _, n := range a.nodes {
		if a.inCfg[n.ID] {
			ids = append(ids, n.ID)
		}
	}
	return ids
}

// lightIDs: the active server, the 8 fastest known ones and a few never-tested ones.
func (a *App) lightIDs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	type kv struct {
		id string
		d  int
	}
	var good []kv
	var fresh []string
	for _, n := range a.nodes {
		if !a.inCfg[n.ID] {
			continue
		}
		switch d := a.delays[n.ID]; {
		case d > 0:
			good = append(good, kv{n.ID, d})
		case d == 0:
			fresh = append(fresh, n.ID)
		}
	}
	sort.Slice(good, func(i, j int) bool { return good[i].d < good[j].d })
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if id != "" && !seen[id] && a.inCfg[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	add(a.active)
	for i := 0; i < len(good) && i < 8; i++ {
		add(good[i].id)
	}
	for i := 0; i < len(fresh) && i < 4; i++ {
		add(fresh[i])
	}
	return ids
}

// runTest times ids over several passes and keeps each node's lowest sample (the warm figure, not a
// cold start). After every pass the selector is moved to the fastest working node.
func (a *App) runTest(ids []string, passes int, testURL string) bool {
	best := map[string]int{}
	anyEver := false
	for pass := 0; pass < passes; pass++ {
		got := a.measureMany(ids, testURL)
		ok := false
		for i, id := range ids {
			if got[i] > 0 {
				ok = true
				if b, has := best[id]; !has || got[i] < b {
					best[id] = got[i]
				}
			}
		}
		anyEver = anyEver || ok
		a.mu.Lock()
		if !a.running {
			a.mu.Unlock()
			return anyEver
		}
		for _, id := range ids {
			if b, has := best[id]; has {
				a.delays[id] = b
			} else if ok { // others answered, so the network is fine: this one is dead (for now)
				a.delays[id] = -1
			}
		}
		a.mu.Unlock()
		if ok {
			a.pickBest()
		}
	}
	return anyEver
}

// testAll re-tests servers. full = every server (3 passes); otherwise only the likely candidates (2 passes).
func (a *App) testAll(full bool) {
	a.mu.Lock()
	if !a.running || a.testing {
		a.mu.Unlock()
		return
	}
	a.testing = true
	testURL := a.set.TestURL
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.testing = false
		a.mu.Unlock()
		a.refresh()
	}()

	passes := 2
	ids := a.lightIDs()
	if full || len(ids) == 0 {
		ids, passes = a.allIDs(), 3
	}
	for attempt := 0; attempt < 3; attempt++ {
		if a.runTest(ids, passes, testURL) {
			a.setNet(false)
			return
		}
		a.mu.Lock()
		still := a.running
		a.mu.Unlock()
		if !still {
			return
		}
		if !netUp() { // not the servers' fault: keep what we know, the scheduler retries when the network returns
			a.setNet(true)
			return
		}
		a.setNet(false)
		time.Sleep(3 * time.Second)
	}
	a.mu.Lock()
	for _, id := range ids {
		a.delays[id] = -1
	}
	a.mu.Unlock()
}

// switchTo moves traffic to a node. killOld also closes connections that were riding the dead node.
func (a *App) switchTo(id string, killOld bool) error {
	if err := a.apiCall("PUT", "/proxies/proxy", map[string]string{"name": id}, 5*time.Second, nil); err != nil {
		return err
	}
	a.mu.Lock()
	a.active = id
	a.lastSwitch = time.Now()
	a.mu.Unlock()
	if killOld {
		a.closeConnections()
	}
	return nil
}

// pickBest moves the selector to the fastest working node unless the user pinned one.
// It only moves when it is worth it: the current server is dead, or a candidate is clearly faster
// (by 30 ms and 25 %) and the last move was over 45 s ago. Every candidate is re-verified first.
func (a *App) pickBest() {
	a.switchMu.Lock()
	defer a.switchMu.Unlock()
	a.mu.Lock()
	if !a.running || a.userPin != "" {
		a.mu.Unlock()
		return
	}
	type kv struct {
		id string
		d  int
	}
	var cands []kv
	for _, n := range a.nodes {
		if d := a.delays[n.ID]; d > 0 && a.inCfg[n.ID] {
			cands = append(cands, kv{n.ID, d})
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].d < cands[j].d })
	cur := a.active
	curD := a.delays[cur]
	since := time.Since(a.lastSwitch)
	if time.Since(a.startedAt) < 2*time.Minute {
		since = time.Hour // right after connecting, settle on the true fastest quickly
	}
	testURL := a.set.TestURL
	a.mu.Unlock()

	curDead := cur == "" || curD <= 0
	for i, c := range cands {
		if i >= 3 {
			return
		}
		if c.id == cur {
			return // already on the best
		}
		if !curDead {
			margin := curD / 4
			if margin < 30 {
				margin = 30
			}
			if c.d > curD-margin || since < 45*time.Second {
				return
			}
		}
		if a.measure(c.id, testURL) <= 0 { // looked fast earlier; confirm before moving traffic
			a.mu.Lock()
			a.delays[c.id] = -1
			a.mu.Unlock()
			continue
		}
		if err := a.switchTo(c.id, curDead && curD < 0); err == nil {
			log.Printf("switched to %s (%d ms), previous %q was %d", c.id, c.d, cur, curD)
		}
		return
	}
}

// initialSelect runs at connect: it must end with a server that carries traffic, or an error.
func (a *App) initialSelect() error {
	a.mu.Lock()
	a.testing = true
	testURL := a.set.TestURL
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.testing = false
		a.mu.Unlock()
	}()
	ids := a.allIDs()
	okCount := 0
	for attempt := 0; attempt < 3 && okCount == 0; attempt++ {
		if attempt > 0 {
			time.Sleep(3 * time.Second)
		}
		got := a.measureMany(ids, testURL)
		a.mu.Lock()
		for i, id := range ids {
			if got[i] > 0 {
				okCount++
				a.delays[id] = got[i]
			}
		}
		if okCount > 0 {
			for i, id := range ids {
				if got[i] <= 0 {
					a.delays[id] = -1
				}
			}
		}
		a.mu.Unlock()
	}
	if okCount == 0 {
		if !netUp() {
			a.setNet(true)
			return errors.New("no internet connection - will keep trying")
		}
		return fmt.Errorf("none of the %d servers answered. Update the subscription or check that it is still valid", len(ids))
	}
	a.setNet(false)
	a.refresh()
	// pickBest re-verifies its choice through the node itself (a full request to the test URL) before
	// switching, so a server that "answers" but carries nothing is skipped. Then one request through
	// our own local port, which also gives the live-latency number.
	a.pickBest()
	a.refresh()
	if !a.realProbe() {
		log.Println("note: the local proxy port did not answer the first request; continuing")
	}
	return nil
}

// realProbe fetches the test URL through the local proxy port: the whole path the apps will use.
func (a *App) realProbe() bool {
	a.mu.Lock()
	port, testURL := a.mixed, a.set.TestURL
	a.mu.Unlock()
	t0 := time.Now()
	resp, err := proxyClient(port, 7*time.Second).Get(testURL)
	if err != nil {
		return false
	}
	resp.Body.Close()
	a.mu.Lock()
	a.liveMS = int(time.Since(t0).Milliseconds())
	a.mu.Unlock()
	return true
}

// ---- health loop -------------------------------------------------------------------

// healthLoop watches the live server. Two failed checks in a row (about 16 s) trigger a fail-over.
func (a *App) healthLoop() {
	fails := 0
	for range time.Tick(8 * time.Second) {
		a.mu.Lock()
		ok := a.running && a.busy == "" && !a.testing && a.active != ""
		active, testURL := a.active, a.set.TestURL
		a.mu.Unlock()
		if !ok {
			fails = 0
			continue
		}
		healthy := a.realProbe()
		if !healthy && a.measure(active, testURL) > 0 {
			healthy = true // the server answers; a one-off local hiccup
		}
		if healthy {
			fails = 0
			continue
		}
		if fails++; fails < 2 {
			continue
		}
		fails = 0
		a.failover(active)
	}
}

func (a *App) failover(dead string) {
	a.mu.Lock()
	if a.active != dead || !a.running {
		a.mu.Unlock()
		return
	}
	a.delays[dead] = -1
	if a.userPin == dead {
		a.userPin = "" // a pinned server that died: back to automatic
	}
	a.mu.Unlock()
	log.Printf("server %s stopped answering, failing over", dead)
	a.pickBest()
	a.mu.Lock()
	moved := a.active != dead
	a.mu.Unlock()
	if moved {
		go a.testAll(false) // refresh the candidate list in the background
		return
	}
	go a.testAll(true) // nothing verified: full test (also decides whether the local network is down)
}

// ---- scheduler -----------------------------------------------------------------------

func waitNet(max time.Duration) {
	end := time.Now().Add(max)
	for time.Now().Before(end) {
		if netUp() {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

// fastestLoop keeps the choice fresh: light re-test every CheckSecs, full re-test every 10 minutes,
// after sleep, and when the network comes back.
func (a *App) fastestLoop() {
	now := time.Now()
	last, lastLight, lastFull, lastFresh, lastSys := now, now, now, now, now
	for range time.Tick(10 * time.Second) {
		now := time.Now()
		resumed := now.Sub(last) > 40*time.Second // the tick stalled: the PC was asleep
		last = now

		a.mu.Lock()
		ok := a.running && a.busy == ""
		every := time.Duration(a.set.CheckSecs) * time.Second
		pinnedDead := a.userPin != "" && a.delays[a.userPin] < 0
		down := a.netDown
		untested := true
		for _, d := range a.delays {
			if d > 0 {
				untested = false
				break
			}
		}
		a.mu.Unlock()
		if !ok {
			continue
		}
		switch {
		case resumed:
			log.Println("woke from sleep: dropping stale connections and re-testing")
			a.closeConnections()
			waitNet(30 * time.Second)
			a.testAll(true)
			lastLight, lastFull = time.Now(), time.Now()
			continue
		case down:
			if netUp() {
				a.setNet(false)
				a.closeConnections()
				a.testAll(true)
				lastLight, lastFull = time.Now(), time.Now()
			} else if now.Sub(lastFresh) >= 30*time.Second {
				// our own probes may be blocked while the servers still work: try the servers
				lastFresh = now
				a.testAll(false)
			}
			continue
		}
		if pinnedDead {
			log.Println("pinned server stopped answering, back to auto")
			_ = a.selectNode("auto")
		}
		if now.Sub(lastSys) >= 30*time.Second {
			lastSys = now
			a.assertSysProxy()
		}
		switch {
		case untested && now.Sub(lastFresh) >= 15*time.Second:
			lastFresh = now
			a.testAll(true)
			lastLight, lastFull = time.Now(), time.Now()
		case now.Sub(lastFull) >= 10*time.Minute:
			a.testAll(true)
			lastLight, lastFull = time.Now(), time.Now()
		case now.Sub(lastLight) >= every:
			a.testAll(false)
			lastLight = time.Now()
		}
	}
}

// assertSysProxy puts the system proxy back if something else changed it (max 3 times per session).
func (a *App) assertSysProxy() {
	a.mu.Lock()
	want := a.set.SysProxy && a.running && a.proxyFixes < 3
	addr := fmt.Sprintf("127.0.0.1:%d", a.mixed)
	a.mu.Unlock()
	if !want || sysProxyIs(addr) {
		return
	}
	log.Println("system proxy was changed by another program, restoring it")
	a.mu.Lock()
	a.proxyFixes++
	a.mu.Unlock()
	applySysProxy(true, addr)
}

// ---- core crash recovery ---------------------------------------------------------------

// recoverCore restarts the core after an unexpected exit, with back-off (1.5 s ... 30 s), up to 8 tries.
func (a *App) recoverCore() {
	for attempt := 0; attempt < 8; attempt++ {
		d := 1500 * time.Millisecond << uint(attempt)
		if d > 30*time.Second {
			d = 30 * time.Second
		}
		time.Sleep(d)
		a.mu.Lock()
		gone := a.restart == 99 || a.running // user disconnected, or someone already reconnected
		a.mu.Unlock()
		if gone {
			return
		}
		if a.begin("reconnecting") != nil {
			continue
		}
		err := a.connect()
		if err != nil {
			a.disconnect()
		}
		a.end()
		if err == nil {
			log.Printf("core restarted after crash (attempt %d)", attempt+1)
			return
		}
		log.Println("core restart failed:", err)
	}
	a.setErr(errors.New("The core keeps stopping: " + tailFile(coreLogPath(), 2)))
}

func coreLogPath() string { return dataDir + string(os.PathSeparator) + "core.log" }
