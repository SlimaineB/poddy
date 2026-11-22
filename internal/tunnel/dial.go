package tunnel

import (
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// DialHandler serves the cluster-internal peer dial endpoint. It is used when
// the portal has no public SSH bastion but still needs to forward region or
// workspace streams to the replica that owns the tunnel.
type DialHandler struct {
	Key          []byte
	Broker       *Broker
	RegionTunnel *RegionBroker
}

func (h DialHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ServeDial(w, r, h.Key, h.Broker, h.RegionTunnel)
}

func ServeDial(w http.ResponseWriter, r *http.Request, key []byte, broker *Broker, regions *RegionBroker) {
	kind := strings.TrimSpace(r.Header.Get("X-Poddy-Dial-Kind"))
	encodedName := strings.TrimSpace(r.Header.Get("X-Poddy-Dial-Name"))
	target := strings.TrimSpace(r.Header.Get("X-Poddy-Dial-Target"))
	timestamp := strings.TrimSpace(r.Header.Get("X-Poddy-Dial-Time"))
	signature := strings.TrimSpace(r.Header.Get("X-Poddy-Dial-Auth"))
	nameBytes, err := base64.RawURLEncoding.DecodeString(encodedName)
	if err != nil {
		http.Error(w, "invalid name", http.StatusBadRequest)
		return
	}
	name := string(nameBytes)
	if !VerifyDial(key, timestamp, kind, name, target, signature) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var upstream net.Conn
	switch kind {
	case KindWorkspace:
		if broker == nil {
			http.Error(w, "workspace tunnel is not configured", http.StatusNotFound)
			return
		}
		upstream, err = broker.Dial(ctx, name, target)
	case KindRegion:
		if regions == nil {
			http.Error(w, "region tunnel is not configured", http.StatusNotFound)
			return
		}
		upstream, err = regions.Dial(ctx, name, target)
	default:
		http.Error(w, "invalid kind", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "cannot reach target", http.StatusBadGateway)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "streaming is not supported", http.StatusInternalServerError)
		return
	}
	conn, buffered, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	defer conn.Close()
	defer upstream.Close()
	_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
	if err := buffered.Flush(); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, conn)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(conn, upstream)
		done <- struct{}{}
	}()
	<-done
}
