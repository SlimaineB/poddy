package extension

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestDefaultMatchesVSCodePackage(t *testing.T) {
	data, err := os.ReadFile("../../vscode-extension/package.json")
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Publisher string `json:"publisher"`
		Name      string `json:"name"`
		Version   string `json:"version"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}

	meta := Default()
	if want := pkg.Publisher + "." + pkg.Name; meta.ID != want {
		t.Fatalf("extension id = %q, want %q", meta.ID, want)
	}
	if meta.Version != pkg.Version {
		t.Fatalf("extension version = %q, want %q", meta.Version, pkg.Version)
	}
	tag := "v" + pkg.Version
	if !strings.HasSuffix(meta.ReleaseURL, "/"+tag) {
		t.Fatalf("release URL = %q, want tag %q", meta.ReleaseURL, tag)
	}
	if !strings.Contains(meta.DownloadURL, "/"+tag+"/") || !strings.HasSuffix(meta.DownloadURL, "/"+pkg.Name+"-"+pkg.Version+".vsix") {
		t.Fatalf("download URL = %q does not match %s %s", meta.DownloadURL, pkg.Name, pkg.Version)
	}
}
