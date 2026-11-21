package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestTrailer(t *testing.T) {
	dir := t.TempDir()
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	for name, body := range map[string]string{"sing-box-1.14.4-windows-amd64/sing-box.exe": "CORE", "sing-box-1.14.4-windows-amd64/libcronet.dll": "DLL", "../evil.txt": "x"} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	exe := filepath.Join(dir, "setup.exe")
	os.WriteFile(exe, append(bytes.Repeat([]byte("MZ-fake-exe-"), 5000), zb.Bytes()...), 0o755)
	out := filepath.Join(dir, "out")
	os.Mkdir(out, 0o755)
	ok, err := extractTrailer(exe, out)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "sing-box.exe")); string(b) != "CORE" {
		t.Fatal("core content", string(b))
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.txt")); err == nil {
		t.Fatal("escaped")
	}
	// no trailer: not an error
	plain := filepath.Join(dir, "plain.exe")
	os.WriteFile(plain, []byte("just bytes, no zip"), 0o755)
	if ok, err := extractTrailer(plain, out); ok || err != nil {
		t.Fatal(ok, err)
	}
}
