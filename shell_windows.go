//go:build windows

package main

// Native Windows shell: a real top-level window that hosts Microsoft WebView2 (the Edge engine),
// a system-tray icon with a menu, close-to-tray, and "second launch brings up the first window".
// Written against plain Win32 / COM through syscall, so it needs no third-party Go modules.
// If WebView2 can't start, the window falls back to an Edge/Chrome app window.

import (
	_ "embed"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

//go:embed assets/WebView2Loader.dll
var loaderDLL []byte

//go:embed assets/app.ico
var iconICO []byte

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")

	pRegisterClassEx  = user32.NewProc("RegisterClassExW")
	pDwmSetAttr       = syscall.NewLazyDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
	pCreateWindowEx   = user32.NewProc("CreateWindowExW")
	pDefWindowProc    = user32.NewProc("DefWindowProcW")
	pDestroyWindow    = user32.NewProc("DestroyWindow")
	pShowWindow       = user32.NewProc("ShowWindow")
	pSetForeground    = user32.NewProc("SetForegroundWindow")
	pGetMessage       = user32.NewProc("GetMessageW")
	pTranslateMessage = user32.NewProc("TranslateMessage")
	pDispatchMessage  = user32.NewProc("DispatchMessageW")
	pPostMessage      = user32.NewProc("PostMessageW")
	pGetClientRect    = user32.NewProc("GetClientRect")
	pLoadImage        = user32.NewProc("LoadImageW")
	pLoadCursor       = user32.NewProc("LoadCursorW")
	pCreatePopupMenu  = user32.NewProc("CreatePopupMenu")
	pAppendMenu       = user32.NewProc("AppendMenuW")
	pTrackPopupMenu   = user32.NewProc("TrackPopupMenu")
	pDestroyMenu      = user32.NewProc("DestroyMenu")
	pGetCursorPos     = user32.NewProc("GetCursorPos")
	pRegisterWinMsg   = user32.NewProc("RegisterWindowMessageW")
	pSetTimer         = user32.NewProc("SetTimer")
	pKillTimer        = user32.NewProc("KillTimer")
	pSysParamsInfo    = user32.NewProc("SystemParametersInfoW")
	pSetWindowPos     = user32.NewProc("SetWindowPos")
	pGetDpiForSystem  = user32.NewProc("GetDpiForSystem")
	pSetDpiAwareness  = user32.NewProc("SetProcessDpiAwarenessContext")
	pGetModuleHandle  = kernel32.NewProc("GetModuleHandleW")
	pShellNotifyIcon  = shell32.NewProc("Shell_NotifyIconW")
	pCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	pCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
)

const (
	wmDestroy       = 0x0002
	wmSize          = 0x0005
	wmSetFocus      = 0x0007
	wmEndSession    = 0x0016
	wmGetMinMaxInfo = 0x0024
	wmTimer         = 0x0113
	wmDpiChanged    = 0x02E0
	wmShow          = 0x8000 + 1 // our own: bring the window up
	wmTray          = 0x8000 + 2 // tray icon callback

	swShow    = 5
	swRestore = 9

	wsOverlappedWindow = 0x00CF0000
	wsClipChildren     = 0x02000000
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type winMsg struct {
	HWnd    uintptr
	Message uint32
	_       uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	PtX     int32
	PtY     int32
	_       uint32
}

type winRect struct{ L, T, R, B int32 }

type notifyIcon struct {
	Size        uint32
	HWnd        uintptr
	ID          uint32
	Flags       uint32
	CallbackMsg uint32
	Icon        uintptr
	Tip         [128]uint16
	State       uint32
	StateMask   uint32
	Info        [256]uint16
	Version     uint32
	InfoTitle   [64]uint16
	InfoFlags   uint32
	Guid        [16]byte
	BalloonIcon uintptr
}

// everything below is touched only from the UI thread
var (
	shellApp   *App
	shellURL   string
	hostHwnd   uintptr
	mainHwnd   uintptr
	hIcon      uintptr
	hInstance  uintptr
	dpiScale   = 1.0
	nid        notifyIcon
	taskbarMsg uintptr

	// WebView2 objects (raw COM pointers)
	wvEnv, wvCtrl, wvView uintptr
	outPtr                uintptr // scratch out-parameter (global so the address never moves)
	rcScratch             winRect
)

func u16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }
func up(p *uint16) uintptr { return uintptr(unsafe.Pointer(p)) }

