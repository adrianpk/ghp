package ghp

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func loadPrompt(fsys fs.FS, diskPath, embedPath string) (string, string, error) {
	if diskPath != "" {
		if b, err := os.ReadFile(filepath.Clean(diskPath)); err == nil {
			return string(b), fmt.Sprintf("file:%s", diskPath), nil
		}
	}

	if fsys != nil {
		b, err := fs.ReadFile(fsys, embedPath)
		if err != nil {
			return "", "", err
		}
		return string(b), fmt.Sprintf("embed:%s", embedPath), nil
	}

	return "", "", fmt.Errorf("no prompt source available")
}
