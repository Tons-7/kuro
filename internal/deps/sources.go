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
	return strings.TrimPrefix(digest, "sha256:")
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

// rqbit tags its betas as full releases, so the prerelease flag cannot be
// trusted; a plain semver tag is the only reliable marker of a stable build.
// Each platform's build is a single binary, so there is no archive to unpack.
func resolveRqbit(ctx context.Context, m *Manager) (Release, error) {
	asset, err := rqbitAsset()
	if err != nil {
		return Release{}, err
	}

	var releases []ghRelease
	if err := m.getJSON(ctx,
		"https://api.github.com/repos/ikatson/rqbit/releases?per_page=40", &releases); err != nil {
		return Release{}, err
	}

	for _, r := range releases {
		if !semverTag.MatchString(r.Tag) {
			continue
		}
		for _, a := range r.Assets {
			if a.Name == asset {
				return Release{
					Version: strings.TrimPrefix(r.Tag, "v"),
					URL:     a.URL,
					Digest:  sha256Of(a.Digest),
				}, nil
			}
		}
	}
	return Release{}, fmt.Errorf("no stable rqbit release carrying %s", asset)
}

// ffmpeg has no single cross-platform source: Windows from gyan.dev, Linux from
// John Van Sickle's static builds, and macOS from a package manager (Homebrew's
// ffmpeg is on PATH), which is how kuro finds it there.
func resolveFfmpeg(ctx context.Context, m *Manager) (Release, error) {
	switch runtime.GOOS {
	case "windows":
		return resolveFfmpegWindows(ctx, m)
	case "linux":
		url, err := ffmpegLinuxURL()
		if err != nil {
			return Release{}, err
		}
		// The static build has no version endpoint; the tarball's own name is
		// enough to tell an install happened.
		return Release{Version: "static", URL: url, Archive: true}, nil
	default:
		return Release{}, manualInstall("ffmpeg")
	}
}

// gyan.dev publishes the current version as a bare string, so nothing has to be
// scraped to find it.
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
	return Release{
		Version: strings.TrimSpace(string(raw)),
		URL:     url,
		Archive: true,
		Digest:  m.publishedSum(ctx, url+".sha256"),
	}, nil
}

// publishedSum reads a checksum a source publishes beside its download. Empty
// when it cannot be read: the install proceeds unverified rather than failing
// because a side file moved.
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

// mpv has clean prebuilt binaries only on Windows; on macOS and Linux it is a
// package-manager install kuro then finds on PATH.
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

func resolveAnime4K(ctx context.Context, m *Manager) (Release, error) {
	var release ghRelease
	if err := m.getJSON(ctx,
		"https://api.github.com/repos/bloc97/Anime4K/releases/latest", &release); err != nil {
		return Release{}, err
	}

	for _, a := range release.Assets {
		if strings.HasSuffix(a.Name, ".zip") {
			return Release{
				Version: strings.TrimPrefix(release.Tag, "v"),
				URL:     a.URL,
				Archive: true,
			}, nil
		}
	}
	return Release{}, fmt.Errorf("no zip in Anime4K release %s", release.Tag)
}
