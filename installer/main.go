//go:build windows

// Sharkoxy installer / uninstaller. One small exe, no dependencies:
//
//	Sharkoxy-Setup.exe             installs for the current user (no admin rights needed)
//	<install dir>\Uninstall.exe    removes it again
package main

import (
	_ "embed"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

//go:embed payload/Sharkoxy.exe
var appExe []byte

//go:embed sharkoxy.ico
var appIco []byte

const (
	appName   = "Sharkoxy"
	uninstKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\Sharkoxy`
	version   = "1.4.0"
)

var (
	user32     = syscall.NewLazyDLL("user32.dll")
	msgBoxProc = user32.NewProc("MessageBoxW")
)

const (
	mbOK, mbYesNo, mbIconInfo, mbIconQuestion, mbIconError = 0x0, 0x4, 0x40, 0x20, 0x10
	mbDefault2                                             = 0x100
	idYes                                                  = 6
)

// silent mode (for scripts and package managers): /S, /silent, /quiet or /VERYSILENT. No windows, answers are the safe defaults.
var silent = func() bool {
	for _, a := range os.Args[1:] {
		switch strings.ToLower(a) {
		case "/s", "-s", "/silent", "-silent", "/quiet", "/verysilent", "/qn":
			return true
		}
	}
	return false
}()

func msgBox(title, text string, flags uintptr) int {
	if silent {
		if flags&mbDefault2 != 0 {
			return 7 // "No": keep the person's data
		}
		if flags&0xF != mbYesNo {
			return 1 // OK
		}
		return idYes
	}
	t, _ := syscall.UTF16PtrFromString(text)
	c, _ := syscall.UTF16PtrFromString(title)
	r, _, _ := msgBoxProc.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), flags|0x10000) // MB_SETFOREGROUND
	return int(r)
}

func hidden(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return c
}

func installDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base, _ = os.UserHomeDir()
	}
	return filepath.Join(base, "Programs", appName)
}

func startMenuDir() string {
	return filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs`)
}
func desktopDir() string { return filepath.Join(os.Getenv("USERPROFILE"), "Desktop") }

