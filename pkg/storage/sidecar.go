package storage

import (
	"encoding/json"
	"os"
)

type sidecarData struct {
	ContentType string            `json:"content_type,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

func sidecarPath(objectPath string) string {
	return objectPath + ".meta"
}

func writeSidecar(objectPath string, opts PutOptions) error {
	data := sidecarData{
		ContentType: contentTypeOrDefault(opts.ContentType),
		Metadata:    cloneMetadata(opts.Metadata),
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return os.WriteFile(sidecarPath(objectPath), b, 0o640)
}

func readSidecar(objectPath string) (contentType string, metadata map[string]string) {
	b, err := os.ReadFile(sidecarPath(objectPath))
	if err != nil {
		return "", nil
	}
	var data sidecarData
	if json.Unmarshal(b, &data) != nil {
		return "", nil
	}
	return data.ContentType, cloneMetadata(data.Metadata)
}
