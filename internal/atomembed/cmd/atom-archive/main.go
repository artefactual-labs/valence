package main

import (
	"archive/tar"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type config struct {
	src string
	dst string
}

func main() {
	cfg := parseFlags()
	if err := buildArchive(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "atom-archive: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	cfg := config{}
	flag.StringVar(&cfg.src, "src", "./atom", "path to atom source directory")
	flag.StringVar(&cfg.dst, "dst", "./internal/atomembed/atom.tar.gz", "path to output tar.gz")
	flag.Parse()
	return cfg
}

func buildArchive(cfg config) error {
	srcAbs, err := filepath.Abs(cfg.src)
	if err != nil {
		return err
	}
	info, err := os.Stat(srcAbs)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("source is not a directory: %s", srcAbs)
	}

	if err := os.MkdirAll(filepath.Dir(cfg.dst), 0755); err != nil {
		return err
	}

	out, err := os.Create(cfg.dst)
	if err != nil {
		return err
	}
	defer out.Close()

	gz := gzip.NewWriter(out)
	defer gz.Close()

	tw := tar.NewWriter(gz)
	defer tw.Close()

	excludes := defaultExcludes()

	walkFn := func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcAbs, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		relSlash := filepath.ToSlash(rel)
		if shouldExclude(relSlash, excludes) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}

		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = relSlash
		hdr.ModTime = time.Unix(0, 0)
		hdr.AccessTime = time.Unix(0, 0)
		hdr.ChangeTime = time.Unix(0, 0)

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			if _, err := io.Copy(tw, file); err != nil {
				return err
			}
		}

		return nil
	}

	return filepath.WalkDir(srcAbs, walkFn)
}

func defaultExcludes() []string {
	return []string{
		".git",
		"cache",
		"log",
		"uploads",
		"web/uploads",
	}
}

func shouldExclude(rel string, excludes []string) bool {
	clean := strings.TrimSuffix(rel, "/")
	for _, exclude := range excludes {
		ex := strings.TrimSuffix(exclude, "/")
		if clean == ex {
			return true
		}
		if strings.HasPrefix(clean, ex+"/") {
			return true
		}
	}
	return false
}
