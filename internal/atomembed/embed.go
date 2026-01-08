package atomembed

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

//go:generate go run ./cmd/atom-archive --src ../../atom --dst atom.tar.gz

//go:embed atom.tar.gz
var archiveData []byte

const markerFile = ".valence-atom-version"

var ErrAtomRootExists = errors.New("atom root exists and differs from embedded archive")

func ArchiveAvailable() bool {
	return len(archiveData) > 0
}

func ArchiveHash() string {
	sum := sha256.Sum256(archiveData)
	return hex.EncodeToString(sum[:])
}

func EnsureExtracted(target string, force bool) (bool, error) {
	if strings.TrimSpace(target) == "" {
		return false, errors.New("atom root path is empty")
	}

	info, err := os.Stat(target)
	if err == nil {
		if !info.IsDir() {
			return false, errors.New("atom root exists and is not a directory")
		}

		if markerMatches(target) {
			return false, nil
		}

		if force {
			if err := os.RemoveAll(target); err != nil {
				return false, err
			}
		} else if dirEmpty(target) {
			// ok to proceed
		} else {
			return false, ErrAtomRootExists
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}

	if err := extractArchive(target); err != nil {
		return false, err
	}

	if err := os.WriteFile(filepath.Join(target, markerFile), []byte(ArchiveHash()), 0644); err != nil {
		return true, err
	}

	return true, nil
}

func markerMatches(target string) bool {
	contents, err := os.ReadFile(filepath.Join(target, markerFile))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(contents)) == ArchiveHash()
}

func dirEmpty(target string) bool {
	entries, err := os.ReadDir(target)
	if err != nil {
		return false
	}
	return len(entries) == 0
}

func extractArchive(target string) error {
	if !ArchiveAvailable() {
		return errors.New("embedded atom archive not available")
	}

	if err := os.MkdirAll(target, 0755); err != nil {
		return err
	}

	reader := bytes.NewReader(archiveData)
	gz, err := gzip.NewReader(reader)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}

		if hdr == nil || hdr.Name == "" {
			continue
		}

		cleanName := filepath.Clean(hdr.Name)
		if strings.HasPrefix(cleanName, "..") || filepath.IsAbs(cleanName) {
			return errors.New("archive contains invalid path")
		}

		dstPath := filepath.Join(target, cleanName)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dstPath, hdr.FileInfo().Mode().Perm()); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, dstPath); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, hdr.FileInfo().Mode().Perm())
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		default:
			// skip other file types
		}
	}

	return nil
}
