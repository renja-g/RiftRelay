package ratelimit

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

type bucketEntry struct {
	at time.Time
	id uint64
}

// Bucket models a simple sliding-window bucket.
type Bucket struct {
	Limit   int
	Window  time.Duration
	entries []bucketEntry
	nextID  uint64
}

func (b *Bucket) prune(now time.Time) {
	cutoff := now.Add(-b.Window)
	idx := 0
	for _, e := range b.entries {
		if e.at.After(cutoff) {
			b.entries[idx] = e
			idx++
		}
	}
	b.entries = b.entries[:idx]
}

// nextAvailable returns the earliest time a new request can be added.
func (b *Bucket) nextAvailable(now time.Time) time.Time {
	if b.Limit == 0 {
		return now
	}
	b.prune(now)
	if len(b.entries) < b.Limit {
		return now
	}
	return b.entries[0].at.Add(b.Window)
}

func (b *Bucket) add(at time.Time) uint64 {
	id := b.nextID
	b.nextID++
	b.entries = append(b.entries, bucketEntry{at: at, id: id})
	return id
}

func (b *Bucket) remove(id uint64) {
	for i, e := range b.entries {
		if e.id == id {
			b.entries = append(b.entries[:i], b.entries[i+1:]...)
			return
		}
	}
}

func (b *Bucket) remaining(now time.Time) (int, time.Duration) {
	b.prune(now)
	used := len(b.entries)
	remaining := b.Limit - used
	if remaining < 0 {
		remaining = 0
	}
	var untilReset time.Duration
	if len(b.entries) == 0 {
		untilReset = b.Window
	} else {
		untilReset = b.entries[0].at.Add(b.Window).Sub(now)
		if untilReset < 0 {
			untilReset = 0
		}
	}
	return remaining, untilReset
}

func parseLimitHeader(v string) []Bucket {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	buckets := make([]Bucket, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		fields := strings.Split(p, ":")
		if len(fields) != 2 {
			continue
		}
		limit, err1 := strconv.Atoi(fields[0])
		seconds, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil || limit <= 0 || seconds <= 0 {
			continue
		}
		buckets = append(buckets, Bucket{
			Limit:  limit,
			Window: time.Duration(seconds) * time.Second,
		})
	}
	return buckets
}

func parseCountHeader(v string) map[time.Duration]int {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	counts := make(map[time.Duration]int, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		fields := strings.Split(p, ":")
		if len(fields) != 2 {
			continue
		}
		count, err1 := strconv.Atoi(fields[0])
		seconds, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil || count < 0 || seconds <= 0 {
			continue
		}
		counts[time.Duration(seconds)*time.Second] = count
	}
	return counts
}

// mergeBuckets prefers method buckets, then app buckets; keeps unique window sizes.
func mergeBuckets(methodBuckets, appBuckets []Bucket) []Bucket {
	all := append([]Bucket{}, methodBuckets...)
	added := make(map[time.Duration]bool, len(all))
	for _, b := range all {
		added[b.Window] = true
	}
	for _, b := range appBuckets {
		if !added[b.Window] {
			all = append(all, b)
			added[b.Window] = true
		}
	}
	return all
}

// UpdateBucketsFromHeaders parses Riot rate limit headers to buckets.
func UpdateBucketsFromHeaders(h http.Header) []Bucket {
	methodBuckets := parseLimitHeader(h.Get("X-Method-Rate-Limit"))
	appBuckets := parseLimitHeader(h.Get("X-App-Rate-Limit"))
	return mergeBuckets(methodBuckets, appBuckets)
}