func safe(name string) {
	if r := recover(); r != nil {
		log.Printf("shell: panic in %s: %v", name, r)
	}
}

// ---- COM helpers ------------------------------------------------------------------

func vcall(obj uintptr, idx int, args ...uintptr) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	fn := *(*uintptr)(unsafe.Pointer(vtbl + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}
func comAddRef(o uintptr) {
	if o != 0 {
		vcall(o, 1)
	}
}
func comRelease(o uintptr) {
	if o != 0 {
		vcall(o, 2)
	}
}

// a COM object implemented in Go: pointer to a vtable of {QueryInterface, AddRef, Release, Invoke}
type comObj struct{ vtbl *[4]uintptr }

func newHandler(invoke func(this, hr, p uintptr) uintptr) *comObj {
	o := &comObj{}
	o.vtbl = &[4]uintptr{
		syscall.NewCallback(func(this, iid, ppv uintptr) uintptr { // QueryInterface: always ourselves
			if ppv != 0 {
				*(*uintptr)(unsafe.Pointer(ppv)) = this
			}
			return 0
		}),
		syscall.NewCallback(func(this uintptr) uintptr { return 1 }),
		syscall.NewCallback(func(this uintptr) uintptr { return 1 }),
		syscall.NewCallback(func(this, hr, p uintptr) uintptr {
			defer safe("webview callback")
			return invoke(this, hr, p)
		}),
	}
	return o
}

var envHandler, ctrlHandler *comObj

func initHandlers() {
	envHandler = newHandler(func(this, hr, env uintptr) uintptr {
		if int32(hr) < 0 || env == 0 {
			log.Printf("webview2: environment failed (0x%x)", uint32(hr))
			fallbackWindow()
			return 0
		}
		comAddRef(env)
		wvEnv = env
		if mainHwnd == 0 {
			return 0
		}
		if r := vcall(env, 3, mainHwnd, uintptr(unsafe.Pointer(ctrlHandler))); int32(r) < 0 {
			log.Printf("webview2: controller failed (0x%x)", uint32(r))
			fallbackWindow()
		}
		return 0
	})
	ctrlHandler = newHandler(func(this, hr, ctrl uintptr) uintptr {
		if int32(hr) < 0 || ctrl == 0 {
			log.Printf("webview2: controller creation failed (0x%x)", uint32(hr))
			fallbackWindow()
			return 0
		}
		comAddRef(ctrl)
		wvCtrl = ctrl
		outPtr = 0
		vcall(ctrl, 25, uintptr(unsafe.Pointer(&outPtr))) // get_CoreWebView2
		wvView = outPtr
		comAddRef(wvView)
		// tidy the page: no status bar, no browser context menu
		outPtr = 0
		if vcall(wvView, 3, uintptr(unsafe.Pointer(&outPtr))) == 0 && outPtr != 0 { // get_Settings
			vcall(outPtr, 10, 0) // put_IsStatusBarEnabled(false)
			vcall(outPtr, 14, 0) // put_AreDefaultContextMenusEnabled(false)
			vcall(outPtr, 12, 0) // put_AreDevToolsEnabled(false): it should feel like an app, not a browser
			comRelease(outPtr)
		}
		resizeWV()
		nav := u16(shellURL)
		vcall(wvView, 5, up(nav)) // Navigate
		runtime.KeepAlive(nav)
		vcall(ctrl, 4, 1)  // put_IsVisible(true)
		vcall(ctrl, 12, 0) // MoveFocus(programmatic)
		log.Println("webview2: ready")
		return 0
	})
}

func resizeWV() {
	if wvCtrl == 0 || mainHwnd == 0 {
		return
	}
	pGetClientRect.Call(mainHwnd, uintptr(unsafe.Pointer(&rcScratch)))
	vcall(wvCtrl, 6, uintptr(unsafe.Pointer(&rcScratch))) // put_Bounds
}

func closeWV() {
	if wvCtrl != 0 {
		vcall(wvCtrl, 24) // Close
		comRelease(wvCtrl)
	}
	comRelease(wvView)
	comRelease(wvEnv)
	wvCtrl, wvView, wvEnv = 0, 0, 0
}

func ensureLoader() (string, error) {
	dir := filepath.Join(dataDir, "webview2")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "WebView2Loader.dll")
	if st, err := os.Stat(p); err != nil || st.Size() != int64(len(loaderDLL)) {
		if err := os.WriteFile(p, loaderDLL, 0o755); err != nil {
			return "", err
		}
	}
	return p, nil
}

