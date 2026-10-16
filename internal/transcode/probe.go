// Package transcode serves video to a browser, rewriting Matroska into HLS, usually without re-encoding.
package transcode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"kuro/internal/sound"
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

// Chapter is the release's own marker, which beats AniSkip as it fits this exact file.
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

// ffprobe repeats itself per unreadable stream; the first line is the cause.
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

// A slow swarm can take minutes over the header. A var for tests.
var probeLimit = 3 * time.Minute

// ErrProbeTimeout: the start of the file had not arrived when the probe gave up.
var ErrProbeTimeout = errors.New("the start of the file didn't arrive")

// Probe inspects the source over HTTP, so the engine fetches only the header.
func (p *Prober) Probe(ctx context.Context, url string) (*MediaInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, probeLimit)
	defer cancel()

	// -v error rather than quiet: diagnostics go to stderr so stdout JSON stays
	// clean, and a failure can still say why instead of a bare "exit status 1".
	cmd := exec.CommandContext(ctx, p.ffprobe,
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-show_chapters",
		// Everything read below is in the headers; twenty seconds' worth meant
		// tens of megabytes, a minute through the engine on a fresh swarm.
		"-analyzeduration", "2M",
		"-probesize", "8M",
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
			return nil, fmt.Errorf("ffprobe: %w within %s", ErrProbeTimeout, probeLimit)
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
	info.Audio = dropMirror(info.Audio)
	info.Subtitles = dropMirror(info.Subtitles)
	return info, nil
}

// dropMirror removes the phantom second copy of the tracks ffprobe reports mid-download.
func dropMirror(tracks []Stream) []Stream {
	n := len(tracks)
	if n < 2 || n%2 != 0 {
		return tracks
	}
	half := n / 2
	for i := range half {
		a, b := tracks[i], tracks[half+i]
		if b.Index <= a.Index || a.Kind != b.Kind || a.Codec != b.Codec || a.Language != b.Language ||
			a.Title != b.Title || a.Channels != b.Channels || a.Default != b.Default || a.Forced != b.Forced {
			return tracks
		}
	}
	return tracks[:half]
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
	// Sound is what is done to the audio on the way out; anything at all forces an audio re-encode.
	Sound  sound.Options `json:"sound"`
	Reason string        `json:"reason"`
}

// Chromium decodes H.264 and AAC everywhere; HEVC needs a platform extension
// and 10-bit H.264 has no hardware decoder, so neither can be relied on.
func PlanFor(info *MediaInfo, encoder string) Plan {
	return planFor(info, encoder, 0, sound.Options{})
}

// planFor judges the audio track that will actually be sent, under what is asked of the sound.
func planFor(info *MediaInfo, encoder string, track int, snd sound.Options) Plan {
	plan := Plan{VideoCopy: true, AudioCopy: true, Sound: sound.Normalized(snd)}
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
		a := info.Audio[track]
		switch a.Codec {
		case "aac", "mp3", "opus", "flac", "vorbis":
		default:
			plan.AudioCopy = false
			reasons = append(reasons, "audio "+a.Codec)
		}
		// The sound is shaped while encoding, so a track that could have been copied is encoded too.
		if plan.Sound.Any() {
			plan.AudioCopy = false
			reasons = append(reasons, "sound")
		}
		if !plan.AudioCopy {
			plan.AudioCodec = "aac"
			// Lossless in, lossless out: FLAC is the lossless format every browser plays.
			if lossless(a) {
				plan.AudioCodec = "flac"
			}
		}
	}

	if len(reasons) > 0 {
		plan.Reason = "re-encoding " + strings.Join(reasons, " and ")
	} else {
		plan.Reason = "remux only"
	}
	return plan
}

// lossless reports an audio track that holds the sound exactly, which a lossy re-encode would throw away.
func lossless(a Stream) bool {
	switch a.Codec {
	case "truehd", "mlp", "alac", "wavpack", "tta", "ape":
		return true
	case "dts":
		// The core is lossy; only the Master Audio extension is not.
		return strings.Contains(strings.ToUpper(a.Profile), "HD MA")
	}
	return strings.HasPrefix(a.Codec, "pcm_")
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
