package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestCmpVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"1.5.0", "1.4.9", 1}, {"1.4.0", "1.4.0", 0}, {"v1.10.0", "1.9.0", 1}, {"1.4", "1.4.1", -1}} {
		if got := cmpVersion(c.a, c.b); got != c.want {
			t.Errorf("cmpVersion(%s,%s)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestVerifyManifest(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	body := []byte(`{"version":"9.9.9","url":"https://x/y.exe","sha256":"` + hex.EncodeToString(make([]byte, 32)) + `","size":10}`)
	sig := hex.EncodeToString(ed25519.Sign(priv, body))
	if _, err := verifyManifest(hex.EncodeToString(pub), body, []byte(sig)); err != nil {
		t.Fatal("good signature rejected:", err)
	}
	if _, err := verifyManifest(hex.EncodeToString(pub), append(body, ' '), []byte(sig)); err == nil {
		t.Fatal("tampered manifest accepted")
	}
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := verifyManifest(hex.EncodeToString(pub2), body, []byte(sig)); err == nil {
		t.Fatal("wrong key accepted")
	}
	if _, err := verifyManifest("", body, []byte(sig)); err == nil {
		t.Fatal("empty key accepted")
	}
	http := []byte(`{"version":"9.9.9","url":"http://x/y.exe","sha256":"` + hex.EncodeToString(make([]byte, 32)) + `","size":10}`)
	if _, err := verifyManifest(hex.EncodeToString(pub), http, []byte(hex.EncodeToString(ed25519.Sign(priv, http)))); err == nil {
		t.Fatal("non-https URL accepted")
	}
}

func TestDownloadVerified(t *testing.T) {
	data := []byte("pretend this is an exe")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer srv.Close()
	sum := sha256.Sum256(data)
	m := manifest{Version: "9", URL: srv.URL, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))}
	dst := filepath.Join(t.TempDir(), "new.exe")
	if err := downloadVerified(srv.Client(), m, dst); err != nil {
		t.Fatal(err)
	}
	if fileHash(dst) != m.SHA256 {
		t.Fatal("hash mismatch after download")
	}
	m.SHA256 = hex.EncodeToString(make([]byte, 32))
	if err := downloadVerified(srv.Client(), m, filepath.Join(t.TempDir(), "bad.exe")); err == nil {
		t.Fatal("wrong hash accepted")
	}
}