func startWebView() {
	// The page is served by this very program on 127.0.0.1. Never route it through the Windows system proxy
	// (that is our own proxy, which is down while disconnected or restarting: that was the Edge "no connection" page).
	_ = os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "--no-proxy-server --disable-features=msSmartScreenProtection")
	if runtime.GOARCH != "amd64" {
		fallbackWindow()
		return
	}
	p, err := ensureLoader()
	if err != nil {
		log.Println("webview2: loader:", err)
		fallbackWindow()
		return
	}
	dll, err := syscall.LoadDLL(p)
	if err != nil {
		log.Println("webview2: load:", err)
		fallbackWindow()
		return
	}
	proc, err := dll.FindProc("CreateCoreWebView2EnvironmentWithOptions")
	if err != nil {
		log.Println("webview2: proc:", err)
		fallbackWindow()
		return
	}
	udf := u16(filepath.Join(dataDir, "webview"))
	r, _, _ := proc.Call(0, up(udf), 0, uintptr(unsafe.Pointer(envHandler)))
	runtime.KeepAlive(udf)
	if int32(r) < 0 {
		log.Printf("webview2: runtime not available (0x%x)", uint32(r))
		fallbackWindow()
	}
}

// fallbackWindow is used when WebView2 can't run: close our window, open the UI in Edge/Chrome app mode.
func fallbackWindow() {
	if mainHwnd != 0 {
		h := mainHwnd
		pDestroyWindow.Call(h)
	}
	openWindow(shellURL, nil)
}

// ---- windows -------------------------------------------------------------------------

func mainProc(hwnd, msg, wp, lp uintptr) uintptr {
	defer safe("mainProc")
	switch msg {
	case wmSize:
		resizeWV()
		return 0
	case wmSetFocus:
		if wvCtrl != 0 {
			vcall(wvCtrl, 12, 0)
		}
		return 0
	case wmGetMinMaxInfo:
		mm := (*[10]int32)(unsafe.Pointer(lp))
		mm[6], mm[7] = int32(420*dpiScale), int32(540*dpiScale)
		return 0
	case wmDpiChanged:
		r := (*winRect)(unsafe.Pointer(lp))
		pSetWindowPos.Call(hwnd, 0, uintptr(r.L), uintptr(r.T), uintptr(r.R-r.L), uintptr(r.B-r.T), 0x4|0x10)
		return 0
	case wmTimer:
		pKillTimer.Call(hwnd, 1)
		if wvCtrl == 0 && mainHwnd == hwnd {
			log.Println("webview2: did not start in time")
			fallbackWindow()
		}
		return 0
	case 0x0010: // WM_CLOSE: hide to the tray instead of destroying the window and the page inside it
		pShowWindow.Call(hwnd, 0)
		return 0
	case wmDestroy: // window really going away
		closeWV()
		mainHwnd = 0
		return 0
	}
	r, _, _ := pDefWindowProc.Call(hwnd, msg, wp, lp)
	return r
}

func showMain() {
	if mainHwnd != 0 {
		pShowWindow.Call(mainHwnd, swRestore)
		pSetForeground.Call(mainHwnd)
		return
	}
	// size: 1120x780 (scaled for DPI), kept inside the work area and centred
	var wa winRect
	pSysParamsInfo.Call(0x30, 0, uintptr(unsafe.Pointer(&wa)), 0)
	w, h := int32(1120*dpiScale), int32(780*dpiScale)
	if aw := wa.R - wa.L; aw > 0 && w > aw {
		w = aw
	}
	if ah := wa.B - wa.T; ah > 0 && h > ah {
		h = ah
	}
	x, y := wa.L+(wa.R-wa.L-w)/2, wa.T+(wa.B-wa.T-h)/2
	r, _, err := pCreateWindowEx.Call(0, up(u16("SharkoxyMain")), up(u16("Sharkoxy")), wsOverlappedWindow|wsClipChildren,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0, 0, hInstance, 0)
	if r == 0 {
		log.Println("shell: CreateWindow:", err)
		openWindow(shellURL, nil)
		return
	}
	mainHwnd = r
	themeTitleBar(r)
	pShowWindow.Call(r, swShow)
	pSetForeground.Call(r)
	pSetTimer.Call(r, 1, 20000, 0) // safety net: fall back if WebView2 hangs
	startWebView()
}

