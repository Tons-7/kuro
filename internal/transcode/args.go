package transcode

import (
	"fmt"
	"strings"
)

// Hardware decode keeps frames on the GPU in the source pixel format, so a
// 10-bit source feeding an 8-bit H.264 encoder cannot be converted and ffmpeg
// writes nothing.
func (s *Session) canDecodeInHardware() bool {
	v := s.Info.Video
	if v == nil {
		return false
	}
	if v.BitDepth > 8 && isH264(s.Plan.VideoCodec) {
		return false
	}
	return true
}

const (
	// Twice realtime buffers ahead without racing to the end of the file.
	readRate = 2.0

	// A head start so playback begins at once. Float: ffmpeg rejects an integer.
	initialBurstSeconds = 60.0
)

func (s *Session) args(offset float64, startSegment int) []string {
	a := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}

	// Pinning the output to d3d11 keeps frames on the GPU, where the pixel
	// format conversion a 10-bit source needs cannot happen.
	if !s.Plan.VideoCopy && usesHardware(s.encoder) && s.canDecodeInHardware() {
		a = append(a, "-hwaccel", "d3d11va")
	}

	if offset > 0 {
		// A copy seek lands on the preceding keyframe; the bias reaches the
		// intended one.
		seek := offset
		if s.Plan.VideoCopy {
			seek += 0.5
		}
		a = append(a, "-ss", fmt.Sprintf("%.3f", seek))
	}

	// Uncapped, ffmpeg converts the whole episode as fast as the disk allows and
	// stutters the machine. The burst covers the start and early seeks; the rate
	// keeps it ahead of the viewer.
	a = append(a,
		"-readrate", fmt.Sprintf("%.1f", readRate),
		"-readrate_initial_burst", fmt.Sprintf("%.0f", initialBurstSeconds),
	)

	a = append(a,
		"-i", s.Source,
		"-map", "0:v:0",
		// Read directly, not via AudioTrack(): args runs inside launch, whose
		// callers already hold s.mu (await) or startMu (start), and re-locking
		// s.mu here would deadlock against await.
		"-map", fmt.Sprintf("0:a:%d?", s.audioTrack),
		"-map_metadata", "-1",
		"-map_chapters", "-1",
	)

	a = append(a, s.videoArgs()...)
	a = append(a, s.audioArgs()...)

	a = append(a,
		// Preserved timestamps keep segments aligned to the playlist the
		// player was handed before any encoding started.
		"-copyts",
		"-avoid_negative_ts", "disabled",
		"-max_muxing_queue_size", "1024",
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%.0f", SegmentSeconds),
		"-hls_segment_type", "fmp4",
		"-hls_playlist_type", "vod",
		"-hls_list_size", "0",
		"-start_number", fmt.Sprint(startSegment),
		// frag_discont writes real timestamps into each fragment for a mid-file
		// start; skip_sidx avoids shifting packets at open-GOP boundaries.
		"-hls_segment_options", "movflags=+frag_discont+skip_sidx",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", "%d.mp4",
		"-y", "playlist.m3u8",
	)
	return a
}

func (s *Session) videoArgs() []string {
	if s.Plan.VideoCopy {
		return []string{"-c:v", "copy"}
	}

	codec := s.Plan.VideoCodec
	args := []string{"-c:v", codec}

	// Hardware encoders ignore -force_key_frames, so the interval has to be
	// set directly or segment boundaries drift out of alignment.
	gop := int(SegmentSeconds * 24)
	if usesHardware(codec) {
		args = append(args, "-g", fmt.Sprint(gop), "-keyint_min", fmt.Sprint(gop))
	} else {
		args = append(args,
			"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%.0f)", SegmentSeconds),
			"-sc_threshold", "0", // a scene cut would move the forced keyframe
			"-preset", "veryfast",
		)
	}

	switch {
	case strings.Contains(codec, "nvenc"):
		args = append(args, "-preset", "p4", "-rc", "vbr", "-cq", "23")
	case strings.Contains(codec, "qsv"):
		args = append(args, "-global_quality", "23")
	case strings.Contains(codec, "amf"):
		args = append(args, "-quality", "balanced", "-rc", "vbr_latency")
	case codec == "libx264":
		args = append(args, "-crf", "21")
	}

	// 10-bit H.264 is one of the formats the browser cannot decode, so
	// preserving the source depth makes the output equally unplayable.
	if isH264(codec) {
		args = append(args, "-pix_fmt", "yuv420p")
	}
	return args
}

func (s *Session) audioArgs() []string {
	if s.Plan.AudioCopy {
		return []string{"-c:a", "copy"}
	}
	return []string{"-c:a", "aac", "-b:a", "192k", "-ac", "2"}
}

func usesHardware(codec string) bool {
	for _, suffix := range []string{"nvenc", "qsv", "amf", "vaapi"} {
		if strings.Contains(codec, suffix) {
			return true
		}
	}
	return false
}

func isH264(codec string) bool {
	return strings.HasPrefix(codec, "libx264") || strings.HasPrefix(codec, "h264")
}
