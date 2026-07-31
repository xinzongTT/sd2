package media

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var uuidRe = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Resolve turns a canvas/client media ref into a CLI-acceptable path or UUID.
// Accepts: local path, UUID, http(s) URL, data URL (base64).
func Resolve(dataDir, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	if uuidRe.MatchString(ref) {
		return ref, nil
	}
	if strings.HasPrefix(ref, "data:") {
		return saveDataURL(dataDir, ref)
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return downloadURL(dataDir, ref)
	}
	// local path
	if _, err := os.Stat(ref); err == nil {
		abs, err := filepath.Abs(ref)
		if err != nil {
			return ref, nil
		}
		return abs, nil
	}
	// maybe relative to dataDir/uploads
	cand := filepath.Join(dataDir, "uploads", ref)
	if _, err := os.Stat(cand); err == nil {
		return cand, nil
	}
	return "", fmt.Errorf("media not found: %s", truncate(ref, 80))
}

func ResolveMany(dataDir string, refs []string) ([]string, error) {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		v, err := Resolve(dataDir, r)
		if err != nil {
			return nil, err
		}
		if v != "" {
			out = append(out, v)
		}
	}
	return out, nil
}

func saveDataURL(dataDir, dataURL string) (string, error) {
	// data:[<mediatype>][;base64],<data>
	comma := strings.Index(dataURL, ",")
	if comma < 0 {
		return "", fmt.Errorf("invalid data url")
	}
	meta := dataURL[5:comma]
	payload := dataURL[comma+1:]
	if !strings.Contains(meta, ";base64") {
		return "", fmt.Errorf("only base64 data urls supported")
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		// try raw std without padding issues
		raw, err = base64.RawStdEncoding.DecodeString(payload)
		if err != nil {
			return "", fmt.Errorf("decode data url: %w", err)
		}
	}
	ext := extFromMIME(meta)
	return writeUpload(dataDir, ext, raw)
}

func downloadURL(dataDir, url string) (string, error) {
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("download media: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("download media http %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 200<<20)) // 200MB
	if err != nil {
		return "", err
	}
	ext := extFromContentType(resp.Header.Get("Content-Type"))
	if ext == ".bin" {
		ext = extFromURL(url)
	}
	return writeUpload(dataDir, ext, raw)
}

func writeUpload(dataDir, ext string, raw []byte) (string, error) {
	dir := filepath.Join(dataDir, "uploads")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := fmt.Sprintf("up_%d%s", time.Now().UnixNano(), ext)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// SaveUpload writes multipart file bytes into uploads dir.
func SaveUpload(dataDir, filename string, raw []byte) (string, error) {
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".bin"
	}
	return writeUpload(dataDir, strings.ToLower(ext), raw)
}

func extFromMIME(meta string) string {
	meta = strings.ToLower(meta)
	switch {
	case strings.Contains(meta, "image/png"):
		return ".png"
	case strings.Contains(meta, "image/jpeg"), strings.Contains(meta, "image/jpg"):
		return ".jpg"
	case strings.Contains(meta, "image/webp"):
		return ".webp"
	case strings.Contains(meta, "image/gif"):
		return ".gif"
	case strings.Contains(meta, "video/mp4"):
		return ".mp4"
	case strings.Contains(meta, "video/webm"):
		return ".webm"
	case strings.Contains(meta, "audio/mpeg"), strings.Contains(meta, "audio/mp3"):
		return ".mp3"
	case strings.Contains(meta, "audio/wav"), strings.Contains(meta, "audio/x-wav"):
		return ".wav"
	case strings.Contains(meta, "audio/ogg"):
		return ".ogg"
	default:
		return ".bin"
	}
}

func extFromContentType(ct string) string {
	return extFromMIME(ct)
}

func extFromURL(u string) string {
	u = strings.Split(u, "?")[0]
	ext := strings.ToLower(filepath.Ext(u))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".mp4", ".webm", ".mov", ".mp3", ".wav", ".ogg", ".m4a":
		if ext == ".jpeg" {
			return ".jpg"
		}
		return ext
	default:
		return ".bin"
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