func hostProc(hwnd, msg, wp, lp uintptr) uintptr {
	defer safe("hostProc")
	switch msg {
	case wmShow:
		showMain()
		return 0
	case wmTray:
		switch uint32(lp) {
		case 0x202, 0x203: // left click / double click
			showMain()
		case 0x205: // right click
			trayMenu(hwnd)
		}
		return 0
	case wmEndSession:
		if wp != 0 {
			shellApp.quit() // Windows is shutting down: put the system proxy back
		}
		return 0
	}
	if taskbarMsg != 0 && msg == taskbarMsg { // Explorer restarted
		trayAdd()
		return 0
	}
	r, _, _ := pDefWindowProc.Call(hwnd, msg, wp, lp)
	return r
}

// ---- tray ------------------------------------------------------------------------------

func trayAdd() {
	nid = notifyIcon{}
	nid.Size = uint32(unsafe.Sizeof(nid))
	nid.HWnd, nid.ID, nid.Flags, nid.CallbackMsg, nid.Icon = hostHwnd, 1, 1|2|4, wmTray, hIcon
	copy(nid.Tip[:], syscall.StringToUTF16("Sharkoxy"))
	pShellNotifyIcon.Call(0, uintptr(unsafe.Pointer(&nid))) // NIM_ADD
}

func trayRemove() { pShellNotifyIcon.Call(2, uintptr(unsafe.Pointer(&nid))) }

func trayMenu(hwnd uintptr) {
	a := shellApp
	a.mu.Lock()
	running, busy := a.running, a.busy
	name, ms := "", 0
	for _, n := range a.nodes {
		if n.ID == a.active {
			name, ms = n.Name, a.delays[n.ID]
		}
	}
	a.mu.Unlock()
	status := "Offline"
	if busy == "connecting" || busy == "reconnecting" {
		status = "Connecting..."
	} else if running {
		status = "Online"
		if name != "" {
			status += ": " + name
			if ms > 0 {
				status += fmt.Sprintf(" (%d ms)", ms)
			}
		}
	}
	m, _, _ := pCreatePopupMenu.Call()
	pAppendMenu.Call(m, 0x1, 0, up(u16(status))) // MF_GRAYED
	pAppendMenu.Call(m, 0x800, 0, 0)             // separator
	pAppendMenu.Call(m, 0, 1, up(u16("Open Sharkoxy")))
	if running {
		pAppendMenu.Call(m, 0, 2, up(u16("Disconnect")))
	} else {
		pAppendMenu.Call(m, 0, 2, up(u16("Connect")))
	}
	pAppendMenu.Call(m, 0x800, 0, 0)
	pAppendMenu.Call(m, 0, 3, up(u16("Quit")))
	var pt struct{ X, Y int32 }
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pSetForeground.Call(hwnd)
	cmd, _, _ := pTrackPopupMenu.Call(m, 0x100|0x2, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0) // RETURNCMD|RIGHTBUTTON
	pPostMessage.Call(hwnd, 0, 0, 0)
	pDestroyMenu.Call(m)
	switch cmd {
	case 1:
		showMain()
	case 2:
		go trayToggle(running)
	case 3:
		trayRemove()
		a.quit()
	}
}

func trayToggle(running bool) {
	a := shellApp
	if running {
		a.mu.Lock()
		a.restart = 99
		a.mu.Unlock()
		a.disconnect()
		return
	}
	if a.begin("connecting") != nil {
		return
	}
	defer a.end()
	a.mu.Lock()
	a.restart = 0
	a.mu.Unlock()
	if err := a.connect(); err != nil {
		a.disconnect()
		a.setErr(err)
	}
}

// ---- entry point -----------------------------------------------------------------------

