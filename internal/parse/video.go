package parse

import "slices"

import "strings"

// videoExt is every container a player opens, shared by the torrent picker, the film picker and the scanner.
var videoExt = []string{".mkv", ".mp4", ".avi", ".ts", ".m2ts", ".webm", ".mov", ".m4v", ".ogm", ".wmv"}

// IsVideo reports a file a player can open.
func IsVideo(name string) bool {
	lower := strings.ToLower(name)
	for _, ext := range videoExt {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// VideoExt reports whether a lower-case extension (".mkv") is a video's.
func VideoExt(ext string) bool {
	return slices.Contains(videoExt, ext)
}

// BaseName is a path's last element without its extension.
func BaseName(path string) string {
	path = strings.ReplaceAll(path, `\`, "/")
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		path = path[i+1:]
	}
	if i := strings.LastIndexByte(path, '.'); i > 0 {
		path = path[:i]
	}
	return path
}
