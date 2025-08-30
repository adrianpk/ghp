package ghp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

func getCachePath(elem ...string) (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	fullPath := append([]string{cacheDir, "ghp"}, elem...)

	return filepath.Join(fullPath...), nil
}

func writeCache(path string, data any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}

	return os.WriteFile(path, b, 0644)
}

func readCache(path string, target any, ttl time.Duration) (bool, error) {
	stat, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil // Cache miss, not an error
	}
	if err != nil {
		return false, err // Real error
	}

	if time.Since(stat.ModTime()) > ttl {
		return false, nil // Cache expired
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}

	if err := json.Unmarshal(b, target); err != nil {
		return false, err
	}

	return true, nil // Cache hit
}
