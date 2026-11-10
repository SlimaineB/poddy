package extension

import (
	_ "embed"
	"encoding/json"
)

type Metadata struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	ReleaseURL  string `json:"releaseUrl"`
	DownloadURL string `json:"downloadUrl"`
}

//go:embed extension.json
var metadataJSON []byte

func Default() Metadata {
	var meta Metadata
	if err := json.Unmarshal(metadataJSON, &meta); err != nil {
		return Metadata{}
	}
	return meta
}
