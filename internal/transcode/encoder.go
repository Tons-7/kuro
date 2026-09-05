package transcode

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// Hardware encoders, fastest first. Only H.264: it is the one codec every
// browser decodes.
var hardwareEncoders = []string{"h264_nvenc", "h264_qsv", "h264_amf"}

// IsHardwareEncoder reports whether a DetectEncoder result is a GPU encoder, so
// callers can tell a machine that transcodes in real time from one that will
// stall on HEVC/10-bit.
func IsHardwareEncoder(codec string) bool { return usesHardware(codec) }

// DetectEncoder picks the first hardware encoder that actually encodes on this
// machine, or libx264. Listing is not enough: every build lists h264_nvenc
// whatever GPU is fitted, and the wrong one fails at the first frame.
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

// trialEncode runs a second of 10-bit frames through the encoder with a
// session's arguments. 10-bit is the source that needs re-encoding most, and
// the input conversion is where it fails.
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
