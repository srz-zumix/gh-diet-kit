package assets

import (
	"encoding/json"
	"fmt"
	"os"
)

// LoadMetadata reads a DumpMetadata JSON file from disk.
func LoadMetadata(path string) (*DumpMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read metadata file %q: %w", path, err)
	}
	var meta DumpMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse metadata file %q: %w", path, err)
	}
	return &meta, nil
}

// WriteMetadata writes meta to the given path as a JSON file.
func WriteMetadata(path string, meta DumpMetadata) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write metadata file %q: %w", path, err)
	}
	return nil
}
