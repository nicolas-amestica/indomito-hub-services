package functions

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestOpenAPIPathsMatchImplementedRoutes(t *testing.T) {
	raw, err := os.ReadFile("../openapi.fragment.json")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		OpenAPI string                                `json:"openapi"`
		Paths   map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("invalid OpenAPI JSON: %v", err)
	}
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("OpenAPI version = %q", document.OpenAPI)
	}
	seen := 0
	for _, route := range ImplementedRoutes {
		path := externalRoutePath(route.Path)
		methods, exists := document.Paths[path]
		if !exists {
			t.Errorf("OpenAPI is missing path %s", path)
			continue
		}
		if _, exists := methods[strings.ToLower(route.Method)]; !exists {
			t.Errorf("OpenAPI is missing operation %s %s", route.Method, route.Path)
		}
		seen++
	}
	operations := 0
	for _, methods := range document.Paths {
		for method := range methods {
			if method == "get" || method == "post" || method == "patch" || method == "put" || method == "delete" {
				operations++
			}
		}
	}
	if operations != seen {
		t.Fatalf("OpenAPI operations = %d, implemented routes = %d", operations, seen)
	}
}

func TestOpenAPIObjectSchemasAreClosedAndRequired(t *testing.T) {
	raw, err := os.ReadFile("../openapi.fragment.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	for name, rawSchema := range schemas {
		schema := rawSchema.(map[string]any)
		if schema["type"] != "object" {
			continue
		}
		if schema["additionalProperties"] != false {
			t.Errorf("schema %s is not closed", name)
		}
		properties, _ := schema["properties"].(map[string]any)
		if required, ok := schema["required"].([]any); len(properties) > 0 && (!ok || len(required) == 0) {
			t.Errorf("schema %s has no required fields", name)
		}
	}
}