func psq(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func makeShortcut(lnk, target, icon string) error {
	script := fmt.Sprintf(`$s=(New-Object -ComObject WScript.Shell).CreateShortcut(%s);$s.TargetPath=%s;$s.WorkingDirectory=%s;$s.IconLocation=%s;$s.Description='Sharkoxy';$s.Save()`,
		psq(lnk), psq(target), psq(filepath.Dir(target)), psq(icon))
	return hidden("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script).Run()
}

// quitApp asks a running Sharkoxy to close itself (it restores the system proxy and removes its tray icon),
// then falls back to killing it.
func quitApp() {
	// a bare HTTP request keeps the installer small (no net/http)
	if c, err := net.DialTimeout("tcp", "127.0.0.1:17800", 800*time.Millisecond); err == nil {
		_ = c.SetDeadline(time.Now().Add(2 * time.Second))
		_, _ = c.Write([]byte("POST /_quit HTTP/1.1\r\nHost: 127.0.0.1:17800\r\nX-Sharkoxy: 1\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
		buf := make([]byte, 64)
		_, _ = c.Read(buf)
		c.Close()
		for i := 0; i < 20; i++ { // wait up to ~5 s for it to leave
			time.Sleep(250 * time.Millisecond)
			c, err := net.DialTimeout("tcp", "127.0.0.1:17800", 200*time.Millisecond)
			if err != nil {
				break
			}
			c.Close()
		}
	}
	killApp()
}

func killApp() {
	_ = hidden("taskkill", "/F", "/T", "/IM", "Sharkoxy.exe").Run()
	_ = hidden("taskkill", "/F", "/T", "/IM", "sing-box.exe").Run()
}

// removeLegacy cleans up an install made before the project was renamed from FastNode.
func removeLegacy() {
	_ = hidden("taskkill", "/F", "/T", "/IM", "FastNode.exe").Run()
	_ = hidden("reg", "delete", `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\FastNode`, "/f").Run()
	_ = hidden("reg", "delete", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", "FastNode", "/f").Run()
	_ = os.Remove(filepath.Join(startMenuDir(), "FastNode.lnk"))
	_ = os.Remove(filepath.Join(desktopDir(), "FastNode.lnk"))
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		_ = os.RemoveAll(filepath.Join(base, "Programs", "FastNode"))
	}
}

func install() error {
	dir := installDir()
	quitApp()
	removeLegacy() // upgrading over a running copy
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	exe := filepath.Join(dir, "Sharkoxy.exe")
	if err := os.WriteFile(exe, appExe, 0o755); err != nil {
		return fmt.Errorf("could not write %s: %v", exe, err)
	}
	ico := filepath.Join(dir, "Sharkoxy.ico")
	_ = os.WriteFile(ico, appIco, 0o644)

	// core bundled into this installer (zip appended to the exe)
	bundled := false
	if self, err := os.Executable(); err == nil {
		if ok, err := extractTrailer(self, dir); err != nil {
			msgBox("Sharkoxy Setup", "The bundled sing-box files could not be unpacked:\n"+err.Error(), mbOK|mbIconError)
		} else {
			bundled = ok
		}
	}
	// otherwise keep a core that sits next to the installer
	if self, err := os.Executable(); err == nil && !bundled {
		if b, err := os.ReadFile(filepath.Join(filepath.Dir(self), "sing-box.exe")); err == nil {
			_ = os.WriteFile(filepath.Join(dir, "sing-box.exe"), b, 0o755)
		}
	}
	// the uninstaller is this same program
	un := filepath.Join(dir, "Uninstall.exe")
	if self, err := os.Executable(); err == nil {
		_ = copyExeOnly(self, un) // without the bundled zip
	}
	_ = makeShortcut(filepath.Join(startMenuDir(), appName+".lnk"), exe, ico)
	_ = makeShortcut(filepath.Join(desktopDir(), appName+".lnk"), exe, ico)

	reg := func(name, typ, val string) {
		_ = hidden("reg", "add", uninstKey, "/v", name, "/t", typ, "/d", val, "/f").Run()
	}
	reg("DisplayName", "REG_SZ", appName)
	reg("DisplayVersion", "REG_SZ", version)
	reg("Publisher", "REG_SZ", appName)
	reg("DisplayIcon", "REG_SZ", ico)
	reg("InstallLocation", "REG_SZ", dir)
	reg("UninstallString", "REG_SZ", `"`+un+`" /uninstall`)
	reg("NoModify", "REG_DWORD", "1")
	reg("NoRepair", "REG_DWORD", "1")
	return nil
}

func uninstall() {
	dir := installDir()
	data := filepath.Join(os.Getenv("APPDATA"), appName)
	quitApp()
	// never leave Windows pointing at a proxy that no longer exists
	const inet = `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	if out, err := hidden("reg", "query", inet, "/v", "ProxyServer").Output(); err == nil && strings.Contains(string(out), "127.0.0.1:") {
		_ = hidden("reg", "add", inet, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f").Run()
		_ = hidden("reg", "delete", inet, "/v", "ProxyServer", "/f").Run()
	}
	_ = hidden("reg", "delete", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", appName, "/f").Run()
	_ = hidden("reg", "delete", uninstKey, "/f").Run()
	_ = hidden("reg", "delete", `HKCU\Software\Classes\sharkoxy`, "/f").Run()
	for _, sc := range []string{"clash", "sing-box"} { // only if we were the one handling them
		k := `HKCU\Software\Classes\` + sc
		if out, err := hidden("reg", "query", k+`\shell\open\command`, "/ve").Output(); err == nil && strings.Contains(string(out), "Sharkoxy") {
			_ = hidden("reg", "delete", k, "/f").Run()
		}
	}
	_ = os.Remove(filepath.Join(startMenuDir(), appName+".lnk"))
	_ = os.Remove(filepath.Join(desktopDir(), appName+".lnk"))
	kept := true
	// default answer is No: a reinstall or a mistaken click must not cost anyone their subscriptions
	if msgBox("Uninstall Sharkoxy", "Also delete your profiles, settings and logs?\n\nChoose No to keep them (handy if you reinstall later).", mbYesNo|mbIconQuestion|mbDefault2) == idYes {
		_ = os.RemoveAll(data)
		kept = false
	}
	// this exe lives in dir, so remove the folder from a helper once we have exited (retries while files unlock)
	_ = hidden("cmd", "/c", fmt.Sprintf(`for /l %%i in (1,1,15) do (rmdir /s /q "%s" 2>nul & if not exist "%s" exit /b & ping 127.0.0.1 -n 2 >nul)`, dir, dir)).Start()
	msg := "Sharkoxy was removed from this PC."
	if kept {
		msg += "\n\nYour settings are kept in:\n" + data
	}
	msgBox("Uninstall Sharkoxy", msg, mbOK|mbIconInfo)
}

func main() {
	for _, a := range os.Args[1:] {
		if strings.EqualFold(a, "/uninstall") || strings.EqualFold(a, "-uninstall") {
			if msgBox("Uninstall Sharkoxy", "Remove Sharkoxy from this PC?", mbYesNo|mbIconQuestion) == idYes {
				uninstall()
			}
			return
		}
	}
	if msgBox("Sharkoxy Setup", "Install Sharkoxy "+version+" for your Windows account?\n\nIt will be placed in:\n"+installDir()+"\n\nA Start menu and desktop shortcut are created. No administrator rights needed.", mbYesNo|mbIconQuestion) != idYes {
		return
	}
	if err := install(); err != nil {
		msgBox("Sharkoxy Setup", "Install failed:\n"+err.Error(), mbOK|mbIconError)
		os.Exit(1)
	}
	note := ""
	if _, err := os.Stat(filepath.Join(installDir(), "sing-box.exe")); err != nil {
		note = "\n\nNote: sing-box.exe was not included. Sharkoxy will ask to download it on first start."
	}
	if !silent && msgBox("Sharkoxy Setup", "Sharkoxy is installed."+note+"\n\nStart it now?", mbYesNo|mbIconInfo) == idYes {
		_ = exec.Command(filepath.Join(installDir(), "Sharkoxy.exe")).Start()
	}
}
