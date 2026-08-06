package contentcomp

import (
	"context"
	"testing"
)

func TestRef(t *testing.T) {
	r := Ref{Key: "sha256:abc123", Size: 42}
	if r.Key != "sha256:abc123" {
		t.Errorf("expected Key 'sha256:abc123', got %q", r.Key)
	}
	if r.Size != 42 {
		t.Errorf("expected Size 42, got %d", r.Size)
	}
}

func TestStoreAndCompressorInterfaces(t *testing.T) {
	ctx := context.Background()

	var store Store = &mockStore{data: make(map[string]string)}
	ref, err := store.Put(ctx, "hello")
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}

	var comp Compressor = &mockCompressor{name: "test"}
	if comp.Name() != "test" {
		t.Errorf("expected name 'test', got %q", comp.Name())
	}
	out, changed, err := comp.Compress(ctx, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("expected changed=true for non-empty input")
	}
	if out == "hello" {
		t.Error("expected output different from input")
	}
}

type mockStore struct {
	data map[string]string
}

func (m *mockStore) Put(_ context.Context, content string) (Ref, error) {
	key := "sha256:" + content
	m.data[key] = content
	return Ref{Key: key, Size: len(content)}, nil
}

func (m *mockStore) Get(_ context.Context, ref Ref) (string, error) {
	return m.data[ref.Key], nil
}

type mockCompressor struct {
	name string
}

func (m *mockCompressor) Name() string { return m.name }
func (m *mockCompressor) Compress(_ context.Context, content string) (string, bool, error) {
	if content == "" {
		return content, false, nil
	}
	return "[compressed:" + content + "]", true, nil
}
