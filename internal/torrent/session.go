package torrent

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// legacySessionDir is where rqbit keeps its session when not told otherwise.
func legacySessionDir() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "rqbit", "session", "data")
}

// SeedSession copies, once, the default session's torrents downloading into cacheDir.
func SeedSession(dst, legacy, cacheDir string) (int, error) {
	if _, err := os.Stat(filepath.Join(dst, "session.json")); err == nil {
		return 0, nil
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return 0, err
	}
	if legacy == "" {
		return 0, nil
	}
	raw, err := os.ReadFile(filepath.Join(legacy, "session.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return 0, err
	}
	var torrents map[string]json.RawMessage
	if err := json.Unmarshal(doc["torrents"], &torrents); err != nil {
		return 0, err
	}

	kept := map[string]json.RawMessage{}
	for id, entry := range torrents {
		var t struct {
			InfoHash     string `json:"info_hash"`
			OutputFolder string `json:"output_folder"`
		}
		if json.Unmarshal(entry, &t) != nil || !Within(cacheDir, t.OutputFolder) {
			continue
		}
		name := strings.ToLower(t.InfoHash) + ".torrent"
		if body, err := os.ReadFile(filepath.Join(legacy, name)); err == nil {
			if err := os.WriteFile(filepath.Join(dst, name), body, 0o644); err != nil {
				return 0, err
			}
		}
		kept[id] = entry
	}

	doc["torrents"], err = json.Marshal(kept)
	if err != nil {
		return 0, err
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return 0, err
	}
	return len(kept), os.WriteFile(filepath.Join(dst, "session.json"), out, 0o644)
}

// Within reports whether path is dir or inside it.
func Within(dir, path string) bool {
	if dir == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
