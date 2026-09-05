// Package transcode serves video to a browser. No browser can play Matroska,
// which is what anime ships as, so the container is always rewritten — usually
// without touching the streams themselves.
package transcode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Stream struct {
	Index    int    `json:"index"`
	Kind     string `json:"kind"`
	Codec    string `json:"codec"`
	Profile  string `json:"profile"`
	BitDepth int    `json:"bitDepth"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Channels int    `json:"channels"`
	Language string `json:"language"`
	Title    string `json:"title"`
	Default  bool   `json:"default"`
	Forced   bool   `json:"forced"`
}

type Attachment struct {
	Index    int    `json:"index"`
	Filename string `json:"filename"`
	MimeType string `json:"mimeType"`
}

// Chapter is a marker the release itself carries. Where one exists it beats a
// crowd-sourced timestamp: it was cut for this exact file.
type Chapter struct {
	Title string  `json:"title"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	// "op", "ed" or empty when the title names no part of the episode.
	Kind string `json:"kind"`
}

// Release groups label the same two things a dozen ways: "Intro"/"Opening"/"OP"
// and "Credits"/"Ending"/"Outro", often numbered. Anything else is body.
func chapterKind(title string) string {
	t := strings.ToLower(strings.TrimSpace(title))
	// The first word, minus any number: "op", "OP1", "ED 2", "OP - Kick Back".
	var head string
	if fields := strings.FieldsFunc(t, func(r rune) bool {
		return r == ' ' || r == '-' || r == '_'
	}); len(fields) > 0 {
		head = strings.TrimRight(fields[0], "0123456789")
	}

	switch {
	case head == "op", strings.Contains(t, "opening"), strings.Contains(t, "intro"):
		return "op"
	case head == "ed", strings.Contains(t, "ending"),
		strings.Contains(t, "credits"), strings.Contains(t, "outro"):
		return "ed"
	}
	return ""
}

type MediaInfo struct {
	Duration    float64      `json:"duration"`
	Container   string       `json:"container"`
	Bitrate     int64        `json:"bitrate"`
	Video       *Stream      `json:"video"`
	Audio       []Stream     `json:"audio"`
	Subtitles   []Stream     `json:"subtitles"`
	Attachments []Attachment `json:"attachments"`
	Chapters    []Chapter    `json:"chapters"`
}

