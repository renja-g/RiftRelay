package router

import (
	"context"
	"net/http"
	"strings"
)

type PathInfo struct {
	Region string
	Path   string
}

// ShiftPath splits "/region/rest/of/path" into PathInfo.
func ShiftPath(p string) (info PathInfo, ok bool) {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return info, false
	}

	parts := strings.SplitN(p, "/", 2)
	info.Region = parts[0]
	if len(parts) == 1 {
		info.Path = "/"
		return info, true
	}

	info.Path = "/" + parts[1]
	return info, true
}

type pathContextKey struct{}

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
		info, ok := ShiftPath(r.URL.Path)
		if !ok || info.Path == "/" {
			http.Error(w, "expected path /{region}/riot/...", http.StatusBadRequest)
			return
		}

		r = r.WithContext(WithPath(r.Context(), info))
		proxy.ServeHTTP(w, r)
	})
}
