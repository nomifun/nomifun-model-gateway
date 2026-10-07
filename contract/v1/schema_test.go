// SPDX-License-Identifier: Apache-2.0
package v1_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/mock"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

const schemaResource = "https://schema.nomifun.example/openapi.json"

// No test can silently fetch a changed remote schema or follow an external ref.
type offlineSchemaLoader struct{}

func (offlineSchemaLoader) Load(location string) (any, error) {
	return nil, fmt.Errorf("external schema resource is forbidden: %s", location)
}

type openAPIContract struct {
	doc      map[string]any
	compiler *jsonschema.Compiler
}

func loadOpenAPI(t *testing.T) *openAPIContract {
	t.Helper()
	data, err := os.ReadFile("../../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// yaml.v3 rejects duplicate mapping keys instead of silently replacing them.
	var document map[string]any
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("parse actual openapi.yaml: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("openapi.yaml must contain exactly one YAML document: %v", err)
	}
	// Convert YAML integers to exact json.Number; float64 would lose int64 bounds.
	doc := jsonValue(t, document).(map[string]any)
	if doc["openapi"] != "3.1.0" || doc["jsonSchemaDialect"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("expected frozen OAS 3.1.0 and JSON Schema Draft 2020-12, got %v / %v", doc["openapi"], doc["jsonSchemaDialect"])
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.UseLoader(offlineSchemaLoader{})
	// OpenAPI components use document-local references. Preserve their original
	// pointers rather than generating a second, potentially divergent schema.
	doc["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	if err := compiler.AddResource(schemaResource, doc); err != nil {
		t.Fatal(err)
	}
	return &openAPIContract{doc: doc, compiler: compiler}
}

func jsonValue(t *testing.T, value any) any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	result, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func object(t *testing.T, value any) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected object, got %T: %v", value, value)
	}
	return result
}

func escapePointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

func (c *openAPIContract) resolve(t *testing.T, pointer string) any {
	t.Helper()
	if !strings.HasPrefix(pointer, "#/") {
		t.Fatalf("only local JSON pointers are permitted: %q", pointer)
	}
	var value any = c.doc
	for _, segment := range strings.Split(strings.TrimPrefix(pointer, "#/"), "/") {
		segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		switch current := value.(type) {
		case map[string]any:
			var exists bool
			value, exists = current[segment]
			if !exists {
				t.Fatalf("unresolved ref %q at %q", pointer, segment)
			}
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(current) {
				t.Fatalf("unresolved array ref %q", pointer)
			}
			value = current[index]
		default:
			t.Fatalf("unresolved ref %q through %T", pointer, value)
		}
	}
	return value
}

func (c *openAPIContract) dereference(t *testing.T, value any) map[string]any {
	t.Helper()
	result := object(t, value)
	seen := map[string]bool{}
	for {
		ref, exists := result["$ref"]
		if !exists {
			return result
		}
		pointer, ok := ref.(string)
		if !ok || seen[pointer] {
			t.Fatalf("invalid or cyclic OpenAPI object ref: %v", ref)
		}
		seen[pointer] = true
		result = object(t, c.resolve(t, pointer))
	}
}

func (c *openAPIContract) schema(t *testing.T, pointer string) *jsonschema.Schema {
	t.Helper()
	c.resolve(t, pointer)
	schema, err := c.compiler.Compile(schemaResource + pointer)
	if err != nil {
		t.Fatalf("compile %s using Draft 2020-12: %v", pointer, err)
	}
	if schema.DraftVersion != 2020 {
		t.Fatalf("%s compiled as draft %d", pointer, schema.DraftVersion)
	}
	return schema
}

func (c *openAPIContract) operation(t *testing.T, path, method string) map[string]any {
	t.Helper()
	paths := object(t, c.doc["paths"])
	return object(t, object(t, paths[path])[strings.ToLower(method)])
}

type schemaMedia struct {
	document map[string]any
	pointer  string
}

