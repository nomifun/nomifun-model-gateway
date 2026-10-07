// SPDX-License-Identifier: Apache-2.0
package v1

func ptr[T any](v T) *T { return &v }

// MockMeta returns fictional operator data, never an official hosted service.
func MockMeta() Meta {
	return Meta{Version, Operator{"Example Community Operator (Mock)", ptr("https://operator.example/"), ptr("https://operator.example/console"), ptr("https://operator.example/purchase"), ptr("https://operator.example/terms"), ptr("https://operator.example/privacy")}, []Endpoint{OpenAI, Responses, Anthropic, Gemini, Images, Embeddings, Rerank}, []string{"/v1/messages/count_tokens"}}
}

func MockCatalog(limited bool) Catalog {
	model := func(id, vendor string, tasks map[Task]TaskEndpoints, context, output *int64, modalities []string, traits []string) Model {
		m := Model{ID: id, DisplayName: id, Vendor: vendor, Tasks: []Task{}, TaskEndpoints: tasks, ContextWindow: context, MaxOutputTokens: output, InputModalities: modalities, Traits: traits, Pricing: []Price{}, IncludedInPlan: true, Status: "available"}
		for _, task := range []Task{Chat, ImageGeneration, ImageEdit, Embedding, Reranking} {
			if _, ok := tasks[task]; ok {
				m.Tasks = append(m.Tasks, task)
			}
		}
		for _, task := range m.Tasks {
			m.Pricing = append(m.Pricing, Price{task, "requests", 1, 0, "USD"})
		}
		return m
	}
	capability := func(preferred Endpoint, endpoints ...Endpoint) TaskEndpoints {
		return TaskEndpoints{endpoints, preferred}
	}
	models := []Model{
		model("mock-gpt", "openai", map[Task]TaskEndpoints{Chat: capability(Responses, Responses, OpenAI)}, ptr(int64(128000)), ptr(int64(8192)), []string{"text", "image"}, []string{"vision_input"}),
		model("mock-claude", "anthropic", map[Task]TaskEndpoints{Chat: capability(Anthropic, Anthropic)}, ptr(int64(200000)), ptr(int64(4096)), []string{"text", "image"}, []string{"vision_input"}),
		model("mock-gemini", "google", map[Task]TaskEndpoints{Chat: capability(Gemini, Gemini)}, ptr(int64(1000000)), ptr(int64(8192)), []string{"text", "image", "audio", "video"}, []string{"vision_input", "audio_input", "video_input"}),
		model("mock-compatible", "example", map[Task]TaskEndpoints{Chat: capability(OpenAI, OpenAI)}, ptr(int64(32768)), ptr(int64(4096)), []string{"text"}, []string{}),
		model("mock-image", "example", map[Task]TaskEndpoints{ImageGeneration: capability(Images, Images), ImageEdit: capability(Images, Images)}, nil, nil, []string{"text", "image"}, []string{}),
		model("mock-embedding", "example", map[Task]TaskEndpoints{Embedding: capability(Embeddings, Embeddings)}, ptr(int64(8192)), nil, []string{"text"}, []string{}),
		model("mock-rerank", "example", map[Task]TaskEndpoints{Reranking: capability(Rerank, Rerank)}, ptr(int64(8192)), nil, []string{"text"}, []string{}),
	}
	if limited {
		models = models[3:4]
	}
	return Catalog{Version, models}
}

func MockAccount() Account {
	return Account{Version, &Plan{"Mock Development", ptr("2026-10-01T00:00:00Z"), ptr("2099-11-01T00:00:00Z"), Quota{"tokens", ptr(int64(1000000)), 0}}, Balance{12345, "USD"}, Key{"Mock development key", nil, "tokens", ptr(int64(1000000))}, RateLimits{ptr(int64(60)), ptr(int64(100000)), ptr(int64(4))}}
}
