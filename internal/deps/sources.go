package deps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const userAgent = "kuro"

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	// "sha256:<hex>" where GitHub has computed one.
	Digest string `json:"digest"`
}

// sha256Of turns GitHub's prefixed digest into the bare hex the installer
// compares against, and ignores any other algorithm it might report.
func sha256Of(digest string) string {
	if h, ok := strings.CutPrefix(digest, "sha256:"); ok {
		return h
	}
	return ""
}

type ghRelease struct {
	Tag    string    `json:"tag_name"`
	Assets []ghAsset `json:"assets"`
}

func (m *Manager) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.github+json")

	res, err := m.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", url, res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// ffmpeg comes from gyan.dev on Windows and static builds on Linux; macOS uses Homebrew's on PATH.
func resolveFfmpeg(ctx context.Context, m *Manager) (Release, error) {
	switch runtime.GOOS {
	case "windows":
		return resolveFfmpegWindows(ctx, m)
	case "linux":
		url, err := ffmpegLinuxURL()
		if err != nil {
			return Release{}, err
		}
		// No version endpoint; the tarball's name is enough to tell an install happened.
		return Release{Version: "static", URL: url, Archive: true}, nil
	default:
		return Release{}, manualInstall("ffmpeg")
	}
}

// gyan.dev publishes the current version as a bare string, so nothing has to be scraped to find it.
func resolveFfmpegWindows(ctx context.Context, m *Manager) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://www.gyan.dev/ffmpeg/builds/release-version", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("User-Agent", userAgent)

	res, err := m.http.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("ffmpeg version: HTTP %d", res.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(res.Body, 128))
	if err != nil {
		return Release{}, err
	}
	const url = "https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-full.7z"
	sum := m.publishedSum(ctx, url+".sha256")
	if sum == "" {
		// Once more after a moment: the sidecar is a tiny file a flaky line loses.
		time.Sleep(2 * time.Second)
		sum = m.publishedSum(ctx, url+".sha256")
	}
	if sum == "" {
		m.log.Warn("ffmpeg checksum unavailable; installing unverified")
	}
	return Release{
		Version: strings.TrimSpace(string(raw)),
		URL:     url,
		Archive: true,
		Digest:  sum,
	}, nil
}

// publishedSum reads a source's published checksum; "" when unreadable, and the install goes on unverified.
func (m *Manager) publishedSum(ctx context.Context, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", userAgent)

	res, err := m.http.Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return ""
	}

	raw, err := io.ReadAll(io.LimitReader(res.Body, 256))
	if err != nil {
		return ""
	}
	// Either a bare hash or "<hash>  <filename>".
	sum, _, _ := strings.Cut(strings.TrimSpace(string(raw)), " ")
	if len(sum) != 64 {
		return ""
	}
	return sum
}

// The plain x86_64 build runs anywhere; -v3 needs AVX2 and -dev is headers.
var mpvAsset = regexp.MustCompile(`^mpv-x86_64-\d{8}-git-[0-9a-f]+\.7z$`)

// mpv has prebuilt binaries only on Windows; elsewhere the package manager's is found on PATH.
func resolveMpv(ctx context.Context, m *Manager) (Release, error) {
	if runtime.GOOS != "windows" {
		return Release{}, manualInstall("mpv")
	}

	var release ghRelease
	if err := m.getJSON(ctx,
		"https://api.github.com/repos/shinchiro/mpv-winbuild-cmake/releases/latest", &release); err != nil {
		return Release{}, err
	}

	for _, a := range release.Assets {
		if mpvAsset.MatchString(a.Name) {
			version := strings.TrimSuffix(strings.TrimPrefix(a.Name, "mpv-x86_64-"), ".7z")
			return Release{
				Version: version, URL: a.URL, Archive: true, Digest: sha256Of(a.Digest),
			}, nil
		}
	}
	return Release{}, fmt.Errorf("no plain x86_64 mpv build in %s", release.Tag)
}

// anime4kSums pins releases older than GitHub's asset digests; v4.0.1 has not changed since 2021.
var anime4kSums = map[string]string{"v4.0.1": "139cd282086457c5adc79caf7b75b8b825091d71c9b54958c18745fea62d7ed7"}

func resolveAnime4K(ctx context.Context, m *Manager) (Release, error) {
	var release ghRelease
	if err := m.getJSON(ctx,
		"https://api.github.com/repos/bloc97/Anime4K/releases/latest", &release); err != nil {
		return Release{}, err
	}

	for _, a := range release.Assets {
		if strings.HasSuffix(a.Name, ".zip") {
			digest := sha256Of(a.Digest)
			if digest == "" {
				digest = anime4kSums[release.Tag]
			}
			return Release{
				Version: strings.TrimPrefix(release.Tag, "v"),
				URL:     a.URL,
				Archive: true,
				Digest:  digest,
			}, nil
		}
	}
	return Release{}, fmt.Errorf("no zip in Anime4K release %s", release.Tag)
}