func (c *openAPIContract) responseMedia(t *testing.T, path, method string, status int, mediaType string) schemaMedia {
	t.Helper()
	op := c.operation(t, path, method)
	responses := object(t, op["responses"])
	statusKey := strconv.Itoa(status)
	response, exists := responses[statusKey]
	if !exists {
		response, exists = responses["default"]
		statusKey = "default"
	}
	if !exists {
		t.Fatalf("%s %s does not document status %d", method, path, status)
	}
	pointer := "#/paths/" + escapePointer(path) + "/" + strings.ToLower(method) + "/responses/" + statusKey
	responseObject := object(t, response)
	seen := map[string]bool{}
	for {
		ref, exists := responseObject["$ref"]
		if !exists {
			break
		}
		var ok bool
		pointer, ok = ref.(string)
		if !ok || seen[pointer] {
			t.Fatalf("invalid/cyclic response ref: %v", ref)
		}
		seen[pointer] = true
		responseObject = object(t, c.resolve(t, pointer))
	}
	content := object(t, responseObject["content"])
	return schemaMedia{object(t, content[mediaType]), pointer + "/content/" + escapePointer(mediaType)}
}

func (c *openAPIContract) mediaSchema(t *testing.T, media schemaMedia, keyword string) *jsonschema.Schema {
	t.Helper()
	_, exists := media.document[keyword]
	if !exists {
		t.Fatalf("media type has no %s", keyword)
	}
	return c.schema(t, media.pointer+"/"+keyword)
}

var frozenPaths = map[string]string{
	"/nomifun/v1/meta": "GET", "/nomifun/v1/catalog": "GET", "/nomifun/v1/account": "GET",
	"/v1/models": "GET", "/v1/chat/completions": "POST", "/v1/responses": "POST",
	"/v1/embeddings": "POST", "/v1/images/generations": "POST", "/v1/images/edits": "POST",
	"/v1/rerank": "POST", "/v1/messages": "POST", "/v1/messages/count_tokens": "POST",
	"/v1beta/models/{model}:generateContent": "POST", "/v1beta/models/{model}:streamGenerateContent": "POST",
}

