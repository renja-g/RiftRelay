package httputil

import (
	"strconv"
	"strings"
	"time"
)

// ParseRetryAfter parses the Retry-After header per RFC 7231 delta-seconds format.
// Returns (duration in seconds, true) or (0, false) if unparseable.
// Zero seconds yields (0, true) for immediate retry.
func ParseRetryAfter(value string) (time.Duration, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, false
	}

	secs, err := strconv.Atoi(trimmed)
	if err != nil || secs < 0 {
		return 0, false
	}
	return time.Duration(secs) * time.Second, true
}
