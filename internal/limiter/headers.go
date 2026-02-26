package limiter

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

type parsedWindow struct {
	limit  int
	count  int
	window time.Duration
}

func parseRetryAfter(v string, now time.Time) *time.Time {
	value := strings.TrimSpace(v)
	if value == "" {
		return nil
	}

	if secs, err := strconv.Atoi(value); err == nil && secs >= 0 {
		return new(now.Add(time.Duration(secs) * time.Second))
	}

	if ts, err := http.ParseTime(value); err == nil {
		if ts.Before(now) {
			return new(now)
		}
		return &ts
	}

	return nil
}

func parseRateHeader(limitHeader, countHeader string) []parsedWindow {
	limits := strings.Split(strings.TrimSpace(limitHeader), ",")
	if len(limits) == 0 || limits[0] == "" {
		return nil
	}

	counts := strings.Split(strings.TrimSpace(countHeader), ",")
	out := make([]parsedWindow, 0, len(limits))

	for i := range limits {
		lParts := strings.SplitN(strings.TrimSpace(limits[i]), ":", 2)
		if len(lParts) != 2 {
			continue
		}

		limit, err := strconv.Atoi(lParts[0])
		if err != nil || limit <= 0 {
			continue
		}

		windowSecs, err := strconv.Atoi(lParts[1])
		if err != nil || windowSecs <= 0 {
			continue
		}

		count := 0
		if i < len(counts) {
			cParts := strings.SplitN(strings.TrimSpace(counts[i]), ":", 2)
			if len(cParts) == 2 {
				if parsedCount, err := strconv.Atoi(cParts[0]); err == nil && parsedCount >= 0 {
					count = parsedCount
				}
			}
		}

		out = append(out, parsedWindow{
			limit:  limit,
			count:  count,
			window: time.Duration(windowSecs) * time.Second,
		})
	}

	return out
}
