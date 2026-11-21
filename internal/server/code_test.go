package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestInjectIDEReload(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader("<html><head></head><body></body></html>")),
	}
	if err := injectIDEReload(resp); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), ideReloadScript) {
		t.Fatalf("reload script missing from %q", body)
	}
	if resp.ContentLength != int64(len(body)) {
		t.Fatalf("content length = %d, want %d", resp.ContentLength, len(body))
	}
}

func TestInjectIDEReloadSkipsNonHTML(t *testing.T) {
	const original = `{"ok":true}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(original)),
	}
	if err := injectIDEReload(resp); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != original {
		t.Fatalf("body = %q, want %q", body, original)
	}
}

func TestInjectIDEReloadSkipsCompressed(t *testing.T) {
	const original = `<html><head></head></html>`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":     []string{"text/html"},
			"Content-Encoding": []string{"gzip"},
		},
		Body: io.NopCloser(strings.NewReader(original)),
	}
	if err := injectIDEReload(resp); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != original {
		t.Fatalf("body = %q, want %q", body, original)
	}
}
