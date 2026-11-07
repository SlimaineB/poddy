package userkeys

import (
	"context"
	"log"
	"time"

	"poddy/internal/sshkeys"
)

// Watch polls the registry and updates the keyring when it changes. Polling a
// single small ConfigMap is deliberately simple and keeps the bastion free of
// a second controller.
func Watch(ctx context.Context, store *Store, keyring *sshkeys.Dynamic, adminLines []string, allowUnmapped bool) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	last := ""
	for {
		keys, err := store.List(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("ssh user keys: reload: %v", err)
			}
		} else {
			fingerprint := fingerprintOf(keys)
			if fingerprint != last {
				if err := keyring.Update(adminLines, Owned(keys), allowUnmapped); err != nil {
					log.Printf("ssh user keys: update keyring: %v", err)
				} else {
					last = fingerprint
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func fingerprintOf(keys []Key) string {
	raw, err := encode(keys)
	if err != nil {
		return ""
	}
	return raw
}
