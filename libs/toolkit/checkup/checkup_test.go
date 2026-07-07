package checkup

import (
	"encoding/json"
	"testing"
)

func TestResultsOK(t *testing.T) {
	tests := []struct {
		name    string
		results Results
		wantOK  bool
	}{
		{
			name:    "empty",
			results: Results{},
			wantOK:  true,
		},
		{
			name: "all ok",
			results: Results{
				{Component: "a", Status: StatusOK},
				{Component: "b", Status: StatusOK},
			},
			wantOK: true,
		},
		{
			name: "one error",
			results: Results{
				{Component: "a", Status: StatusOK},
				{Component: "b", Status: StatusError},
			},
			wantOK: false,
		},
		{
			name: "mixed limited and ok",
			results: Results{
				{Component: "a", Status: StatusLimited},
				{Component: "b", Status: StatusOK},
			},
			wantOK: true,
		},
		{
			name: "all error",
			results: Results{
				{Component: "a", Status: StatusError, Error: "fail"},
			},
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.results.OK(); got != tt.wantOK {
				t.Errorf("OK() = %v, want %v", got, tt.wantOK)
			}
		})
	}
}

func TestResultsJSON(t *testing.T) {
	r := Results{
		{Component: "test_list", Instance: "prod", Status: StatusOK, Message: "works"},
		{Component: "test_describe", Instance: "prod", Status: StatusLimited, Message: "no resources"},
	}
	compact := r.JSON("")
	indented := r.JSON("  ")

	var parsed Results
	if err := json.Unmarshal([]byte(compact), &parsed); err != nil {
		t.Fatalf("compact JSON is not valid: %v", err)
	}
	if len(parsed) != 2 {
		t.Fatalf("expected 2 results, got %d", len(parsed))
	}
	if parsed[0].Component != "test_list" || parsed[0].Status != StatusOK {
		t.Errorf("unexpected compact result[0]: %+v", parsed[0])
	}

	var parsedIndent Results
	if err := json.Unmarshal([]byte(indented), &parsedIndent); err != nil {
		t.Fatalf("indented JSON is not valid: %v", err)
	}
	if len(parsedIndent) != 2 {
		t.Fatalf("expected 2 indented results, got %d", len(parsedIndent))
	}
}

func TestMerge(t *testing.T) {
	a := Results{{Component: "a", Status: StatusOK}}
	b := Results{{Component: "b", Status: StatusOK}}
	c := Results{{Component: "c", Status: StatusError, Error: "fail"}}

	merged := Merge(a, b, c)
	if len(merged) != 3 {
		t.Fatalf("expected 3 results, got %d", len(merged))
	}
	if merged[0].Component != "a" {
		t.Errorf("first component: %s", merged[0].Component)
	}
	if merged[2].Component != "c" {
		t.Errorf("third component: %s", merged[2].Component)
	}
}

func TestMergeNil(t *testing.T) {
	merged := Merge(nil, Results{{Component: "x", Status: StatusOK}}, nil)
	if len(merged) != 1 {
		t.Fatalf("expected 1 result, got %d", len(merged))
	}
}

func TestDependencyFailed(t *testing.T) {
	r := DependencyFailed("a", "b", "c")
	if len(r) != 3 {
		t.Fatalf("expected 3 results, got %d", len(r))
	}
	for i, rr := range r {
		if rr.Status != StatusError {
			t.Errorf("result %d: expected status error, got %q", i, rr.Status)
		}
		if rr.Error != "dependency failed" {
			t.Errorf("result %d: expected error 'dependency failed', got %q", i, rr.Error)
		}
	}
}
