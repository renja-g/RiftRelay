package swagger

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServeUI(t *testing.T) {
	handler := NewHandlerWithClient("http://example.invalid/spec.json", http.DefaultClient)

	req := httptest.NewRequest(http.MethodGet, "http://relay.local/swagger/", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.Code)
	}
	if !strings.Contains(resp.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("expected HTML content type, got %q", resp.Header().Get("Content-Type"))
	}
	if !strings.Contains(resp.Body.String(), specPath) {
		t.Fatalf("expected UI to reference %q", specPath)
	}
}

func TestHandlerServeOpenAPISpec(t *testing.T) {
	t.Run("rewrites servers strips auth and adds priority header", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"openapi":"3.0.0",
				"servers":[{"url":"https://{platform}.api.riotgames.com","variables":{"platform":{"default":"americas","enum":["americas","europe"]}}}],
				"security":[{"X-Riot-Token":[]}],
				"components":{"securitySchemes":{"X-Riot-Token":{"type":"apiKey","in":"header","name":"X-Riot-Token"}}},
				"paths":{
					"/lol/status/v4/platform-data":{
						"get":{
							"security":[{"X-Riot-Token":[]}],
							"parameters":[{"name":"locale","in":"query","required":false,"schema":{"type":"string"}}]
						}
					}
				}
			}`))
		}))
		t.Cleanup(upstream.Close)

		handler := NewHandlerWithClient(upstream.URL, upstream.Client())

		req := httptest.NewRequest(http.MethodGet, "http://relay.local:8985/swagger/openapi.json", nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("expected status %d, got %d, body=%q", http.StatusOK, resp.Code, resp.Body.String())
		}

		var doc map[string]any
		if err := json.Unmarshal(resp.Body.Bytes(), &doc); err != nil {
			t.Fatalf("decode response: %v", err)
		}

		servers, ok := doc["servers"].([]any)
		if !ok || len(servers) != 1 {
			t.Fatalf("expected exactly one server, got %+v", doc["servers"])
		}

		server, ok := servers[0].(map[string]any)
		if !ok {
			t.Fatalf("expected server object, got %#v", servers[0])
		}
		if got, _ := server["url"].(string); got != "https://relay.local:8985/{region}" {
			t.Fatalf("expected rewritten server url, got %q", got)
		}

		variables, ok := server["variables"].(map[string]any)
		if !ok {
			t.Fatalf("expected variables object, got %#v", server["variables"])
		}
		region, ok := variables["region"].(map[string]any)
		if !ok {
			t.Fatalf("expected region variable, got %#v", variables["region"])
		}
		enumValues, ok := region["enum"].([]any)
		if !ok || len(enumValues) != 2 {
			t.Fatalf("expected 2 region enum values, got %#v", region["enum"])
		}

		if _, exists := doc["security"]; exists {
			t.Fatalf("expected top-level security to be removed")
		}

		if rawComponents, exists := doc["components"]; exists {
			components, ok := rawComponents.(map[string]any)
			if !ok {
				t.Fatalf("expected components object when present")
			}
			if _, exists := components["securitySchemes"]; exists {
				t.Fatalf("expected components.securitySchemes to be removed")
			}
		}

		paths, ok := doc["paths"].(map[string]any)
		if !ok {
			t.Fatalf("expected paths object")
		}
		pathItem, ok := paths["/lol/status/v4/platform-data"].(map[string]any)
		if !ok {
			t.Fatalf("expected path item")
		}
		getOperation, ok := pathItem["get"].(map[string]any)
		if !ok {
			t.Fatalf("expected get operation")
		}
		if _, exists := getOperation["security"]; exists {
			t.Fatalf("expected operation security to be removed")
		}

		parameters := parametersSlice(getOperation["parameters"])
		if len(parameters) != 2 {
			t.Fatalf("expected existing and priority params, got %d", len(parameters))
		}
		if countPriorityHeaderParameters(parameters) != 1 {
			t.Fatalf("expected exactly one priority header parameter")
		}
	})

	t.Run("does not duplicate priority header parameter", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"openapi":"3.0.0",
				"paths":{
					"/one":{
						"get":{"parameters":[{"name":"X-Priority","in":"header","schema":{"type":"string"}}]}
					},
					"/two":{
						"parameters":[{"name":"X-Priority","in":"header","schema":{"type":"string"}}],
						"get":{"parameters":[{"name":"foo","in":"query","schema":{"type":"string"}}]}
					}
				}
			}`))
		}))
		t.Cleanup(upstream.Close)

		handler := NewHandlerWithClient(upstream.URL, upstream.Client())
		req := httptest.NewRequest(http.MethodGet, "http://relay.local:8985/swagger/openapi.json", nil)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("expected status %d, got %d, body=%q", http.StatusOK, resp.Code, resp.Body.String())
		}

		var doc map[string]any
		if err := json.Unmarshal(resp.Body.Bytes(), &doc); err != nil {
			t.Fatalf("decode response: %v", err)
		}

		paths, ok := doc["paths"].(map[string]any)
		if !ok {
			t.Fatalf("expected paths object")
		}

		oneOperation := paths["/one"].(map[string]any)["get"].(map[string]any)
		if countPriorityHeaderParameters(parametersSlice(oneOperation["parameters"])) != 1 {
			t.Fatalf("expected one priority header on /one")
		}

		twoOperation := paths["/two"].(map[string]any)["get"].(map[string]any)
		if countPriorityHeaderParameters(parametersSlice(twoOperation["parameters"])) != 0 {
			t.Fatalf("expected no operation-level priority header on /two due to path-level parameter")
		}
	})

	t.Run("simplifies info description", func(t *testing.T) {
		originalDesc := "\nOpenAPI/Swagger version of the [Riot API](https://developer.riotgames.com/). Automatically generated daily.\n## OpenAPI Spec File\nThe following versions of the Riot API spec file are available:\n- openapi-3.0.0.json\n- openapi-3.0.0.min.json\n## Other Files\n- Missing DTOs: missing.json\n## Source Code\nSource code on [GitHub](https://github.com/MingweiSamuel/riotapi-schema). Pull requests welcome!\n## Automatically Generated\nRebuilt on [Travis CI](https://travis-ci.com/MingweiSamuel/riotapi-schema/builds) daily.\n***\n"
		testSpec := map[string]any{
			"openapi": "3.0.0",
			"info": map[string]any{
				"title":          "Riot API",
				"description":    originalDesc,
				"termsOfService": "https://developer.riotgames.com/terms",
				"version":        "test",
			},
		}
		specJSON, _ := json.Marshal(testSpec)

		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(specJSON)
		}))
		t.Cleanup(upstream.Close)

		handler := NewHandlerWithClient(upstream.URL, upstream.Client())
		req := httptest.NewRequest(http.MethodGet, "http://relay.local/swagger/openapi.json", nil)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("expected status %d, got %d, body=%q", http.StatusOK, resp.Code, resp.Body.String())
		}

		var doc map[string]any
		if err := json.Unmarshal(resp.Body.Bytes(), &doc); err != nil {
			t.Fatalf("decode response: %v", err)
		}

		info, ok := doc["info"].(map[string]any)
		if !ok {
			t.Fatalf("expected info object")
		}

		description, ok := info["description"].(string)
		if !ok {
			t.Fatalf("expected description string")
		}

		if !strings.Contains(description, "riotapi-schema") {
			t.Fatalf("expected description to contain riotapi-schema attribution, got %q", description)
		}
		if !strings.Contains(description, "github.com/MingweiSamuel/riotapi-schema") {
			t.Fatalf("expected description to contain GitHub repo link, got %q", description)
		}
		if strings.Contains(description, "openapi-3.0.0.json") {
			t.Fatalf("expected description to not contain file version references, got %q", description)
		}
		if strings.Contains(description, "Travis CI") {
			t.Fatalf("expected description to not contain CI/build info, got %q", description)
		}

		termsOfService, ok := info["termsOfService"].(string)
		if !ok || termsOfService != "https://developer.riotgames.com/terms" {
			t.Fatalf("expected termsOfService to be preserved")
		}
	})

	t.Run("returns bad gateway on upstream errors", func(t *testing.T) {
		tests := []struct {
			name    string
			specURL string
			handler http.Handler
		}{
			{
				name:    "connection failure",
				specURL: "http://127.0.0.1:1/spec.json",
			},
			{
				name: "upstream non-200",
				handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "oops", http.StatusInternalServerError)
				}),
			},
		}

		for _, tt := range tests {
			tt := tt
			t.Run(tt.name, func(t *testing.T) {
				specURL := tt.specURL
				client := http.DefaultClient
				if tt.handler != nil {
					upstream := httptest.NewServer(tt.handler)
					t.Cleanup(upstream.Close)
					specURL = upstream.URL
					client = upstream.Client()
				}

				handler := NewHandlerWithClient(specURL, client)
				req := httptest.NewRequest(http.MethodGet, "http://relay.local/swagger/openapi.json", nil)
				resp := httptest.NewRecorder()
				handler.ServeHTTP(resp, req)

				if resp.Code != http.StatusBadGateway {
					t.Fatalf("expected status %d, got %d", http.StatusBadGateway, resp.Code)
				}
			})
		}
	})
}

func countPriorityHeaderParameters(parameters []any) int {
	count := 0
	for _, rawParameter := range parameters {
		parameter, ok := rawParameter.(map[string]any)
		if !ok {
			continue
		}
		name, _ := parameter["name"].(string)
		location, _ := parameter["in"].(string)
		if strings.EqualFold(name, priorityHeaderName) && strings.EqualFold(location, "header") {
			count++
		}
	}
	return count
}

func TestHandlerUnknownPath(t *testing.T) {
	handler := NewHandlerWithClient("http://example.invalid/spec.json", http.DefaultClient)

	req := httptest.NewRequest(http.MethodGet, "http://relay.local/swagger/unknown", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, resp.Code)
	}
}
