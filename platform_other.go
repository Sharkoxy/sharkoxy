//go:build !windows

package main

import (
	"errors"
	"log"
	"os/exec"
)

func hideWindow(c *exec.Cmd) {}

func setSystemProxy(on bool, addr string) error { return nil }

func openWindow(url string, onClose func()) {
	log.Println("open this address in a browser:", url)
}

func setAutostart(on bool) error { return nil }

func openFolder(dir string) {}

func runShell(url string, app *App, showNow bool) bool { return false }

func sysProxyIs(addr string) bool { return true }

func swapExe(newPath string) error { return errors.New("self-update is Windows only for now") }
func restartSelf(show bool) bool   { return false }

func registerProtocols(all bool) {}
func readClipboard() string      { return "" }
