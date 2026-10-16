package transcode

import (
	"slices"
	"strings"
)

// mergeASS unions two partial reads of a track: the first's header, events deduplicated in time order.
func mergeASS(have, read string) string {
	if strings.TrimSpace(have) == "" {
		return read
	}
	header, events := splitASS(have)
	_, more := splitASS(read)

	seen := make(map[string]bool, len(events)+len(more))
	var all []string
	for _, line := range append(events, more...) {
		if !seen[line] {
			seen[line] = true
			all = append(all, line)
		}
	}
	slices.SortStableFunc(all, func(a, b string) int { return strings.Compare(eventStart(a), eventStart(b)) })
	return header + strings.Join(all, "\n") + "\n"
}

// splitASS returns everything up to and including the [Events] Format line, and the event lines after it.
func splitASS(s string) (string, []string) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	var header strings.Builder
	var events []string
	inEvents := false
	for _, line := range lines {
		if inEvents && (strings.HasPrefix(line, "Dialogue:") || strings.HasPrefix(line, "Comment:")) {
			events = append(events, line)
			continue
		}
		if strings.TrimSpace(line) == "" && inEvents {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "[Events]") {
			inEvents = true
		}
		header.WriteString(line)
		header.WriteString("\n")
	}
	return header.String(), events
}

// eventStart is the Start field (H:MM:SS.cc), zero-padded to sort as text.
func eventStart(line string) string {
	_, rest, ok := strings.Cut(line, ":")
	if !ok {
		return ""
	}
	fields := strings.SplitN(rest, ",", 3)
	if len(fields) < 2 {
		return ""
	}
	start := strings.TrimSpace(fields[1])
	if i := strings.IndexByte(start, ':'); i == 1 {
		start = "0" + start
	}
	return start
}