type ffprobeOutput struct {
	Format struct {
		Duration   string `json:"duration"`
		BitRate    string `json:"bit_rate"`
		FormatName string `json:"format_name"`
	} `json:"format"`
	Streams []struct {
		Index       int    `json:"index"`
		CodecName   string `json:"codec_name"`
		CodecType   string `json:"codec_type"`
		Profile     string `json:"profile"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		Channels    int    `json:"channels"`
		PixFmt      string `json:"pix_fmt"`
		BitsPerRaw  string `json:"bits_per_raw_sample"`
		Disposition struct {
			Default  int `json:"default"`
			Forced   int `json:"forced"`
			Attached int `json:"attached_pic"`
		} `json:"disposition"`
		Tags map[string]string `json:"tags"`
	} `json:"streams"`
	Chapters []struct {
		StartTime string            `json:"start_time"`
		EndTime   string            `json:"end_time"`
		Tags      map[string]string `json:"tags"`
	} `json:"chapters"`
}

// ffprobe repeats itself once per stream it could not read; the first line is
// the cause and the rest is noise in a log.
func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

type Prober struct {
	ffprobe string
}

func NewProber(ffprobe string) *Prober { return &Prober{ffprobe: ffprobe} }

// Probe inspects the source. It reads over HTTP directly from the torrent
// engine, so only the header is fetched rather than the whole file.
func (p *Prober) Probe(ctx context.Context, url string) (*MediaInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// -v error rather than quiet: diagnostics go to stderr so stdout JSON stays
	// clean, and a failure can still say why instead of a bare "exit status 1".
	cmd := exec.CommandContext(ctx, p.ffprobe,
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-show_chapters",
		"-analyzeduration", "20M",
		"-probesize", "20M",
		url,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		if reason := strings.TrimSpace(stderr.String()); reason != "" {
			return nil, fmt.Errorf("ffprobe: %w: %s", err, firstLine(reason))
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("ffprobe: %w: gave up after 60s reading the source", err)
		}
		return nil, fmt.Errorf("ffprobe: %w", err)
	}

	var raw ffprobeOutput
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("ffprobe output: %w", err)
	}

	info := &MediaInfo{Container: raw.Format.FormatName}
	info.Duration, _ = strconv.ParseFloat(raw.Format.Duration, 64)
	info.Bitrate, _ = strconv.ParseInt(raw.Format.BitRate, 10, 64)

	for _, s := range raw.Streams {
		stream := Stream{
			Index:    s.Index,
			Kind:     s.CodecType,
			Codec:    s.CodecName,
			Profile:  s.Profile,
			Width:    s.Width,
			Height:   s.Height,
			Channels: s.Channels,
			Language: s.Tags["language"],
			Title:    s.Tags["title"],
			Default:  s.Disposition.Default == 1,
			Forced:   s.Disposition.Forced == 1,
			BitDepth: bitDepth(s.PixFmt, s.BitsPerRaw),
		}

		switch s.CodecType {
		case "video":
			// Cover art is stored as a video stream; it is not the picture.
			if s.Disposition.Attached == 1 {
				continue
			}
			if info.Video == nil {
				info.Video = &stream
			}
		case "audio":
			info.Audio = append(info.Audio, stream)
		case "subtitle":
			info.Subtitles = append(info.Subtitles, stream)
		case "attachment":
			info.Attachments = append(info.Attachments, Attachment{
				Index:    s.Index,
				Filename: s.Tags["filename"],
				MimeType: s.Tags["mimetype"],
			})
		}
	}

	for _, c := range raw.Chapters {
		start, _ := strconv.ParseFloat(c.StartTime, 64)
		end, _ := strconv.ParseFloat(c.EndTime, 64)
		if end <= start {
			continue
		}
		title := c.Tags["title"]
		kind := chapterKind(title)
		// A theme runs a minute or two; anything else is a mislabelled body.
		if span := end - start; span < 15 || span > 240 {
			kind = ""
		}
		info.Chapters = append(info.Chapters, Chapter{
			Title: title, Start: start, End: end, Kind: kind,
		})
	}

	if info.Video == nil {
		return nil, fmt.Errorf("no video stream in %s", url)
	}
	return info, nil
}

func bitDepth(pixFmt, bitsPerRaw string) int {
	if n, err := strconv.Atoi(bitsPerRaw); err == nil && n > 0 {
		return n
	}
	switch {
	case strings.Contains(pixFmt, "p10"):
		return 10
	case strings.Contains(pixFmt, "p12"):
		return 12
	case pixFmt != "":
		return 8
	}
	return 0
}

// Plan decides what has to be re-encoded. Re-encoding costs GPU time and
// quality, so it is only done when the browser cannot play the stream.
type Plan struct {
	VideoCopy  bool   `json:"videoCopy"`
	AudioCopy  bool   `json:"audioCopy"`
	VideoCodec string `json:"videoCodec"`
	AudioCodec string `json:"audioCodec"`
	Reason     string `json:"reason"`
}

// Chromium decodes H.264 and AAC everywhere; HEVC needs a platform extension
// and 10-bit H.264 has no hardware decoder, so neither can be relied on.
func PlanFor(info *MediaInfo, encoder string) Plan {
	return planFor(info, encoder, 0)
}

// planFor judges the audio track that will actually be sent.
func planFor(info *MediaInfo, encoder string, track int) Plan {
	plan := Plan{VideoCopy: true, AudioCopy: true}
	var reasons []string

	switch v := info.Video; {
	case v == nil:
	case v.Codec == "h264" && v.BitDepth <= 8:
	case v.Codec == "vp9", v.Codec == "av1":
		// Chromium decodes both natively.
	default:
		plan.VideoCopy = false
		plan.VideoCodec = orDefault(encoder, "libx264")
		reasons = append(reasons, fmt.Sprintf("video %s %d-bit", v.Codec, v.BitDepth))
	}

	if track < 0 || track >= len(info.Audio) {
		track = 0
	}
	if len(info.Audio) > 0 {
		switch a := info.Audio[track]; a.Codec {
		case "aac", "mp3", "opus", "flac", "vorbis":
		default:
			plan.AudioCopy = false
			plan.AudioCodec = "aac"
			reasons = append(reasons, "audio "+a.Codec)
		}
	}

	if len(reasons) > 0 {
		plan.Reason = "re-encoding " + strings.Join(reasons, " and ")
	} else {
		plan.Reason = "remux only"
	}
	return plan
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
