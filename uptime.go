package main

import (
	"regexp"
	"strconv"
	"strings"
)

// uptimeRe matches Omada's uptime strings like "3day(s) 1h 46m 13s",
// tolerating missing components (e.g. "10h" or "5m 3s").
var uptimeRe = regexp.MustCompile(`^(?:(\d+)day\(s\))?\s*(?:(\d+)h)?\s*(?:(\d+)m)?\s*(?:(\d+)s)?$`)

// parseUptimeSeconds parses an Omada device uptime string into seconds.
// It returns false if the string is empty or doesn't match the expected
// format, so callers can skip the metric rather than emit a bogus value.
func parseUptimeSeconds(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}

	m := uptimeRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	if m[1] == "" && m[2] == "" && m[3] == "" && m[4] == "" {
		return 0, false
	}

	var total int64
	if m[1] != "" {
		v, _ := strconv.ParseInt(m[1], 10, 64)
		total += v * 86400
	}
	if m[2] != "" {
		v, _ := strconv.ParseInt(m[2], 10, 64)
		total += v * 3600
	}
	if m[3] != "" {
		v, _ := strconv.ParseInt(m[3], 10, 64)
		total += v * 60
	}
	if m[4] != "" {
		v, _ := strconv.ParseInt(m[4], 10, 64)
		total += v
	}
	return total, true
}
