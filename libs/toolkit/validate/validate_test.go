package validate

import (
	"strings"
	"testing"
)

// podLogParams mirrors components/tool/kubernetes/pod_log.go PodLogParams so
// the regression that motivated this helper (maxLines > 500 failing the
// 'max' tag with an opaque message) is covered directly.
type podLogParams struct {
	Cluster       string `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
	Namespace     string `json:"namespace" validate:"required" jsonschema:"(required) The namespace of the pod."`
	Name          string `json:"name" validate:"required" jsonschema:"(required) The pod name."`
	Container     string `json:"container,omitempty" validate:"omitempty" jsonschema:"(optional) The container name."`
	MaxLines      int64  `json:"maxLines,omitempty" validate:"omitempty,min=1,max=500" jsonschema:"(optional) The maximum number of log lines to return. Default to 100."`
	FilterPattern string `json:"filterPattern,omitempty" validate:"omitempty" jsonschema:"(optional) A Go RE2 regex."`
}

func TestStruct_OK(t *testing.T) {
	p := &podLogParams{Cluster: "c", Namespace: "ns", Name: "pod", MaxLines: 500}
	if err := Struct(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStruct_DefaultZeroMaxLines(t *testing.T) {
	// MaxLines == 0 is valid (omitempty skips min/max); the tool applies the
	// default of 100 afterwards, validate must not reject it.
	p := &podLogParams{Cluster: "c", Namespace: "ns", Name: "pod"}
	if err := Struct(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStruct_MaxLinesTooLarge(t *testing.T) {
	p := &podLogParams{Cluster: "c", Namespace: "ns", Name: "pod", MaxLines: 1000}
	err := Struct(p)
	if err == nil {
		t.Fatal("expected error for maxLines > 500, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "maxLines") {
		t.Fatalf("error should mention the JSON parameter name 'maxLines', got: %s", msg)
	}
	if !strings.Contains(msg, "<=") {
		t.Fatalf("error should state the 'must be <=' constraint, got: %s", msg)
	}
	if !strings.Contains(msg, "500") {
		t.Fatalf("error should state the max bound 500, got: %s", msg)
	}
	if !strings.Contains(msg, "1000") {
		t.Fatalf("error should show the provided value 1000, got: %s", msg)
	}
	if !strings.Contains(msg, "reduce it and retry") {
		t.Fatalf("error should prescribe a remediation, got: %s", msg)
	}
}

func TestStruct_MaxLinesTooSmall(t *testing.T) {
	p := &podLogParams{Cluster: "c", Namespace: "ns", Name: "pod", MaxLines: -1}
	err := Struct(p)
	if err == nil {
		t.Fatal("expected error for maxLines < 1, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "maxLines") || !strings.Contains(msg, ">=") || !strings.Contains(msg, "increase it and retry") {
		t.Fatalf("expected a 'must be >=' prescription for maxLines, got: %s", msg)
	}
}

func TestStruct_RequiredFields(t *testing.T) {
	p := &podLogParams{}
	err := Struct(p)
	if err == nil {
		t.Fatal("expected error for missing required fields, got nil")
	}
	msg := err.Error()
	for _, field := range []string{"cluster", "namespace", "name"} {
		if !strings.Contains(msg, "'"+field+"'") {
			t.Fatalf("error should mention required field '%s', got: %s", field, msg)
		}
	}
	if !strings.Contains(msg, "is required") {
		t.Fatalf("error should say field is required, got: %s", msg)
	}
	// Should use JSON names, not Go field names.
	for _, goName := range []string{"'Cluster'", "'Namespace'", "'Name'"} {
		if strings.Contains(msg, goName) {
			t.Fatalf("error should not use Go field name %s, got: %s", goName, msg)
		}
	}
}

type oneofParams struct {
	Health  string `json:"health,omitempty" validate:"omitempty,oneof=up down unknown"`
	Refresh string `json:"refresh,omitempty" validate:"omitempty,oneof=alpha_asc alpha_desc"`
}

func TestStruct_Oneof(t *testing.T) {
	p := &oneofParams{Health: "foo"}
	err := Struct(p)
	if err == nil {
		t.Fatal("expected error for invalid oneof value, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "health") || !strings.Contains(msg, "one of") {
		t.Fatalf("expected a 'must be one of' message, got: %s", msg)
	}
	for _, allowed := range []string{"up", "down", "unknown"} {
		if !strings.Contains(msg, allowed) {
			t.Fatalf("error should list allowed value %s, got: %s", allowed, msg)
		}
	}
	if !strings.Contains(msg, `"foo"`) {
		t.Fatalf("error should quote the offending value, got: %s", msg)
	}
}

type lengthParams struct {
	Tags       []string `json:"tags,omitempty" validate:"omitempty,max=64"`
	FolderUIDs []string `json:"folderUIDs,omitempty" validate:"omitempty,min=1"`
	Query      string   `json:"query,omitempty" validate:"omitempty,max=10"`
}

func TestStruct_LengthMaxSlice(t *testing.T) {
	tags := make([]string, 65)
	for i := range tags {
		tags[i] = "t"
	}
	p := &lengthParams{Tags: tags}
	err := Struct(p)
	if err == nil {
		t.Fatal("expected error for too many tags, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "tags") || !strings.Contains(msg, "at most") || !strings.Contains(msg, "64") {
		t.Fatalf("expected an 'at most 64' length message, got: %s", msg)
	}
}

func TestStruct_LengthMinSlice(t *testing.T) {
	p := &lengthParams{FolderUIDs: []string{}}
	err := Struct(p)
	if err == nil {
		t.Fatal("expected error for empty FolderUIDs with min=1, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "folderUIDs") || !strings.Contains(msg, "at least") || !strings.Contains(msg, "1") {
		t.Fatalf("expected an 'at least 1' length message, got: %s", msg)
	}
}

func TestStruct_LengthMaxString(t *testing.T) {
	p := &lengthParams{Query: "12345678901"} // 11 chars, max=10
	err := Struct(p)
	if err == nil {
		t.Fatal("expected error for too-long query string, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "query") || !strings.Contains(msg, "at most") || !strings.Contains(msg, "10") {
		t.Fatalf("expected an 'at most 10' length message for string, got: %s", msg)
	}
}

type excludeParams struct {
	ExcludeFieldsOutput []string `json:"excludeFieldsOutput,omitempty" validate:"omitempty,dive,oneof=metadata spec status data"`
}

func TestStruct_DiveOneof(t *testing.T) {
	p := &excludeParams{ExcludeFieldsOutput: []string{"metadata", "nope"}}
	err := Struct(p)
	if err == nil {
		t.Fatal("expected error for invalid dive/oneof element, got nil")
	}
	msg := err.Error()
	// The offending element is reported with its index, pointing the LLM at the
	// exact slot to fix.
	if !strings.Contains(msg, "excludeFieldsOutput[1]") {
		t.Fatalf("error should point at the offending element 'excludeFieldsOutput[1]', got: %s", msg)
	}
	if !strings.Contains(msg, "one of") || !strings.Contains(msg, "metadata") {
		t.Fatalf("error should list allowed values, got: %s", msg)
	}
}

type urlParams struct {
	URL string `json:"url" validate:"required,url"`
}

func TestStruct_URL(t *testing.T) {
	p := &urlParams{URL: "not a url"}
	err := Struct(p)
	if err == nil {
		t.Fatal("expected error for invalid url, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "url") || !strings.Contains(msg, "valid URL") {
		t.Fatalf("expected a 'valid URL' message, got: %s", msg)
	}
}

func TestStruct_NotStruct(t *testing.T) {
	// validator returns InvalidValidationError for non-struct arguments; the
	// helper must not panic and should still wrap it.
	if err := Struct("not a struct"); err == nil {
		t.Fatal("expected error for non-struct argument, got nil")
	}
}

func TestStruct_NilPointer(t *testing.T) {
	// A nil typed pointer still validates (as the zero struct) so required
	// fields are reported; must not panic.
	var p *podLogParams
	err := Struct(p)
	if err == nil {
		t.Fatal("expected error for nil pointer with required fields, got nil")
	}
}
