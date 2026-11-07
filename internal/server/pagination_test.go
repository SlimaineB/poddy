package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"poddy/internal/k8s"
)

func TestPaginate(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}
	page, info := paginate(items, 2, 2)
	if info.Total != 5 || info.Pages != 3 || info.Page != 2 || info.PerPage != 2 {
		t.Fatalf("info = %+v", info)
	}
	if len(page) != 2 || page[0] != "c" || page[1] != "d" {
		t.Fatalf("page = %+v", page)
	}
	page, info = paginate(items, 99, 2)
	if info.Page != 3 || len(page) != 1 || page[0] != "e" {
		t.Fatalf("clamped = %+v info=%+v", page, info)
	}
}

func TestWorkspacesPaginationAndSearch(t *testing.T) {
	s := newTestServer("")
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		if rec := request(t, s, http.MethodPost, "/api/workspaces", map[string]any{"name": name, "template": "base"}); rec.Code != http.StatusCreated {
			t.Fatalf("create %s = %d, body = %s", name, rec.Code, rec.Body.String())
		}
	}

	rec := request(t, s, http.MethodGet, "/api/workspaces?page=2&per_page=2", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("page status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var page pageResult[k8s.Workspace]
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || page.Page != 2 || page.Pages != 2 || len(page.Items) != 1 || page.Items[0].Name != "charlie" {
		t.Fatalf("page = %+v", page)
	}

	rec = request(t, s, http.MethodGet, "/api/workspaces?q=brav", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Name != "bravo" {
		t.Fatalf("search page = %+v", page)
	}
}