func TestOpenAPIFrozenSurface(t *testing.T) {
	c := loadOpenAPI(t)
	if object(t, c.doc["info"])["version"] != v1.Version {
		t.Fatalf("OpenAPI info.version must equal %s", v1.Version)
	}
	paths := object(t, c.doc["paths"])
	if len(paths) != len(frozenPaths) {
		t.Fatalf("frozen path count = %d, want %d", len(paths), len(frozenPaths))
	}
	schemes := object(t, object(t, c.doc["components"])["securitySchemes"])
	headerSchemes := map[string]bool{}
	headerKinds := map[string]bool{}
	queryScheme := ""
	for name, value := range schemes {
		scheme := object(t, value)
		if scheme["type"] == "http" && scheme["scheme"] == "bearer" {
			headerSchemes[name] = true
			headerKinds["bearer"] = true
		} else if scheme["type"] == "apiKey" && scheme["in"] == "header" && (scheme["name"] == "x-api-key" || scheme["name"] == "x-goog-api-key") {
			headerSchemes[name] = true
			headerKinds[scheme["name"].(string)] = true
		} else if scheme["type"] == "apiKey" && scheme["in"] == "query" && scheme["name"] == "key" {
			queryScheme = name
		} else {
			t.Errorf("unexpected security scheme %s: %v", name, scheme)
		}
	}
	if len(headerSchemes) != 3 || len(headerKinds) != 3 || queryScheme == "" {
		t.Fatalf("must document bearer, both header keys, and Gemini query key")
	}
	operationIDs := map[string]string{}
	for path, method := range frozenPaths {
		t.Run(method+" "+path, func(t *testing.T) {
			pathItem := object(t, paths[path])
			for _, candidate := range []string{"get", "post", "put", "patch", "delete", "head", "options", "trace"} {
				if _, exists := pathItem[candidate]; exists && candidate != strings.ToLower(method) {
					t.Fatalf("unexpected frozen operation %s %s", candidate, path)
				}
			}
			op := c.operation(t, path, method)
			id, ok := op["operationId"].(string)
			if !ok || id == "" {
				t.Fatal("missing operationId")
			}
			if previous, exists := operationIDs[id]; exists {
				t.Fatalf("operationId %s already belongs to %s", id, previous)
			}
			operationIDs[id] = path
			security, exists := op["security"]
			if !exists {
				security = c.doc["security"]
			}
			alternatives, ok := security.([]any)
			if !ok {
				t.Fatal("missing security declaration")
			}
			if path == "/nomifun/v1/meta" {
				if len(alternatives) != 0 {
					t.Fatal("meta must explicitly allow anonymous access")
				}
			} else {
				want := map[string]bool{}
				for name := range headerSchemes {
					want[name] = true
				}
				if strings.HasPrefix(path, "/v1beta/") {
					want[queryScheme] = true
				}
				for _, alternative := range alternatives {
					requirement := object(t, alternative)
					if len(requirement) != 1 {
						t.Fatal("credentials are OR alternatives, not combined requirements")
					}
					for name, scopes := range requirement {
						if !want[name] {
							t.Fatalf("unexpected credential alternative %s", name)
						}
						if values, ok := scopes.([]any); !ok || len(values) != 0 {
							t.Fatal("API keys must not declare OAuth scopes")
						}
						delete(want, name)
					}
				}
				if len(want) != 0 {
					t.Fatalf("missing credential alternatives: %v", want)
				}
			}
			parameters := []map[string]any{}
			for _, source := range []map[string]any{pathItem, op} {
				if values, exists := source["parameters"]; exists {
					for _, value := range values.([]any) {
						parameters = append(parameters, c.dereference(t, value))
					}
				}
			}
			findParameter := func(location, name string) map[string]any {
				for _, parameter := range parameters {
					parameterName, _ := parameter["name"].(string)
					if parameter["in"] == location && strings.EqualFold(parameterName, name) {
						return parameter
					}
				}
				return nil
			}
			if strings.HasPrefix(path, "/v1/messages") {
				if parameter := findParameter("header", "Anthropic-Version"); parameter == nil {
					t.Fatal("Anthropic native endpoints must document the Anthropic-Version header")
				}
			}
			if strings.HasPrefix(path, "/v1beta/") {
				if parameter := findParameter("path", "model"); parameter == nil || parameter["required"] != true {
					t.Fatal("Gemini endpoints must document the required model path parameter")
				}
			} else if findParameter("query", "key") != nil {
				t.Fatal("query key compatibility is confined to Gemini")
			}
			if strings.HasSuffix(path, ":streamGenerateContent") {
				parameter := findParameter("query", "alt")
				if parameter == nil {
					t.Fatal("Gemini streaming must document the alt=sse query parameter")
				}
				if values, ok := object(t, parameter["schema"])["enum"].([]any); !ok || len(values) != 1 || values[0] != "sse" {
					t.Fatal("Gemini alt must identify the SSE media mode")
				}
			}
			if method == "POST" {
				body := c.dereference(t, op["requestBody"])
				if body["required"] != true {
					t.Fatal("native request body must be required")
				}
				content := object(t, body["content"])
				media := "application/json"
				if path == "/v1/images/edits" {
					media = "multipart/form-data"
				}
				if _, exists := content[media]; !exists {
					t.Fatalf("request has no %s schema", media)
				}
			}
			responses := object(t, op["responses"])
			if _, exists := responses["200"]; !exists {
				t.Fatal("success response is missing")
			}
			if path != "/nomifun/v1/meta" {
				for _, status := range []string{"400", "401", "402", "403", "429"} {
					if _, exists := responses[status]; !exists {
						if _, fallback := responses["default"]; !fallback {
							t.Errorf("missing native error response for %s", status)
						}
					}
				}
			}
			for status, value := range responses {
				response := c.dereference(t, value)
				if text, ok := response["description"].(string); !ok || text == "" {
					t.Errorf("%s has no response description", status)
				}
				headers := object(t, response["headers"])
				if !hasHeader(headers, "X-Request-ID") {
					t.Errorf("%s does not document X-Request-ID", status)
				}
				if !hasHeader(headers, "Cache-Control") {
					t.Errorf("%s does not document Cache-Control", status)
				}
				if status == "429" && !hasHeader(headers, "Retry-After") {
					t.Error("429 must document Retry-After")
				}
				if status != "200" {
					if _, exists := object(t, response["content"])["application/json"]; !exists {
						t.Errorf("%s must document native JSON errors", status)
					}
				}
			}
		})
	}
	// Resolve every document ref, including non-schema parameters/responses.
	// Compile every component and every request/response/header/parameter schema.
	var walk func(any, string)
	walk = func(value any, pointer string) {
		switch current := value.(type) {
		case map[string]any:
			if ref, exists := current["$ref"]; exists {
				text, ok := ref.(string)
				if !ok {
					t.Fatalf("non-string ref at %s", pointer)
				}
				c.resolve(t, text)
			}
			for key, child := range current {
				childPointer := pointer + "/" + escapePointer(key)
				if key == "schema" || key == "x-sse-event-schema" || key == "x-generated-error-schema" {
					c.schema(t, childPointer)
				}
				walk(child, childPointer)
			}
		case []any:
			for index, child := range current {
				walk(child, pointer+"/"+strconv.Itoa(index))
			}
		}
	}
	walk(c.doc, "#")
	for name := range object(t, object(t, c.doc["components"])["schemas"]) {
		c.schema(t, "#/components/schemas/"+escapePointer(name))
	}
}

