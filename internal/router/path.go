package router

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"
)

type PathInfo struct {
	Region       string
	UpstreamPath string
	Bucket       string
}

var regionPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

type pathPatternNode struct {
	pattern  string
	children map[string]*pathPatternNode
	wildcard *pathPatternNode
}

var (
	pathPatternRoot     *pathPatternNode
	pathPatternRootOnce sync.Once
)

type pathContextKey struct{}

// ParsePath converts "/region/rest/of/path" into validated, canonical routing info.
func ParsePath(rawPath string) (PathInfo, error) {
	trimmed := strings.TrimSpace(rawPath)
	trimmed = strings.TrimPrefix(trimmed, "/")
	if trimmed == "" {
		return PathInfo{}, fmt.Errorf("missing region and upstream path")
	}

	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) != 2 {
		return PathInfo{}, fmt.Errorf("expected path format /{region}/")
	}

	region := strings.ToLower(strings.TrimSpace(parts[0]))
	if region == "" || !regionPattern.MatchString(region) {
		return PathInfo{}, fmt.Errorf("invalid region")
	}

	upstreamPath := path.Clean("/" + strings.TrimSpace(parts[1]))
	if upstreamPath == "/" {
		return PathInfo{}, fmt.Errorf("missing upstream path")
	}

	pattern := matchPathPattern(upstreamPath)
	bucketPath := upstreamPath
	if pattern != "" {
		bucketPath = pattern
	}

	return PathInfo{
		Region:       region,
		UpstreamPath: upstreamPath,
		Bucket:       region + ":" + strings.TrimPrefix(bucketPath, "/"),
	}, nil
}

func ensurePathPatternRoot() {
	pathPatternRootOnce.Do(func() {
		pathPatternRoot = &pathPatternNode{children: make(map[string]*pathPatternNode)}
		for _, p := range PathPatterns {
			insertPathPattern(p)
		}
	})
}

func insertPathPattern(pattern string) {
	current := pathPatternRoot
	cleanPattern := strings.TrimPrefix(pattern, "/")
	segments := strings.Split(cleanPattern, "/")

	for _, segment := range segments {
		isWildcard := strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}")
		if isWildcard {
			if current.wildcard == nil {
				current.wildcard = &pathPatternNode{children: make(map[string]*pathPatternNode)}
			}
			current = current.wildcard
			continue
		}

		if current.children[segment] == nil {
			current.children[segment] = &pathPatternNode{children: make(map[string]*pathPatternNode)}
		}
		current = current.children[segment]
	}

	current.pattern = pattern
}

func matchPathPattern(upstreamPath string) string {
	ensurePathPatternRoot()

	current := pathPatternRoot
	pathWithoutPrefix := strings.TrimPrefix(upstreamPath, "/")
	if pathWithoutPrefix == "" {
		return ""
	}

	start := 0
	for i := 0; i <= len(pathWithoutPrefix); i++ {
		if i == len(pathWithoutPrefix) || pathWithoutPrefix[i] == '/' {
			segment := pathWithoutPrefix[start:i]
			if next, ok := current.children[segment]; ok {
				current = next
			} else if current.wildcard != nil {
				current = current.wildcard
			} else {
				return ""
			}
			start = i + 1
		}
	}

	return current.pattern
}

// WithPath stores PathInfo in the request context.
func WithPath(ctx context.Context, info PathInfo) context.Context {
	return context.WithValue(ctx, pathContextKey{}, info)
}

// PathFromContext retrieves PathInfo from context.
func PathFromContext(ctx context.Context) (info PathInfo, ok bool) {
	info, ok = ctx.Value(pathContextKey{}).(PathInfo)
	return info, ok
}

// ProxyHandler validates the incoming path and injects path info for the proxy director.
func ProxyHandler(proxy http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info, err := ParsePath(r.URL.Path)
		if err != nil {
			http.Error(w, "expected path /{region}/riot/...", http.StatusBadRequest)
			return
		}

		r = r.WithContext(WithPath(r.Context(), info))
		proxy.ServeHTTP(w, r)
	})
}
