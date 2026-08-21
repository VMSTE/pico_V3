package media

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var unsafePersistChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// PersistInboundFile writes inbound chat bytes under <workspace>/files/YYYY-MM/.
// Content is deduplicated by SHA-256 and never gets an exec bit (0600).
// Returns the final absolute path.
func PersistInboundFile(workspace, filename, contentType string, data []byte) (string, error) {
	if strings.TrimSpace(workspace) == "" {
		return "", fmt.Errorf("persist: workspace is empty")
	}
	if len(data) == 0 {
		return "", fmt.Errorf("persist: empty payload")
	}
	sum := sha256.Sum256(data)
	short := hex.EncodeToString(sum[:])[:12]
	name := sanitizePersistFilename(filename)
	if name == "" || name == "." {
		name = "file" + persistExtension(contentType)
	}
	dir := filepath.Join(workspace, "files", time.Now().Format("2006-01"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("persist: mkdir: %w", err)
	}
	final := filepath.Join(dir, short+"-"+name)
	if _, err := os.Stat(final); err == nil {
		return final, nil // dedup hit
	}
	tmp, err := os.CreateTemp(dir, ".persist-*")
	if err != nil {
		return "", fmt.Errorf("persist: create temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", fmt.Errorf("persist: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("persist: close: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("persist: chmod: %w", err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("persist: rename: %w", err)
	}
	return final, nil
}

func sanitizePersistFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = unsafePersistChars.ReplaceAllString(name, "_")
	if len(name) > 80 {
		name = name[len(name)-80:]
	}
	return name
}

func persistExtension(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	case "application/pdf":
		return ".pdf"
	case "text/plain":
		return ".txt"
	default:
		return ""
	}
}
