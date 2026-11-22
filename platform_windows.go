//go:build windows

package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

func hideWindow(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}

const regKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`

func reg(args ...string) error {
	c := exec.Command("reg", args...)
	hideWindow(c)
	return c.Run()
}

func notifyWininet() {
	dll := syscall.NewLazyDLL("wininet.dll")
	p := dll.NewProc("InternetSetOptionW")
	p.Call(0, 39, 0, 0) // INTERNET_OPTION_SETTINGS_CHANGED
	p.Call(0, 37, 0, 0) // INTERNET_OPTION_REFRESH
}

// setSystemProxy switches the Windows system proxy on (addr = host:port) or off.
func setSystemProxy(on bool, addr string) error {
	if on {
		if err := reg("add", regKey, "/v", "ProxyServer", "/t", "REG_SZ", "/d", addr, "/f"); err != nil {
			return err
		}
		_ = reg("add", regKey, "/v", "ProxyOverride", "/t", "REG_SZ", "/d",
			"localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;172.2*;172.30.*;172.31.*;192.168.*;<local>", "/f")
		if err := reg("add", regKey, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f"); err != nil {
			return err
		}
	} else {
		if err := reg("add", regKey, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f"); err != nil {
			return err
		}
	}
	notifyWininet()
	return nil
}

func browserCandidates() []string {
	var out []string
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LocalAppData"} {
		root := os.Getenv(env)
		if root == "" {
			continue
		}
		out = append(out,
			filepath.Join(root, `Microsoft\Edge\Application\msedge.exe`),
			filepath.Join(root, `Google\Chrome\Application\chrome.exe`))
	}
	return out
}

// openWindow shows the UI as a standalone app-style window (Edge/Chrome --app mode).
func openWindow(url string, onClose func()) {
	for _, p := range browserCandidates() {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		cmd := exec.Command(p, "--app="+url, "--window-size=1120,780",
			"--user-data-dir="+filepath.Join(dataDir, "window"),
			"--no-first-run", "--no-default-browser-check", "--disable-sync")
		if err := cmd.Start(); err != nil {
			continue
		}
		if onClose != nil {
			go func() { _ = cmd.Wait(); onClose() }()
		}
		return
	}
	c := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	hideWindow(c)
	_ = c.Start()
}

func setAutostart(on bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	key := `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	if on {
		return reg("add", key, "/v", "Sharkoxy", "/t", "REG_SZ", "/d", `"`+exe+`" -background`, "/f")
	}
	_ = reg("delete", key, "/v", "Sharkoxy", "/f")
	_ = reg("delete", key, "/v", "FastNode", "/f") // pre-rename entry
	return nil
}

func openFolder(dir string) {
	_ = os.MkdirAll(dir, 0o755)
	_ = exec.Command("explorer.exe", dir).Start()
}

// sysProxyIs reports whether the current user's system proxy is on and points at addr.
func sysProxyIs(addr string) bool {
	out, err := regQuery("ProxyEnable")
	if err != nil || !strings.Contains(string(out), "0x1") {
		return false
	}
	out, err = regQuery("ProxyServer")
	return err == nil && strings.Contains(string(out), addr)
}

func regQuery(name string) ([]byte, error) {
	c := exec.Command("reg", "query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`, "/v", name)
	hideWindow(c)
	return c.Output()
}

// swapExe puts the new exe in place of the running one. Windows lets a running exe be renamed but not
// overwritten, so the old file is moved aside (and deleted on the next start).
func swapExe(newPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	src, err := os.Open(newPath)
	if err == nil {
		var dst *os.File
		if dst, err = os.OpenFile(exe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755); err == nil {
			_, err = io.Copy(dst, src)
			if cerr := dst.Close(); err == nil {
				err = cerr
			}
		}
		src.Close()
	}
	if err != nil { // put the old one back
		_ = os.Remove(exe)
		_ = os.Rename(old, exe)
		return err
	}
	return nil
}

func restartSelf(show bool) bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	var args []string
	if !show {
		args = []string{"-background"}
	}
	c := exec.Command(exe, args...)
	c.Env = append(os.Environ(), "SHARKOXY_RESTART=1")
	hideWindow(c)
	return c.Start() == nil
}

// registerProtocols makes links like sharkoxy://add?url=... open this app. clash:// and sing-box:// are only
// claimed when the person asks for it, so we never steal them from another client by surprise.
func registerProtocols(all bool) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := `"` + exe + `" "%1"`
	schemes := []string{"sharkoxy"}
	if all {
		schemes = append(schemes, "clash", "sing-box")
	}
	for _, s := range schemes {
		k := `HKCU\Software\Classes\` + s
		_ = reg("add", k, "/ve", "/d", "URL:Sharkoxy", "/f")
		_ = reg("add", k, "/v", "URL Protocol", "/d", "", "/f")
		_ = reg("add", k+`\shell\open\command`, "/ve", "/d", cmd, "/f")
	}
	if !all { // give them back if we had them
		for _, s := range []string{"clash", "sing-box"} {
			k := `HKCU\Software\Classes\` + s
			c := exec.Command("reg", "query", k+`\shell\open\command`, "/ve")
			hideWindow(c)
			if out, err := c.Output(); err == nil && strings.Contains(string(out), "Sharkoxy") {
				_ = reg("delete", k, "/f")
			}
		}
	}
}

// readClipboard returns the text on the clipboard (empty if there is none).
func readClipboard() string {
	u32 := syscall.NewLazyDLL("user32.dll")
	k32 := syscall.NewLazyDLL("kernel32.dll")
	open, closeC, get := u32.NewProc("OpenClipboard"), u32.NewProc("CloseClipboard"), u32.NewProc("GetClipboardData")
	lock, unlock := k32.NewProc("GlobalLock"), k32.NewProc("GlobalUnlock")
	if r, _, _ := open.Call(0); r == 0 {
		return ""
	}
	defer closeC.Call()
	h, _, _ := get.Call(13) // CF_UNICODETEXT
	if h == 0 {
		return ""
	}
	p, _, _ := lock.Call(h)
	if p == 0 {
		return ""
	}
	defer unlock.Call(h)
	var u []uint16
	for i := 0; i < 16384; i++ {
		c := *(*uint16)(unsafe.Pointer(p + uintptr(i)*2))
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return syscall.UTF16ToString(u)
}
