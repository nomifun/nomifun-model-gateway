// SPDX-License-Identifier: Apache-2.0
package v1

// MaxTokenLimit is the largest exact integer accepted by Desktop's model
// configuration renderer. Monetary values retain their separate int64 contract.
const MaxTokenLimit int64 = 9_007_199_254_740_991

// ProtocolManifest describes implemented gateway interfaces, not the tasks or
// health of any particular upstream model. The frozen catalog remains v1.0.
type ProtocolManifest struct {
	ContractVersion       string               `json:"contract_version"`
	ManifestVersion       string               `json:"manifest_version"`
	Protocols             []ProtocolDescriptor `json:"protocols"`
	UnsupportedTasks      []string             `json:"unsupported_tasks"`
	CatalogTraits         []string             `json:"catalog_traits"`
	TechnicalCapabilities []string             `json:"technical_capabilities"`
	TraitsAreAdvisory     bool                 `json:"traits_are_advisory"`
	MaxTokenLimit         int64                `json:"max_token_limit"`
}

// ProtocolDescriptor binds a v1 endpoint family to an explicit Desktop
// invocation protocol and connection. Roots and endpoint templates are paired:
// versioned_root puts /v1 on the connection, origin_root puts it on the path.
type ProtocolDescriptor struct {
	ProtocolID            string               `json:"protocol_id"`
	Endpoint              Endpoint             `json:"endpoint"`
	SupportedTasks        []Task               `json:"supported_tasks"`
	Executor              string               `json:"executor"`
	Transport             string               `json:"transport"`
	ConnectionRole        string               `json:"connection_role"`
	AuthScheme            string               `json:"auth_scheme"`
	RootShape             string               `json:"root_shape"`
	BaseURLSuffix         string               `json:"base_url_suffix"`
	RequiresOutputCeiling bool                 `json:"requires_output_ceiling"`
	RequiresStreamUsage   bool                 `json:"requires_stream_usage"`
	Endpoints             []EndpointDescriptor `json:"endpoints"`
}

// EndpointDescriptor describes one native operation. The stream purpose is a
// distinct URL only for Gemini; other Chat protocols select streaming in JSON.
type EndpointDescriptor struct {
	Task         Task   `json:"task"`
	Field        string `json:"field"`
	Purpose      string `json:"purpose"`
	Method       string `json:"method"`
	DefaultValue string `json:"default_value"`
	RootShape    string `json:"root_shape"`
}

var protocolRegistry = []ProtocolDescriptor{
	{
		ProtocolID: "openai.chat_text", Endpoint: OpenAI, SupportedTasks: []Task{Chat},
		Executor: "agent", Transport: "http", ConnectionRole: "default", AuthScheme: "bearer",
		RootShape: "versioned_root", BaseURLSuffix: "/v1", RequiresOutputCeiling: true, RequiresStreamUsage: true,
		Endpoints: []EndpointDescriptor{{Task: Chat, Field: "endpoint", Purpose: "submit", Method: "POST", DefaultValue: "/chat/completions", RootShape: "versioned_root"}},
	},
	{
		ProtocolID: "openai.responses", Endpoint: Responses, SupportedTasks: []Task{Chat},
		Executor: "agent", Transport: "http", ConnectionRole: "default", AuthScheme: "bearer",
		RootShape: "versioned_root", BaseURLSuffix: "/v1", RequiresOutputCeiling: true,
		Endpoints: []EndpointDescriptor{{Task: Chat, Field: "endpoint", Purpose: "submit", Method: "POST", DefaultValue: "/responses", RootShape: "versioned_root"}},
	},
	{
		ProtocolID: "anthropic.messages", Endpoint: Anthropic, SupportedTasks: []Task{Chat},
		Executor: "agent", Transport: "http", ConnectionRole: "anthropic", AuthScheme: "header_key:x-api-key",
		RootShape: "origin_root", RequiresOutputCeiling: true,
		Endpoints: []EndpointDescriptor{{Task: Chat, Field: "endpoint", Purpose: "submit", Method: "POST", DefaultValue: "/v1/messages", RootShape: "origin_root"}},
	},
	{
		ProtocolID: "gemini.generate_text", Endpoint: Gemini, SupportedTasks: []Task{Chat},
		Executor: "agent", Transport: "http", ConnectionRole: "gemini", AuthScheme: "header_key:x-goog-api-key",
		RootShape: "origin_root", RequiresOutputCeiling: true,
		Endpoints: []EndpointDescriptor{
			{Task: Chat, Field: "endpoint", Purpose: "submit", Method: "POST", DefaultValue: "/v1beta/models/{model}:generateContent", RootShape: "origin_root"},
			{Task: Chat, Field: "endpoint", Purpose: "stream", Method: "POST", DefaultValue: "/v1beta/models/{model}:streamGenerateContent", RootShape: "origin_root"},
		},
	},
	{
		ProtocolID: "openai.images", Endpoint: Images, SupportedTasks: []Task{ImageGeneration, ImageEdit},
		Executor: "model_invoke", Transport: "http", ConnectionRole: "default", AuthScheme: "bearer",
		RootShape: "versioned_root", BaseURLSuffix: "/v1",
		Endpoints: []EndpointDescriptor{
			{Task: ImageGeneration, Field: "endpoint", Purpose: "submit", Method: "POST", DefaultValue: "/images/generations", RootShape: "versioned_root"},
			{Task: ImageEdit, Field: "endpoint", Purpose: "submit", Method: "POST", DefaultValue: "/images/edits", RootShape: "versioned_root"},
		},
	},
	{
		ProtocolID: "openai.embeddings", Endpoint: Embeddings, SupportedTasks: []Task{Embedding},
		Executor: "model_invoke", Transport: "http", ConnectionRole: "default", AuthScheme: "bearer",
		RootShape: "versioned_root", BaseURLSuffix: "/v1",
		Endpoints: []EndpointDescriptor{{Task: Embedding, Field: "endpoint", Purpose: "submit", Method: "POST", DefaultValue: "/embeddings", RootShape: "versioned_root"}},
	},
	{
		ProtocolID: "generic.rerank", Endpoint: Rerank, SupportedTasks: []Task{Reranking},
		Executor: "model_invoke", Transport: "http", ConnectionRole: "default", AuthScheme: "bearer",
		RootShape: "versioned_root", BaseURLSuffix: "/v1",
		Endpoints: []EndpointDescriptor{{Task: Reranking, Field: "endpoint", Purpose: "submit", Method: "POST", DefaultValue: "/rerank", RootShape: "versioned_root"}},
	},
}