func hasHeader(headers map[string]any, name string) bool {
	for key := range headers {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

func TestOpenAPIControlFixtures(t *testing.T) {
	c := loadOpenAPI(t)
	for _, fixture := range []struct {
		name  string
		value any
	}{
		{"Meta", v1.MockMeta()}, {"Catalog", v1.MockCatalog(false)}, {"Catalog", v1.MockCatalog(true)}, {"Account", v1.MockAccount()},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			if err := c.schema(t, "#/components/schemas/"+fixture.name).Validate(jsonValue(t, fixture.value)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// These cases mutate untyped JSON, never call Go struct validation. They prove
// the published schema itself enforces the desktop's fail-closed constraints.
func TestOpenAPICatalogRejectsInvalidWireValues(t *testing.T) {
	c := loadOpenAPI(t)
	schema := c.schema(t, "#/components/schemas/Catalog")
	type modelMutation func(map[string]any)
	cases := []struct {
		name   string
		mutate modelMutation
	}{
		{"missing_id", func(m map[string]any) { delete(m, "id") }},
		{"missing_tasks", func(m map[string]any) { delete(m, "tasks") }},
		{"unknown_task", func(m map[string]any) { m["tasks"] = []any{"future-task"} }},
		{"duplicate_task", func(m map[string]any) { m["tasks"] = []any{"chat", "chat"} }},
		{"missing_task_endpoints", func(m map[string]any) { delete(m, "task_endpoints") }},
		{"missing_advertised_task", func(m map[string]any) { m["task_endpoints"] = map[string]any{} }},
		{"unadvertised_task", func(m map[string]any) { m["tasks"] = []any{"embedding"} }},
		{"missing_endpoints", func(m map[string]any) { delete(chatTask(t, m), "endpoints") }},
		{"empty_endpoints", func(m map[string]any) { chatTask(t, m)["endpoints"] = []any{} }},
		{"missing_preferred_endpoint", func(m map[string]any) { delete(chatTask(t, m), "preferred_endpoint") }},
		{"unknown_preferred_endpoint", func(m map[string]any) { chatTask(t, m)["preferred_endpoint"] = "guess" }},
		{"preferred_not_advertised", func(m map[string]any) { chatTask(t, m)["preferred_endpoint"] = "openai" }},
		{"wrong_endpoint_task_family", func(m map[string]any) {
			chatTask(t, m)["endpoints"] = []any{"embeddings"}
			chatTask(t, m)["preferred_endpoint"] = "embeddings"
		}},
		{"anthropic_output_missing", func(m map[string]any) { delete(m, "max_output_tokens") }},
		{"anthropic_output_null", func(m map[string]any) { m["max_output_tokens"] = nil }},
		{"anthropic_output_zero", func(m map[string]any) { m["max_output_tokens"] = json.Number("0") }},
		{"anthropic_output_negative", func(m map[string]any) { m["max_output_tokens"] = json.Number("-1") }},
		{"anthropic_output_fractional", func(m map[string]any) { m["max_output_tokens"] = json.Number("4096.5") }},
		{"anthropic_output_int64_overflow", func(m map[string]any) { m["max_output_tokens"] = json.Number("9223372036854775808") }},
		{"price_fractional", func(m map[string]any) { firstPrice(t, m)["amount"] = json.Number("0.01") }},
		{"price_negative", func(m map[string]any) { firstPrice(t, m)["amount"] = json.Number("-1") }},
		{"price_int64_overflow", func(m map[string]any) { firstPrice(t, m)["amount"] = json.Number("9223372036854775808") }},
		{"price_unit_zero", func(m map[string]any) { firstPrice(t, m)["unit_size"] = json.Number("0") }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog := object(t, jsonValue(t, v1.MockCatalog(false)))
			model := object(t, catalog["models"].([]any)[1]) // anthropic preferred
			test.mutate(model)
			if err := schema.Validate(catalog); err == nil {
				t.Fatal("published schema accepted invalid wire values")
			}
		})
	}
	for _, limit := range []string{"1", "9223372036854775807"} {
		t.Run("valid_output_"+limit, func(t *testing.T) {
			catalog := object(t, jsonValue(t, v1.MockCatalog(false)))
			object(t, catalog["models"].([]any)[1])["max_output_tokens"] = json.Number(limit)
			if err := schema.Validate(catalog); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func chatTask(t *testing.T, model map[string]any) map[string]any {
	t.Helper()
	return object(t, object(t, model["task_endpoints"])["chat"])
}

func firstPrice(t *testing.T, model map[string]any) map[string]any {
	t.Helper()
	return object(t, model["pricing"].([]any)[0])
}

func TestOpenAPIControlRequiredAndNumericFields(t *testing.T) {
	c := loadOpenAPI(t)
	fixtures := map[string]any{"Meta": v1.MockMeta(), "Catalog": v1.MockCatalog(false), "Account": v1.MockAccount()}
	for name, fixture := range fixtures {
		for field := range object(t, jsonValue(t, fixture)) {
			t.Run(name+"/missing_"+field, func(t *testing.T) {
				value := object(t, jsonValue(t, fixture))
				delete(value, field)
				if err := c.schema(t, "#/components/schemas/"+name).Validate(value); err == nil {
					t.Fatal("accepted missing required field")
				}
			})
		}
		t.Run(name+"/wrong_contract_version", func(t *testing.T) {
			value := object(t, jsonValue(t, fixture))
			value["contract_version"] = "2.0"
			if err := c.schema(t, "#/components/schemas/"+name).Validate(value); err == nil {
				t.Fatal("accepted incompatible contract version")
			}
		})
	}
	for _, amount := range []string{"1.5", "9223372036854775808", "-9223372036854775809"} {
		t.Run("Account/invalid_balance_"+amount, func(t *testing.T) {
			value := object(t, jsonValue(t, v1.MockAccount()))
			object(t, value["balance"])["amount"] = json.Number(amount)
			if err := c.schema(t, "#/components/schemas/Account").Validate(value); err == nil {
				t.Fatal("accepted non-int64 monetary amount")
			}
		})
	}
	t.Run("Meta/insecure_purchase_url", func(t *testing.T) {
		value := object(t, jsonValue(t, v1.MockMeta()))
		object(t, value["operator"])["purchase_url"] = "http://operator.example/purchase"
		if err := c.schema(t, "#/components/schemas/Meta").Validate(value); err == nil {
			t.Fatal("accepted non-HTTPS purchase link")
		}
	})
}

func TestOpenAPIValidatesActualMockResponses(t *testing.T) {
	c := loadOpenAPI(t)
	server := httptest.NewServer(mock.New(mock.Config{}))
	defer server.Close()
	paths := make([]string, 0, len(frozenPaths))
	for path := range frozenPaths {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			validateMockResponse(t, c, server, path, false, mock.DefaultAPIKey, http.StatusOK)
		})
	}
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
		t.Run(path+"/stream", func(t *testing.T) {
			validateMockResponse(t, c, server, path, true, mock.DefaultAPIKey, http.StatusOK)
		})
	}
	// Exercise native error envelopes, Gemini's integer error.code, actionable
	// billing metadata, and 429 headers through the server, not fabricated JSON.
	for _, path := range []string{"/nomifun/v1/account", "/v1/messages", "/v1beta/models/{model}:generateContent"} {
		for _, fixture := range []struct {
			key    string
			status int
		}{
			{"invalid_mock_key", 401}, {mock.InsufficientBalanceAPIKey, 402}, {mock.SubscriptionExpiredAPIKey, 403},
			{mock.ModelNotInPlanAPIKey, 403}, {mock.KeyExpiredAPIKey, 401}, {mock.RateLimitedAPIKey, 429},
		} {
			t.Run(path+"/error_"+fixture.key, func(t *testing.T) {
				validateMockResponse(t, c, server, path, false, fixture.key, fixture.status)
			})
		}
	}
}

func validateMockResponse(t *testing.T, c *openAPIContract, server *httptest.Server, path string, stream bool, key string, wantStatus int) {
	t.Helper()
	body, contentType := mockRequest(t, path, stream)
	actualPath := strings.ReplaceAll(path, "{model}", "mock-gemini")
	if strings.HasSuffix(path, ":streamGenerateContent") {
		actualPath += "?alt=sse"
	}
	request, err := http.NewRequest(frozenPaths[path], server.URL+actualPath, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Anthropic-Version", "2023-06-01")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("HTTP %d, want %d: %s", response.StatusCode, wantStatus, data)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	media := c.responseMedia(t, path, frozenPaths[path], response.StatusCode, mediaType)
	var value any
	if mediaType == "text/event-stream" {
		value = string(data)
	} else {
		value, err = jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := c.mediaSchema(t, media, "schema").Validate(value); err != nil {
		t.Fatalf("actual HTTP response does not match openapi.yaml: %v", err)
	}
	if response.StatusCode >= 400 {
		if _, exists := media.document["x-generated-error-schema"]; exists {
			if err := c.mediaSchema(t, media, "x-generated-error-schema").Validate(value); err != nil {
				t.Fatalf("mock-generated error violates the gateway extension schema: %v", err)
			}
		}
	}
	if response.Header.Get("X-Request-ID") == "" {
		t.Fatal("actual response has no X-Request-ID")
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("response must be no-store")
	}
	if response.StatusCode == 429 && response.Header.Get("Retry-After") == "" {
		t.Fatal("rate limit error has no Retry-After")
	}
	if mediaType == "text/event-stream" {
		eventSchema := c.mediaSchema(t, media, "x-sse-event-schema")
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 4096), 1<<20)
		events := 0
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "[DONE]" {
				continue
			}
			event, err := jsonschema.UnmarshalJSON(strings.NewReader(payload))
			if err != nil {
				t.Fatalf("invalid SSE JSON: %v", err)
			}
			if err := eventSchema.Validate(event); err != nil {
				t.Fatalf("SSE event violates published event schema: %v", err)
			}
			events++
		}
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
		if events == 0 {
			t.Fatal("stream has no schema-validated JSON events")
		}
	}
}

func mockRequest(t *testing.T, path string, stream bool) ([]byte, string) {
	t.Helper()
	if frozenPaths[path] == "GET" {
		return nil, ""
	}
	if path == "/v1/images/edits" {
		var buffer bytes.Buffer
		writer := multipart.NewWriter(&buffer)
		if err := writer.WriteField("model", "mock-image"); err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteField("prompt", "Synthetic fixture"); err != nil {
			t.Fatal(err)
		}
		file, err := writer.CreateFormFile("image", "fixture.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte("synthetic mock image")); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return buffer.Bytes(), writer.FormDataContentType()
	}
	var body map[string]any
	switch path {
	case "/v1/chat/completions":
		body = map[string]any{"model": "mock-compatible", "messages": []any{map[string]any{"role": "user", "content": "Synthetic fixture"}}, "stream": stream}
	case "/v1/responses":
		body = map[string]any{"model": "mock-gpt", "input": "Synthetic fixture", "stream": stream, "store": false}
	case "/v1/messages", "/v1/messages/count_tokens":
		body = map[string]any{"model": "mock-claude", "messages": []any{map[string]any{"role": "user", "content": "Synthetic fixture"}}, "max_tokens": 128, "stream": stream}
	case "/v1beta/models/{model}:generateContent", "/v1beta/models/{model}:streamGenerateContent":
		body = map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Synthetic fixture"}}}}}
	case "/v1/images/generations":
		body = map[string]any{"model": "mock-image", "prompt": "Synthetic fixture"}
	case "/v1/embeddings":
		body = map[string]any{"model": "mock-embedding", "input": "Synthetic fixture"}
	case "/v1/rerank":
		body = map[string]any{"model": "mock-rerank", "query": "Synthetic", "documents": []string{"Synthetic fixture", "Other fixture"}}
	default:
		t.Fatalf("no mock request for %s", path)
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return data, "application/json"
}