// runShell blocks running the window/tray message loop. It returns false only if the
// shell could not be created at all (the caller then falls back to a browser window).
func runShell(url string, app *App, showNow bool) bool {
	runtime.LockOSThread()
	shellApp, shellURL = app, url
	pSetDpiAwareness.Call(^uintptr(3)) // per-monitor v2 (-4); harmless if unsupported
	if pGetDpiForSystem.Find() == nil {
		if d, _, _ := pGetDpiForSystem.Call(); d >= 96 {
			dpiScale = float64(d) / 96
		}
	}
	pCoInitializeEx.Call(0, 2) // apartment threaded, required by WebView2
	initHandlers()
	hInstance, _, _ = pGetModuleHandle.Call(0)

	icoPath := filepath.Join(dataDir, "app.ico")
	_ = os.WriteFile(icoPath, iconICO, 0o644)
	hIcon, _, _ = pLoadImage.Call(0, up(u16(icoPath)), 1, 0, 0, 0x10|0x40) // IMAGE_ICON, LR_LOADFROMFILE|LR_DEFAULTSIZE
	if hIcon == 0 {
		hIcon, _, _ = pLoadImage.Call(0, 32512, 1, 0, 0, 0x8000) // stock application icon
	}
	cursor, _, _ := pLoadCursor.Call(0, 32512)      // IDC_ARROW
	brush, _, _ := pCreateSolidBrush.Call(0x1F0A0C) // BGR of #0c0a1f, so there is no white flash

	reg := func(name string, proc uintptr, bg uintptr) bool {
		wc := wndClassEx{Instance: hInstance, Icon: hIcon, IconSm: hIcon, Cursor: cursor, Background: bg, WndProc: proc, ClassName: u16(name)}
		wc.Size = uint32(unsafe.Sizeof(wc))
		r, _, err := pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
		if r == 0 {
			log.Println("shell: RegisterClass", name, err)
		}
		return r != 0
	}
	if !reg("SharkoxyMain", syscall.NewCallback(mainProc), brush) || !reg("SharkoxyHost", syscall.NewCallback(hostProc), 0) {
		return false
	}
	h, _, err := pCreateWindowEx.Call(0, up(u16("SharkoxyHost")), up(u16("Sharkoxy")), 0, 0, 0, 0, 0, 0, 0, hInstance, 0)
	if h == 0 {
		log.Println("shell: host window:", err)
		return false
	}
	hostHwnd = h
	taskbarMsg, _, _ = pRegisterWinMsg.Call(up(u16("TaskbarCreated")))
	trayAdd()
	quitHook = trayRemove
	themeHook = func() {
		if mainHwnd != 0 {
			themeTitleBar(mainHwnd)
		}
	}
	showHook = func() bool { pPostMessage.Call(hostHwnd, wmShow, 0, 0); return true }
	if showNow {
		showMain()
	}
	var m winMsg
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	return true
}

// windowsDark reports whether Windows apps are set to dark mode.
func windowsDark() bool {
	c := exec.Command("reg", "query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, "/v", "AppsUseLightTheme")
	hideWindow(c)
	out, err := c.Output()
	return err == nil && strings.Contains(string(out), "0x0")
}

// themeTitleBar paints the caption and border to match the chosen theme (Windows 11 honours colours,
// Windows 10 at least gets the dark/light switch).
func themeTitleBar(hwnd uintptr) {
	if pDwmSetAttr.Find() != nil {
		return
	}
	set := func(attr, val uintptr) { pDwmSetAttr.Call(hwnd, attr, uintptr(unsafe.Pointer(&val)), 4) }
	theme := "apple"
	if shellApp != nil {
		shellApp.mu.Lock()
		theme = shellApp.set.Theme
		shellApp.mu.Unlock()
	}
	if theme == "" {
		theme = "apple" // Clean & Soft is the default look
	}
	if theme == "void" { // Midnight: pure black, always dark
		set(20, 1)
		set(19, 1)
		set(35, 0x000000)
		set(36, 0xDDDDDD)
		set(34, 0x1A1A1A)
		return
	}
	if theme == "apple" {
		if windowsDark() {
			set(20, 1)
			set(19, 1)
			set(35, 0x000000) // black, like the dark Clean theme
			set(36, 0xF7F5F5)
			set(34, 0x1E1E1C)
			return
		}
		set(20, 0)
		set(19, 0)
		set(35, 0xF7F2F2) // #f2f2f7
		set(36, 0x1F1D1D) // #1d1d1f
		set(34, 0xE5E1E1)
		return
	}
	set(20, 1)        // DWMWA_USE_IMMERSIVE_DARK_MODE
	set(19, 1)        // the same switch on early Windows 10 builds
	set(35, 0x1F0A0C) // DWMWA_CAPTION_COLOR   #0c0a1f
	set(36, 0xE8F1FF) // DWMWA_TEXT_COLOR      #fff1e8
	set(34, 0x532B1D) // DWMWA_BORDER_COLOR    #1d2b53
}
