package swagger

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	defaultSpecURL = "https://www.mingweisamuel.com/riotapi-schema/openapi-3.0.0.min.json"
	uiPath         = "/swagger/"
	specPath       = "/swagger/openapi.json"
)

// Handler serves a lightweight Swagger UI and an OpenAPI spec proxy.
type Handler struct {
	client  *http.Client
	specURL string
}

func NewHandler() *Handler {
	return NewHandlerWithClient(defaultSpecURL, &http.Client{Timeout: 15 * time.Second})
}

func NewHandlerWithClient(specURL string, client *http.Client) *Handler {
	if strings.TrimSpace(specURL) == "" {
		specURL = defaultSpecURL
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Handler{
		client:  client,
		specURL: specURL,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case uiPath, "/swagger/index.html":
		h.serveUI(w)
	case specPath:
		h.serveOpenAPISpec(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) serveUI(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, swaggerUIHTML, specPath)
}

func (h *Handler) serveOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	upstreamReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, h.specURL, nil)
	if err != nil {
		http.Error(w, "cannot build swagger spec request", http.StatusBadGateway)
		return
	}

	resp, err := h.client.Do(upstreamReq)
	if err != nil {
		http.Error(w, "cannot load swagger spec upstream", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, fmt.Sprintf("swagger spec upstream returned status %d", resp.StatusCode), http.StatusBadGateway)
		return
	}

	var doc map[string]any
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&doc); err != nil {
		http.Error(w, "invalid swagger spec payload", http.StatusBadGateway)
		return
	}

	rewriteServers(doc, r)
	stripSecurity(doc)
	addPriorityHeaderParameter(doc)
	simplifyInfoDescription(doc)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(doc); err != nil {
		http.Error(w, "cannot encode swagger spec", http.StatusInternalServerError)
	}
}

func rewriteServers(doc map[string]any, r *http.Request) {
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = "localhost"
	}

	regionVariable := map[string]any{
		"default": "na1",
	}
	if enumValues := extractPlatformEnum(doc); len(enumValues) > 0 {
		regionVariable["enum"] = enumValues
		if first, ok := enumValues[0].(string); ok && first != "" {
			regionVariable["default"] = first
		}
	}

	doc["servers"] = []any{
		map[string]any{
			"url": fmt.Sprintf("%s://%s/{region}", requestScheme(r), host),
			"variables": map[string]any{
				"region": regionVariable,
			},
		},
	}
}

func stripSecurity(doc map[string]any) {
	delete(doc, "security")

	components, ok := doc["components"].(map[string]any)
	if ok {
		delete(components, "securitySchemes")
		if len(components) == 0 {
			delete(doc, "components")
		}
	}

	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		return
	}

	for _, rawPathItem := range paths {
		pathItem, ok := rawPathItem.(map[string]any)
		if !ok {
			continue
		}
		for _, method := range httpMethods {
			rawOperation, ok := pathItem[method]
			if !ok {
				continue
			}
			operation, ok := rawOperation.(map[string]any)
			if !ok {
				continue
			}
			delete(operation, "security")
		}
	}
}

func addPriorityHeaderParameter(doc map[string]any) {
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		return
	}

	for _, rawPathItem := range paths {
		pathItem, ok := rawPathItem.(map[string]any)
		if !ok {
			continue
		}

		pathLevelParameters := parametersSlice(pathItem["parameters"])
		pathHasPriority := hasPriorityHeaderParameter(pathLevelParameters)

		for _, method := range httpMethods {
			rawOperation, ok := pathItem[method]
			if !ok {
				continue
			}
			operation, ok := rawOperation.(map[string]any)
			if !ok {
				continue
			}

			operationParameters := parametersSlice(operation["parameters"])
			if pathHasPriority || hasPriorityHeaderParameter(operationParameters) {
				continue
			}

			operationParameters = append(operationParameters, newPriorityHeaderParameter())
			operation["parameters"] = operationParameters
		}
	}
}

func parametersSlice(raw any) []any {
	parameters, ok := raw.([]any)
	if !ok {
		return nil
	}
	return parameters
}

func hasPriorityHeaderParameter(parameters []any) bool {
	for _, rawParameter := range parameters {
		parameter, ok := rawParameter.(map[string]any)
		if !ok {
			continue
		}

		name, _ := parameter["name"].(string)
		location, _ := parameter["in"].(string)
		if strings.EqualFold(name, priorityHeaderName) && strings.EqualFold(location, "header") {
			return true
		}
	}
	return false
}

func newPriorityHeaderParameter() map[string]any {
	return map[string]any{
		"name":        priorityHeaderName,
		"in":          "header",
		"description": "Request priority hint. Use high to bypass pacing delay while still respecting rate limits.",
		"required":    false,
		"schema": map[string]any{
			"type": "string",
			"enum": []any{"high"},
		},
	}
}

func simplifyInfoDescription(doc map[string]any) {
	info, ok := doc["info"].(map[string]any)
	if !ok {
		return
	}

	info["description"] = "Riot Games API documentation proxied through [RiftRelay](https://github.com/renja-g/RiftRelay).\n\nThis OpenAPI specification is based on [riotapi-schema](https://github.com/MingweiSamuel/riotapi-schema), automatically generated daily from the Riot Games API Reference."
}

func extractPlatformEnum(doc map[string]any) []any {
	servers, ok := doc["servers"].([]any)
	if !ok {
		return nil
	}

	for _, rawServer := range servers {
		server, ok := rawServer.(map[string]any)
		if !ok {
			continue
		}

		variables, ok := server["variables"].(map[string]any)
		if !ok {
			continue
		}

		platform, ok := variables["platform"].(map[string]any)
		if !ok {
			continue
		}

		enumValues, ok := platform["enum"].([]any)
		if !ok || len(enumValues) == 0 {
			continue
		}

		out := make([]any, len(enumValues))
		copy(out, enumValues)
		return out
	}

	return nil
}

func requestScheme(r *http.Request) string {
	if forwardedProto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwardedProto != "" {
		parts := strings.Split(forwardedProto, ",")
		if len(parts) > 0 {
			value := strings.TrimSpace(parts[0])
			if value != "" {
				return value
			}
		}
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

const priorityHeaderName = "X-Priority"

var httpMethods = []string{
	"get",
	"put",
	"post",
	"delete",
	"patch",
	"options",
	"head",
	"trace",
}

const swaggerUIHTML = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>RiftRelay Swagger UI</title>
    <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css" />
  </head>
  <body>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
    <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-standalone-preset.js"></script>
    <script>
      window.onload = function() {
        SwaggerUIBundle({
          url: "%s",
          dom_id: "#swagger-ui",
          deepLinking: true,
          presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
          layout: "StandaloneLayout"
        });
      };
    </script>
  </body>
</html>
`
