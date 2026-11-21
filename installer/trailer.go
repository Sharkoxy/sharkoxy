package main

import (
	"archive/zip"
	"debug/pe"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The "full" installer is Sharkoxy-Setup.exe with the official sing-box release zip appended to it
// (copy /b Sharkoxy-Setup.exe + sing-box-...-windows-amd64.zip Sharkoxy-Setup-Full.exe).
// A zip can be read from the end of a file, so the exe still runs and still finds its zip.

// peEnd returns where the executable itself ends, i.e. the start of any appended data.
func peEnd(path string) (int64, error) {
	f, err := pe.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var end int64
	for _, s := range f.Sections {
		if e := int64(s.Offset) + int64(s.Size); e > end {
			end = e
		}
	}
	return end, nil
}

// extractTrailer unpacks every file of an appended zip (flattened) into dir.
// It reports whether sing-box.exe was among them. No appended zip is not an error.
func extractTrailer(self, dir string) (bool, error) {
	f, err := os.Open(self)
	if err != nil {
		return false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false, err
	}
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		if errors.Is(err, zip.ErrFormat) {
			return false, nil
		}
		return false, err
	}
	found := false
	for _, e := range zr.File {
		if e.FileInfo().IsDir() {
			continue
		}
		base := filepath.Base(e.Name)
		if base == "." || base == ".." || strings.ContainsAny(base, `/\:`) {
			continue
		}
		rc, err := e.Open()
		if err != nil {
			return found, err
		}
		out, err := os.OpenFile(filepath.Join(dir, base), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			rc.Close()
			return found, err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return found, err
		}
		if strings.EqualFold(base, "sing-box.exe") {
			found = true
		}
	}
	return found, nil
}

// copyExeOnly copies the executable without any appended zip (used for Uninstall.exe).
func copyExeOnly(src, dst string) error {
	end, err := peEnd(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, io.NewSectionReader(in, 0, end))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}