// Protocols returns independent descriptors in the stable v1 endpoint order.
// Callers may prepare a filtered response without changing the registry.
func Protocols() []ProtocolDescriptor {
	result := make([]ProtocolDescriptor, len(protocolRegistry))
	for index, descriptor := range protocolRegistry {
		result[index] = descriptor
		result[index].SupportedTasks = append([]Task(nil), descriptor.SupportedTasks...)
		result[index].Endpoints = append([]EndpointDescriptor(nil), descriptor.Endpoints...)
	}
	return result
}

// Endpoints returns every implemented endpoint family in its stable wire order.
func Endpoints() []Endpoint {
	result := make([]Endpoint, len(protocolRegistry))
	for index, descriptor := range protocolRegistry {
		result[index] = descriptor.Endpoint
	}
	return result
}

func KnownEndpoint(endpoint Endpoint) bool {
	for _, descriptor := range protocolRegistry {
		if descriptor.Endpoint == endpoint {
			return true
		}
	}
	return false
}

// SupportsTaskEndpoint uses the same task declarations as the public manifest.
// Unknown tasks and endpoints never acquire a protocol fallback.
func SupportsTaskEndpoint(task Task, endpoint Endpoint) bool {
	for _, descriptor := range protocolRegistry {
		if descriptor.Endpoint != endpoint {
			continue
		}
		for _, supported := range descriptor.SupportedTasks {
			if task == supported {
				return true
			}
		}
		return false
	}
	return false
}

// Manifest publishes only instance-enabled endpoint families. Configuration
// does not prove model availability or successful native tool/stream calls.
func Manifest(capabilities []Endpoint) ProtocolManifest {
	allowed := make(map[Endpoint]bool, len(capabilities))
	for _, endpoint := range capabilities {
		allowed[endpoint] = true
	}
	protocols := []ProtocolDescriptor{}
	for _, descriptor := range Protocols() {
		if allowed[descriptor.Endpoint] {
			protocols = append(protocols, descriptor)
		}
	}
	return ProtocolManifest{
		ContractVersion: Version, ManifestVersion: "1", Protocols: protocols,
		UnsupportedTasks:      []string{"realtime_conversation", "video_generation", "music_generation", "speech_synthesis", "speech_recognition"},
		CatalogTraits:         []string{"vision_input", "audio_input", "video_input", "web_search"},
		TechnicalCapabilities: []string{"function_calling", "reasoning", "streaming"},
		TraitsAreAdvisory:     true, MaxTokenLimit: MaxTokenLimit,
	}
}
