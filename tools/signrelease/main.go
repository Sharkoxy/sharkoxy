// signrelease creates the signing key for Sharkoxy updates and signs each release's update.json.
//
//	go run ./tools/signrelease keygen -out update.key        (prints the public key to paste into update.go)
//	go run ./tools/signrelease sign -key update.key -exe Sharkoxy.exe -version 1.5.0 -url https://…/Sharkoxy.exe -notes "…" -out dist
//
// The private key may also be passed through the UPDATE_SIGNING_KEY environment variable (a CI secret).
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func die(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: signrelease keygen|sign [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "keygen":
		fs := flag.NewFlagSet("keygen", flag.ExitOnError)
		out := fs.String("out", "update.key", "where to write the private key")
		fs.Parse(os.Args[2:])
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			die(err)
		}
		if err := os.WriteFile(*out, []byte(hex.EncodeToString(priv)+"\n"), 0o600); err != nil {
			die(err)
		}
		fmt.Println("private key written to", *out, "- keep it SECRET and backed up (GitHub secret UPDATE_SIGNING_KEY)")
		fmt.Println("public key (put in update.go, updatePubHex):")
		fmt.Println(hex.EncodeToString(pub))
	case "sign":
		fs := flag.NewFlagSet("sign", flag.ExitOnError)
		key := fs.String("key", "", "private key file (or env UPDATE_SIGNING_KEY)")
		exe := fs.String("exe", "Sharkoxy.exe", "the release exe")
		ver := fs.String("version", "", "version, e.g. 1.5.0")
		url := fs.String("url", "", "https URL where the exe will be downloaded")
		notes := fs.String("notes", "", "short release notes")
		out := fs.String("out", ".", "output directory")
		fs.Parse(os.Args[2:])
		hexKey := strings.TrimSpace(os.Getenv("UPDATE_SIGNING_KEY"))
		if *key != "" {
			b, err := os.ReadFile(*key)
			if err != nil {
				die(err)
			}
			hexKey = strings.TrimSpace(string(b))
		}
		raw, err := hex.DecodeString(hexKey)
		if err != nil || len(raw) != ed25519.PrivateKeySize {
			die(fmt.Errorf("missing or bad private key"))
		}
		if *ver == "" || !strings.HasPrefix(*url, "https://") {
			die(fmt.Errorf("-version and an https -url are required"))
		}
		f, err := os.Open(*exe)
		if err != nil {
			die(err)
		}
		h := sha256.New()
		n, err := io.Copy(h, f)
		f.Close()
		if err != nil {
			die(err)
		}
		body, _ := json.MarshalIndent(map[string]any{
			"version": *ver, "url": *url, "sha256": hex.EncodeToString(h.Sum(nil)), "size": n, "notes": *notes,
		}, "", "  ")
		sig := ed25519.Sign(ed25519.PrivateKey(raw), body)
		_ = os.MkdirAll(*out, 0o755)
		if err := os.WriteFile(filepath.Join(*out, "update.json"), body, 0o644); err != nil {
			die(err)
		}
		if err := os.WriteFile(filepath.Join(*out, "update.json.sig"), []byte(hex.EncodeToString(sig)+"\n"), 0o644); err != nil {
			die(err)
		}
		fmt.Println("wrote update.json and update.json.sig in", *out)
	default:
		die(fmt.Errorf("unknown command %q", os.Args[1]))
	}
}
