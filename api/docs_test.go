package apidocs

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type openAPIDocument struct {
	OpenAPI  string                          `yaml:"openapi"`
	Security []map[string][]string           `yaml:"security"`
	Paths    map[string]map[string]yaml.Node `yaml:"paths"`
}

type operation struct {
	OperationID string                    `yaml:"operationId"`
	Tags        []string                  `yaml:"tags"`
	Responses   map[string]map[string]any `yaml:"responses"`
}

func loadDocument(t *testing.T) openAPIDocument {
	t.Helper()
	var document openAPIDocument
	if err := yaml.Unmarshal(Specification(), &document); err != nil {
		t.Fatalf("parse embedded OpenAPI document: %v", err)
	}
	return document
}

func TestOpenAPIDocumentDescribesEveryRegisteredRoute(t *testing.T) {
	document := loadDocument(t)
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("OpenAPI version = %q, want 3.1.0", document.OpenAPI)
	}
	if len(document.Security) == 0 {
		t.Fatal("OpenAPI document has no default security declaration")
	}

	want := registeredRoutes(t, "../internal/app/api.go")
	for _, realm := range []string{"console", "admin"} {
		base := "/api/v1/auth/" + realm
		for _, route := range []string{
			"GET " + base + "/login",
			"GET " + base + "/callback",
			"GET " + base + "/session",
			"POST " + base + "/logout",
			"GET " + base + "/logout/continue",
			"GET " + base + "/logout/provider",
			"GET " + base + "/logout/callback",
		} {
			want[route] = true
		}
	}
	if len(want) != 62 {
		t.Fatalf("discovered %d registered routes, want 62", len(want))
	}

	verbs := map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true, "head": true, "options": true, "trace": true}
	got := make(map[string]bool)
	operationIDs := make(map[string]string)
	for path, item := range document.Paths {
		for method, node := range item {
			if !verbs[method] {
				continue
			}
			var operation operation
			if err := node.Decode(&operation); err != nil {
				t.Fatalf("decode %s %s: %v", method, path, err)
			}
			route := strings.ToUpper(method) + " " + path
			got[route] = true
			if operation.OperationID == "" {
				t.Errorf("%s has no operationId", route)
			}
			if previous := operationIDs[operation.OperationID]; previous != "" {
				t.Errorf("operationId %q is shared by %s and %s", operation.OperationID, previous, route)
			}
			operationIDs[operation.OperationID] = route
			if len(operation.Tags) == 0 {
				t.Errorf("%s has no tags", route)
			}
			if len(operation.Responses) == 0 {
				t.Errorf("%s has no responses", route)
			}
		}
	}

	for route := range want {
		if !got[route] {
			t.Errorf("registered route is missing from OpenAPI: %s", route)
		}
	}
	for route := range got {
		if !want[route] {
			t.Errorf("OpenAPI operation has no registered route: %s", route)
		}
	}
}

func TestOpenAPIInternalReferencesResolve(t *testing.T) {
	var root yaml.Node
	if err := yaml.Unmarshal(Specification(), &root); err != nil {
		t.Fatalf("parse embedded OpenAPI document: %v", err)
	}
	document := root.Content[0]
	references := 0
	var walk func(*yaml.Node)
	walk = func(node *yaml.Node) {
		if node.Kind == yaml.MappingNode {
			for index := 0; index < len(node.Content); index += 2 {
				key, value := node.Content[index], node.Content[index+1]
				if key.Value == "$ref" && strings.HasPrefix(value.Value, "#/") {
					references++
					if resolveReference(document, strings.Split(strings.TrimPrefix(value.Value, "#/"), "/")) == nil {
						t.Errorf("unresolved OpenAPI reference %q", value.Value)
					}
				}
				walk(value)
			}
			return
		}
		for _, child := range node.Content {
			walk(child)
		}
	}
	walk(document)
	if references == 0 {
		t.Fatal("OpenAPI document contains no reusable references")
	}
}

func TestOpenAPIDoesNotDeclarePlainTextAPIResponses(t *testing.T) {
	contract := string(Specification())
	if strings.Contains(contract, "text/plain") || strings.Contains(contract, "PlainError") {
		t.Fatal("OpenAPI contract declares a plain-text API response")
	}
}

func resolveReference(node *yaml.Node, path []string) *yaml.Node {
	for _, segment := range path {
		if node.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for index := 0; index < len(node.Content); index += 2 {
			if node.Content[index].Value == segment {
				next = node.Content[index+1]
				break
			}
		}
		if next == nil {
			return nil
		}
		node = next
	}
	return node
}

func registeredRoutes(t *testing.T, filename string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("read %s: %v", filename, err)
	}
	matches := regexp.MustCompile(`HandleFunc\("([A-Z]+) ([^"]+)"`).FindAllStringSubmatch(string(raw), -1)
	routes := make(map[string]bool, len(matches))
	for _, match := range matches {
		routes[match[1]+" "+match[2]] = true
	}
	return routes
}

func TestSpecificationHandler(t *testing.T) {
	response := httptest.NewRecorder()
	SpecificationHandler(response, httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/yaml; charset=utf-8" {
		t.Fatalf("Content-Type = %q", contentType)
	}
	if !strings.HasPrefix(response.Body.String(), "openapi: 3.1.0") {
		t.Fatal("response did not contain the embedded OpenAPI document")
	}
	if cacheControl := response.Header().Get("Cache-Control"); cacheControl != "private, no-store" {
		t.Fatalf("Cache-Control = %q", cacheControl)
	}
}

func TestAccessHandlerDoesNotPersistToken(t *testing.T) {
	response := httptest.NewRecorder()
	AccessHandler(response, httptest.NewRequest(http.MethodGet, "/docs/access", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "Authorization: bearer") {
		t.Fatal("access page does not attach the in-memory bearer token")
	}
	if strings.Contains(body, "localStorage") || strings.Contains(body, "sessionStorage") {
		t.Fatal("access page stores its token in browser storage")
	}
	if response.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("access page has no Content-Security-Policy")
	}
}

func TestSwaggerHandler(t *testing.T) {
	handler := SwaggerHandler()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/docs/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if !strings.Contains(strings.ToLower(response.Body.String()), "swagger") {
		t.Fatal("Swagger UI response did not contain the UI bootstrap page")
	}

	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/docs/swagger-ui.css", nil))
	if asset.Code != http.StatusOK || asset.Body.Len() == 0 {
		t.Fatalf("embedded Swagger UI asset returned status %d and %d bytes", asset.Code, asset.Body.Len())
	}
}
