package transcode

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// Hardware encoders, fastest first. Only H.264: it is the one codec every browser decodes.
var hardwareEncoders = []string{"h264_nvenc", "h264_qsv", "h264_amf"}

// IsHardwareEncoder: a GPU encoder, which transcodes HEVC/10-bit in real time.
func IsHardwareEncoder(codec string) bool { return usesHardware(codec) }

// DetectEncoder picks the first hardware encoder that really encodes here (all builds list nvenc), or libx264.
func DetectEncoder(ctx context.Context, ffmpeg string, log *slog.Logger) string {
	out, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-encoders").Output()
	if err != nil {
		return softwareEncoder
	}
	available := string(out)

	for _, codec := range hardwareEncoders {
		if !strings.Contains(available, codec) {
			continue
		}
		if err := trialEncode(ctx, ffmpeg, codec); err != nil {
			log.Info("hardware encoder unusable here", "encoder", codec, "err", err)
			continue
		}
		return codec
	}
	return softwareEncoder
}

// CanEncode: ffmpeg has libx264 (Fedora's ffmpeg-free lacks it) or a working hardware encoder.
func CanEncode(ctx context.Context, ffmpeg string) bool {
	out, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-encoders").Output()
	if err != nil {
		return false
	}
	available := string(out)
	if strings.Contains(available, " "+softwareEncoder+" ") {
		return true
	}
	for _, codec := range hardwareEncoders {
		if strings.Contains(available, codec) && trialEncode(ctx, ffmpeg, codec) == nil {
			return true
		}
	}
	return false
}

// Version is the version ffmpeg or ffprobe reports, or "".
func Version(ctx context.Context, binary string) string {
	out, err := exec.CommandContext(ctx, binary, "-hide_banner", "-version").Output()
	if err != nil {
		return ""
	}
	// "ffmpeg version 7.1-full_build-www.gyan.dev Copyright ..."
	fields := strings.Fields(string(out))
	if len(fields) >= 3 && fields[1] == "version" {
		return fields[2]
	}
	return ""
}

// trialEncode runs a second of 10-bit frames through the encoder: the input most likely to fail.
func trialEncode(ctx context.Context, ffmpeg, codec string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	s := &Session{Plan: Plan{VideoCodec: codec}, encoder: codec}
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=24,format=yuv420p10le",
		"-frames:v", "24",
	}
	args = append(args, s.videoArgs()...)
	args = append(args, "-f", "null", "-")

	out, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, lastLines(string(out), 3))
	}
	return nil
}
