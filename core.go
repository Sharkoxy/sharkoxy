package main

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

func coreDir() string { return filepath.Join(dataDir, "core") }

func coreName() string {
	if runtime.GOOS == "windows" {
		return "sing-box.exe"
	}
	return "sing-box"
}

// corePath finds sing-box next to the app, or in the data folder.
func corePath() string {
	if p := envAny("SHARKOXY_CORE", "FASTNODE_CORE"); p != "" {
		return p
	}
	var cands []string
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Join(filepath.Dir(exe), coreName()))
	}
	cands = append(cands, filepath.Join(dataDir, "core", coreName()))
	for _, p := range cands {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func coreVersion(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	line := strings.SplitN(string(out), "\n", 2)[0]
	f := strings.Fields(line)
	if len(f) >= 3 {
		return f[2]
	}
	return ""
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// freePortPrefer returns want if it is free, otherwise any free port.
func freePortPrefer(want int) (int, error) {
	if l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", want)); err == nil {
		l.Close()
		return want, nil
	}
	return freePort()
}

// probeAlive TCP-dials every TCP-based node in parallel and keeps the ones that answer.
// QUIC based nodes can't be probed this way and are always kept. If nothing answers
// (e.g. the probe itself is being blocked) every node is kept.
func probeAlive(nodes []Node) []Node {
	alive := make([]bool, len(nodes))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 64)
	for i, n := range nodes {
		if n.UDP {
			alive[i] = true
			continue
		}
		wg.Add(1)
		go func(i int, n Node) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			c, err := net.DialTimeout("tcp", net.JoinHostPort(n.Server, fmt.Sprint(n.Port)), 2500*time.Millisecond)
			if err == nil {
				c.Close()
				alive[i] = true
			}
		}(i, n)
	}
	wg.Wait()
	var out []Node
	for i, n := range nodes {
		if alive[i] {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nodes
	}
	return out
}

// ---- core download -------------------------------------------------------

const fallbackTag = "v1.12.0"

func latestTag(mirror string) string {
	c := &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	base := "https://github.com/SagerNet/sing-box/releases/latest"
	urls := []string{base}
	if mirror != "" {
		urls = append(urls, strings.TrimRight(mirror, "/")+"/"+base)
	}
	for _, u := range urls {
		resp, err := c.Get(u)
		if err != nil {
			continue
		}
		resp.Body.Close()
		loc := resp.Header.Get("Location")
		if i := strings.Index(loc, "/tag/"); i >= 0 {
			return loc[i+5:]
		}
	}
	// second route: the API
	c2 := &http.Client{Timeout: 15 * time.Second}
	if resp, err := c2.Get("https://api.github.com/repos/SagerNet/sing-box/releases/latest"); err == nil {
		defer resp.Body.Close()
		var r struct {
			Tag string `json:"tag_name"`
		}
		if json.NewDecoder(resp.Body).Decode(&r) == nil && r.Tag != "" {
			return r.Tag
		}
	}
	return fallbackTag
}

type progressWriter struct {
	done, total int64
	fn          func(done, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	p.fn(p.done, p.total)
	return len(b), nil
}

func downloadTo(url, dest string, fn func(done, total int64)) error {
	c := &http.Client{Timeout: 10 * time.Minute}
	resp, err := c.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	pw := &progressWriter{total: resp.ContentLength, fn: fn}
	_, err = io.Copy(f, io.TeeReader(resp.Body, pw))
	return err
}

func unzipFlat(zipPath, destDir string) (bool, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return false, err
	}
	defer zr.Close()
	found := false
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		base := filepath.Base(f.Name) // flatten: also protects against path tricks
		if base == "." || base == ".." || base == string(filepath.Separator) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return false, err
		}
		out, err := os.OpenFile(filepath.Join(destDir, base), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			rc.Close()
			return false, err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return false, err
		}
		if base == coreName() {
			found = true
		}
	}
	return found, nil
}

func installCore(mirror string, fn func(done, total int64)) error {
	if runtime.GOOS != "windows" {
		return errors.New("automatic core download is only built for Windows - place a sing-box binary next to the app")
	}
	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		return fmt.Errorf("no sing-box build for %s", arch)
	}
	tag := latestTag(mirror)
	ver := strings.TrimPrefix(tag, "v")
	asset := fmt.Sprintf("sing-box-%s-windows-%s.zip", ver, arch)
	base := "https://github.com/SagerNet/sing-box/releases/download/" + tag + "/" + asset
	urls := []string{base}
	if mirror != "" {
		urls = append(urls, strings.TrimRight(mirror, "/")+"/"+base)
	}
	dir := filepath.Join(dataDir, "core")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "download.zip")
	defer os.Remove(tmp)
	var lastErr error
	for _, u := range urls {
		if lastErr = downloadTo(u, tmp, fn); lastErr == nil {
			break
		}
	}
	if lastErr != nil {
		return fmt.Errorf("download failed (%v). Set a GitHub mirror prefix in Settings, or put sing-box.exe next to Sharkoxy.exe", lastErr)
	}
	ok, err := unzipFlat(tmp, dir)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("downloaded archive did not contain sing-box.exe")
	}
	return nil
}
