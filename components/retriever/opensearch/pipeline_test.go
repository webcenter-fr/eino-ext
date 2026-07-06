package opensearch

import (
	"encoding/json"
	"testing"
)

func TestDefaultRRFPipelineBody_ValidJSON(t *testing.T) {
	var v any
	if err := json.Unmarshal([]byte(DefaultRRFPipelineBody), &v); err != nil {
		t.Fatalf("DefaultRRFPipelineBody is not valid JSON: %v", err)
	}
}

func TestDefaultRRFPipelineBody_Structure(t *testing.T) {
	var v map[string]any
	if err := json.Unmarshal([]byte(DefaultRRFPipelineBody), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	desc, ok := v["description"].(string)
	if !ok {
		t.Fatal("missing description field")
	}
	if desc == "" {
		t.Error("description should not be empty")
	}

	processors, ok := v["phase_results_processors"].([]any)
	if !ok || len(processors) == 0 {
		t.Fatal("missing or empty phase_results_processors")
	}

	p0, ok := processors[0].(map[string]any)
	if !ok {
		t.Fatal("first processor is not an object")
	}

	ranker, ok := p0["score-ranker-processor"].(map[string]any)
	if !ok {
		t.Fatal("missing score-ranker-processor")
	}

	comb, ok := ranker["combination"].(map[string]any)
	if !ok {
		t.Fatal("missing combination")
	}

	tech, ok := comb["technique"].(string)
	if !ok || tech != "rrf" {
		t.Fatalf("expected technique rrf, got %q", tech)
	}
}
