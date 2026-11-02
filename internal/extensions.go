package internal

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

func writeOwnedExtension(path, content string) error {
	existing, err := os.ReadFile(path)
	if err == nil && !bytes.Contains(existing, []byte(openCodePluginMarker)) {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if string(existing) == content {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
