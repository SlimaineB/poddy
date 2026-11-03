package templates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Watch polls dir and reloads the catalog when template files change. It is
// intended for mounted ConfigMaps: Kubernetes updates the volume in place, so
// the portal does not need a restart when templates change.
func Watch(ctx context.Context, catalog *Catalog, dir string, interval time.Duration) {
	if dir == "" {
		return
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}

	last, err := fingerprint(dir)
	if err != nil {
		log.Printf("templates: watch %s: %v", dir, err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			next, err := fingerprint(dir)
			if err != nil {
				log.Printf("templates: watch %s: %v", dir, err)
				continue
			}
			if next == last {
				continue
			}
			if err := catalog.Reload(dir); err != nil {
				log.Printf("templates: reload %s failed, keeping previous catalog: %v", dir, err)
				continue
			}
			last = next
			log.Printf("templates: reloaded %d templates from %s", len(catalog.List()), dir)
		}
	}
}

func fingerprint(dir string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if name != "template.yaml" && name != "pod.yaml" &&
			!strings.HasSuffix(name, ".template.yaml") && !strings.HasSuffix(name, ".pod.yaml") {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(hash, "%s\x00", rel)
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		fmt.Fprint(hash, "\x00")
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
