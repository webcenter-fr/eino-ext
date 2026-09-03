package file

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
)

// testSessionDir is the session directory that tool invocations resolve to in
// unit tests: a plain context.Background() carries no adk run session, so the
// session falls back to the sanitized empty-session segment ("session").
func testSessionDir(workdir string) string {
	return sessionPath(workdir, "")
}

// mustCreateSessionDir pre-creates the session directory for tests that plant
// files or symlinks in it before the first tool invocation (which would
// otherwise create it lazily).
func mustCreateSessionDir(t *testing.T, workdir string) string {
	t.Helper()
	dir := testSessionDir(workdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// newTestTools creates the full tool set against a fresh temp workdir.
func newTestTools(t *testing.T, cfg *Config) ([]tool.InvokableTool, *Config) {
	t.Helper()
	if cfg == nil {
		cfg = &Config{Workdir: t.TempDir()}
	}
	tools, err := NewAllTools(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return tools, cfg
}

func toolByName(t *testing.T, tools []tool.InvokableTool, name string) tool.InvokableTool {
	t.Helper()
	for _, tl := range tools {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatalf("Info() for %s: %v", name, err)
		}
		if info.Name == name {
			return tl
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func mustWriteFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---- file_read ----

func TestFileReadFull(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	read := toolByName(t, tools, "file_read")

	mustWriteFile(t, filepath.Join(testSessionDir(cfg.Workdir), "README.md"), []byte("line1\nline2\nline3\n"))

	result, err := read.InvokableRun(context.Background(), `{"path": "README.md"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var output ReadOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatal(err)
	}
	if output.Path != "README.md" || output.Content != "line1\nline2\nline3\n" || output.Bytes != 18 || output.Truncated {
		t.Fatalf("unexpected output: %+v", output)
	}
}

func TestFileReadLineRanges(t *testing.T) {
	tests := []struct {
		name      string
		args      string
		want      string
		wantStart int
		wantEnd   int
	}{
		{name: "middle range", args: `{"path": "f.txt", "startLine": 2, "endLine": 3}`, want: "line2\nline3", wantStart: 2, wantEnd: 3},
		{name: "start only", args: `{"path": "f.txt", "startLine": 2}`, want: "line2\nline3\n", wantStart: 2, wantEnd: 4},
		{name: "end only", args: `{"path": "f.txt", "endLine": 2}`, want: "line1\nline2", wantStart: 1, wantEnd: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools, cfg := newTestTools(t, nil)
			read := toolByName(t, tools, "file_read")

			mustWriteFile(t, filepath.Join(testSessionDir(cfg.Workdir), "f.txt"), []byte("line1\nline2\nline3\n"))

			result, err := read.InvokableRun(context.Background(), tt.args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var output ReadOutput
			if err := json.Unmarshal([]byte(result), &output); err != nil {
				t.Fatal(err)
			}
			if output.Content != tt.want || output.StartLine != tt.wantStart || output.EndLine != tt.wantEnd {
				t.Fatalf("got content=%q start=%d end=%d, want %q (%d, %d)",
					output.Content, output.StartLine, output.EndLine, tt.want, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

func TestFileReadNotFound(t *testing.T) {
	tools, _ := newTestTools(t, nil)
	read := toolByName(t, tools, "file_read")

	_, err := read.InvokableRun(context.Background(), `{"path": "nonexistent.txt"}`)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestFileReadPathTraversal(t *testing.T) {
	tools, _ := newTestTools(t, nil)
	read := toolByName(t, tools, "file_read")

	_, err := read.InvokableRun(context.Background(), `{"path": "../../../etc/passwd"}`)
	if err == nil || !strings.Contains(err.Error(), "escapes root directory") {
		t.Fatalf("expected traversal error, got %v", err)
	}
}

func TestFileReadBinary(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	read := toolByName(t, tools, "file_read")

	mustWriteFile(t, filepath.Join(testSessionDir(cfg.Workdir), "binary.bin"), []byte{0x00, 0x01, 0x02})

	_, err := read.InvokableRun(context.Background(), `{"path": "binary.bin"}`)
	if err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("expected binary error, got %v", err)
	}
}

func TestFileReadEmptyFile(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	read := toolByName(t, tools, "file_read")

	mustWriteFile(t, filepath.Join(testSessionDir(cfg.Workdir), "empty.txt"), []byte{})

	result, err := read.InvokableRun(context.Background(), `{"path": "empty.txt"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var output ReadOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatal(err)
	}
	if output.Content != "" || output.Bytes != 0 {
		t.Fatalf("expected empty content and 0 bytes, got %+v", output)
	}
}

func TestFileReadLargeFile(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	read := toolByName(t, tools, "file_read")

	largeContent := make([]byte, fileutil.DefaultMaxReadBytes+100)
	for i := range largeContent {
		largeContent[i] = 'a'
	}
	largeContent[len(largeContent)-1] = '\n'
	mustWriteFile(t, filepath.Join(testSessionDir(cfg.Workdir), "large.txt"), largeContent)

	result, err := read.InvokableRun(context.Background(), `{"path": "large.txt"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var output ReadOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatal(err)
	}
	if !output.Truncated || !strings.Contains(output.Note, "truncated") {
		t.Fatalf("expected truncation, got %+v", output)
	}
	if output.Bytes != fileutil.DefaultMaxReadBytes {
		t.Fatalf("expected truncated size %d, got %d", fileutil.DefaultMaxReadBytes, output.Bytes)
	}
}

func TestFileReadDirectory(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	read := toolByName(t, tools, "file_read")

	if err := os.MkdirAll(filepath.Join(testSessionDir(cfg.Workdir), "adir"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := read.InvokableRun(context.Background(), `{"path": "adir"}`)
	if err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("expected directory error, got %v", err)
	}
}

func TestFileReadSymlinkTraversal(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	read := toolByName(t, tools, "file_read")

	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionDir := mustCreateSessionDir(t, cfg.Workdir)
	if err := os.Symlink(outsideDir, filepath.Join(sessionDir, "link")); err != nil {
		t.Fatal(err)
	}

	_, err := read.InvokableRun(context.Background(), `{"path": "link/secret.txt"}`)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
}

func TestFileReadStartLineAfterEndLine(t *testing.T) {
	tools, _ := newTestTools(t, nil)
	read := toolByName(t, tools, "file_read")

	_, err := read.InvokableRun(context.Background(), `{"path": "f.txt", "startLine": 5, "endLine": 2}`)
	if err == nil || !strings.Contains(err.Error(), "must be <=") {
		t.Fatalf("expected start/end ordering error, got %v", err)
	}
}

func TestFileReadSessionIsolation(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	read := toolByName(t, tools, "file_read")

	// adk session values cannot be set on a plain context outside a runner
	// (the same limitation as components/agent/memory tests), so verify the
	// isolation mechanism directly: each session maps to its own subdirectory,
	// and no invocation can escape its session directory into another's.
	alice := sessionPath(cfg.Workdir, "alice")
	bob := sessionPath(cfg.Workdir, "bob")
	if alice == bob {
		t.Fatal("distinct sessions must map to distinct directories")
	}
	mustWriteFile(t, filepath.Join(alice, "secret.txt"), []byte("alice data"))
	mustWriteFile(t, filepath.Join(bob, "secret.txt"), []byte("bob data"))

	// The default session cannot see either session's files by plain name...
	if _, err := read.InvokableRun(context.Background(), `{"path": "secret.txt"}`); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
	// ...nor by traversing out of the session directory.
	if _, err := read.InvokableRun(context.Background(), `{"path": "../alice/secret.txt"}`); err == nil || !strings.Contains(err.Error(), "escapes root directory") {
		t.Fatalf("expected traversal error, got %v", err)
	}
}

// ---- file_write ----

func TestFileWriteNewFile(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	write := toolByName(t, tools, "file_write")

	result, err := write.InvokableRun(context.Background(), `{"path": "hello.txt", "content": "hello"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var output WriteOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatal(err)
	}
	if output.Path != "hello.txt" || output.Bytes != 5 || output.Mode != "overwrite" {
		t.Fatalf("unexpected output: %+v", output)
	}
	data, err := os.ReadFile(filepath.Join(testSessionDir(cfg.Workdir), "hello.txt"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("file content mismatch: %q err=%v", string(data), err)
	}
}

func TestFileWriteOverwrite(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	write := toolByName(t, tools, "file_write")

	path := filepath.Join(testSessionDir(cfg.Workdir), "f.txt")
	mustWriteFile(t, path, []byte("old content"))

	if _, err := write.InvokableRun(context.Background(), `{"path": "f.txt", "content": "new"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("file must be overwritten, got %q err=%v", string(data), err)
	}
}

func TestFileWriteAppend(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	write := toolByName(t, tools, "file_write")

	path := filepath.Join(testSessionDir(cfg.Workdir), "log.txt")
	mustWriteFile(t, path, []byte("one"))

	if _, err := write.InvokableRun(context.Background(), `{"path": "log.txt", "content": " two", "append": true}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := write.InvokableRun(context.Background(), `{"path": "log.txt", "content": " three", "append": true}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "one two three" {
		t.Fatalf("appended content mismatch: %q err=%v", string(data), err)
	}
}

func TestFileWriteAppendNewFile(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	write := toolByName(t, tools, "file_write")

	if _, err := write.InvokableRun(context.Background(), `{"path": "new.txt", "content": "created by append", "append": true}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(testSessionDir(cfg.Workdir), "new.txt"))
	if err != nil || string(data) != "created by append" {
		t.Fatalf("append must create the file, got %q err=%v", string(data), err)
	}
}

func TestFileWriteCreatesParentDirs(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	write := toolByName(t, tools, "file_write")

	if _, err := write.InvokableRun(context.Background(), `{"path": "deep/nested/file.txt", "content": "nested"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(testSessionDir(cfg.Workdir), "deep", "nested", "file.txt"))
	if err != nil || string(data) != "nested" {
		t.Fatalf("parent dirs must be created, got %q err=%v", string(data), err)
	}
}

func TestFileWriteContentTooLarge(t *testing.T) {
	tools, _ := newTestTools(t, &Config{Workdir: t.TempDir(), MaxWriteBytes: 10})
	write := toolByName(t, tools, "file_write")

	_, err := write.InvokableRun(context.Background(), `{"path": "big.txt", "content": "0123456789ABCDEF"}`)
	if err == nil || !strings.Contains(err.Error(), "exceeds the maximum") {
		t.Fatalf("expected size-limit error, got %v", err)
	}
}

func TestFileWritePathTraversal(t *testing.T) {
	tools, _ := newTestTools(t, nil)
	write := toolByName(t, tools, "file_write")

	_, err := write.InvokableRun(context.Background(), `{"path": "../../evil.txt", "content": "evil"}`)
	if err == nil || !strings.Contains(err.Error(), "escapes root directory") {
		t.Fatalf("expected traversal error, got %v", err)
	}
}

func TestFileWriteToDirectory(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	write := toolByName(t, tools, "file_write")

	if err := os.MkdirAll(filepath.Join(testSessionDir(cfg.Workdir), "adir"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := write.InvokableRun(context.Background(), `{"path": "adir", "content": "x"}`)
	if err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("expected directory error, got %v", err)
	}
}

func TestFileWriteToSymlink(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	write := toolByName(t, tools, "file_write")

	sessionDir := testSessionDir(cfg.Workdir)
	mustWriteFile(t, filepath.Join(sessionDir, "target.txt"), []byte("target"))
	if err := os.Symlink(filepath.Join(sessionDir, "target.txt"), filepath.Join(sessionDir, "link.txt")); err != nil {
		t.Fatal(err)
	}

	_, err := write.InvokableRun(context.Background(), `{"path": "link.txt", "content": "x"}`)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
	// The symlink target must be untouched.
	data, readErr := os.ReadFile(filepath.Join(sessionDir, "target.txt"))
	if readErr != nil || string(data) != "target" {
		t.Fatalf("symlink target must be untouched, got %q err=%v", string(data), readErr)
	}
}

func TestFileWriteSymlinkTraversal(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	write := toolByName(t, tools, "file_write")

	outsideDir := t.TempDir()
	sessionDir := mustCreateSessionDir(t, cfg.Workdir)
	if err := os.Symlink(outsideDir, filepath.Join(sessionDir, "link")); err != nil {
		t.Fatal(err)
	}

	_, err := write.InvokableRun(context.Background(), `{"path": "link/evil.txt", "content": "evil"}`)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
	_, statErr := os.Stat(filepath.Join(outsideDir, "evil.txt"))
	if !os.IsNotExist(statErr) {
		t.Fatal("file must not be written outside the session via symlink")
	}
}

func TestFileWriteSessionIsolation(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	write := toolByName(t, tools, "file_write")

	// Same limitation as TestFileReadSessionIsolation: verify the session
	// directory mapping and that writes cannot escape into another session.
	alice := sessionPath(cfg.Workdir, "alice")
	bob := sessionPath(cfg.Workdir, "bob")
	if alice == bob {
		t.Fatal("distinct sessions must map to distinct directories")
	}

	if _, err := write.InvokableRun(context.Background(), `{"path": "../bob/leak.txt", "content": "leak"}`); err == nil || !strings.Contains(err.Error(), "escapes root directory") {
		t.Fatalf("expected traversal error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(bob, "leak.txt")); !os.IsNotExist(err) {
		t.Fatal("file must not be written into another session's directory")
	}
}

// ---- file_delete ----

func TestFileDeleteFile(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	del := toolByName(t, tools, "file_delete")

	path := filepath.Join(testSessionDir(cfg.Workdir), "gone.txt")
	mustWriteFile(t, path, []byte("bye"))

	result, err := del.InvokableRun(context.Background(), `{"path": "gone.txt"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var output DeleteOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatal(err)
	}
	if output.Path != "gone.txt" || output.Type != "file" || !output.Deleted {
		t.Fatalf("unexpected output: %+v", output)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file must be deleted")
	}
}

func TestFileDeleteDirectory(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	del := toolByName(t, tools, "file_delete")

	dir := filepath.Join(testSessionDir(cfg.Workdir), "adir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := del.InvokableRun(context.Background(), `{"path": "adir"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var output DeleteOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatal(err)
	}
	if output.Type != "dir" || !output.Deleted {
		t.Fatalf("unexpected output: %+v", output)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("directory must be deleted")
	}
}

func TestFileDeleteNestedDirectory(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	del := toolByName(t, tools, "file_delete")

	mustWriteFile(t, filepath.Join(testSessionDir(cfg.Workdir), "tree", "deep", "nested.txt"), []byte("n"))

	if _, err := del.InvokableRun(context.Background(), `{"path": "tree"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(testSessionDir(cfg.Workdir), "tree")); !os.IsNotExist(err) {
		t.Fatal("nested directory tree must be deleted recursively")
	}
}

func TestFileDeleteNotFound(t *testing.T) {
	tools, _ := newTestTools(t, nil)
	del := toolByName(t, tools, "file_delete")

	_, err := del.InvokableRun(context.Background(), `{"path": "nonexistent.txt"}`)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestFileDeleteSessionRoot(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	del := toolByName(t, tools, "file_delete")

	mustCreateSessionDir(t, cfg.Workdir)

	_, err := del.InvokableRun(context.Background(), `{"path": "."}`)
	if err == nil || !strings.Contains(err.Error(), "session root") {
		t.Fatalf("expected session-root error, got %v", err)
	}
	if _, statErr := os.Stat(testSessionDir(cfg.Workdir)); statErr != nil {
		t.Fatalf("session directory must not be deleted, got %v", statErr)
	}
}

func TestFileDeletePathTraversal(t *testing.T) {
	tools, _ := newTestTools(t, nil)
	del := toolByName(t, tools, "file_delete")

	_, err := del.InvokableRun(context.Background(), `{"path": "../../etc/passwd"}`)
	if err == nil || !strings.Contains(err.Error(), "escapes root directory") {
		t.Fatalf("expected traversal error, got %v", err)
	}
}

func TestFileDeleteSymlink(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	del := toolByName(t, tools, "file_delete")

	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionDir := mustCreateSessionDir(t, cfg.Workdir)
	link := filepath.Join(sessionDir, "link")
	if err := os.Symlink(outsideDir, link); err != nil {
		t.Fatal(err)
	}

	_, err := del.InvokableRun(context.Background(), `{"path": "link/secret.txt"}`)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
	// The symlink itself and the outside file must remain.
	if _, statErr := os.Lstat(link); statErr != nil {
		t.Fatalf("symlink must remain, got %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(outsideDir, "secret.txt")); statErr != nil {
		t.Fatalf("outside file must remain, got %v", statErr)
	}
}

// ---- file_copy ----

func TestFileCopyFile(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	cp := toolByName(t, tools, "file_copy")

	sessionDir := testSessionDir(cfg.Workdir)
	mustWriteFile(t, filepath.Join(sessionDir, "src.txt"), []byte("copy me"))

	result, err := cp.InvokableRun(context.Background(), `{"source": "src.txt", "destination": "dst.txt"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var output CopyOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatal(err)
	}
	if output.Source != "src.txt" || output.Destination != "dst.txt" || output.Type != "file" || !output.Copied {
		t.Fatalf("unexpected output: %+v", output)
	}
	dst, err := os.ReadFile(filepath.Join(sessionDir, "dst.txt"))
	if err != nil || string(dst) != "copy me" {
		t.Fatalf("copied content mismatch: %q err=%v", string(dst), err)
	}
}

func TestFileCopyFileOverwrite(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	cp := toolByName(t, tools, "file_copy")

	sessionDir := testSessionDir(cfg.Workdir)
	mustWriteFile(t, filepath.Join(sessionDir, "src.txt"), []byte("new content"))
	mustWriteFile(t, filepath.Join(sessionDir, "dst.txt"), []byte("old content"))

	if _, err := cp.InvokableRun(context.Background(), `{"source": "src.txt", "destination": "dst.txt"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dst, err := os.ReadFile(filepath.Join(sessionDir, "dst.txt"))
	if err != nil || string(dst) != "new content" {
		t.Fatalf("destination must be overwritten, got %q err=%v", string(dst), err)
	}
}

func TestFileCopyFileCreatesParentDirs(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	cp := toolByName(t, tools, "file_copy")

	sessionDir := testSessionDir(cfg.Workdir)
	mustWriteFile(t, filepath.Join(sessionDir, "src.txt"), []byte("deep"))

	if _, err := cp.InvokableRun(context.Background(), `{"source": "src.txt", "destination": "deep/nested/dst.txt"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dst, err := os.ReadFile(filepath.Join(sessionDir, "deep", "nested", "dst.txt"))
	if err != nil || string(dst) != "deep" {
		t.Fatalf("destination parent dirs must be created, got %q err=%v", string(dst), err)
	}
}

func TestFileCopyDirectory(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	cp := toolByName(t, tools, "file_copy")

	sessionDir := testSessionDir(cfg.Workdir)
	mustWriteFile(t, filepath.Join(sessionDir, "sub", "a.txt"), []byte("aaaa"))
	mustWriteFile(t, filepath.Join(sessionDir, "sub", "b.txt"), []byte("bb"))

	result, err := cp.InvokableRun(context.Background(), `{"source": "sub", "destination": "sub2"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var output CopyOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatal(err)
	}
	if output.Type != "dir" || !output.Copied || output.FileCount != 2 || output.TotalBytes != 6 {
		t.Fatalf("unexpected output: %+v", output)
	}
	a, err := os.ReadFile(filepath.Join(sessionDir, "sub2", "a.txt"))
	if err != nil || string(a) != "aaaa" {
		t.Fatalf("copied tree mismatch: %q err=%v", string(a), err)
	}
}

func TestFileCopyDirectoryMerge(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	cp := toolByName(t, tools, "file_copy")

	sessionDir := testSessionDir(cfg.Workdir)
	mustWriteFile(t, filepath.Join(sessionDir, "sub", "a.txt"), []byte("from source"))
	mustWriteFile(t, filepath.Join(sessionDir, "sub2", "a.txt"), []byte("old"))
	mustWriteFile(t, filepath.Join(sessionDir, "sub2", "extra.txt"), []byte("extra"))

	if _, err := cp.InvokableRun(context.Background(), `{"source": "sub", "destination": "sub2"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a, err := os.ReadFile(filepath.Join(sessionDir, "sub2", "a.txt"))
	if err != nil || string(a) != "from source" {
		t.Fatalf("source files must overwrite matching destination files, got %q err=%v", string(a), err)
	}
	extra, err := os.ReadFile(filepath.Join(sessionDir, "sub2", "extra.txt"))
	if err != nil || string(extra) != "extra" {
		t.Fatalf("extra destination files must remain after a merge, got %q err=%v", string(extra), err)
	}
}

func TestFileCopySourceNotFound(t *testing.T) {
	tools, _ := newTestTools(t, nil)
	cp := toolByName(t, tools, "file_copy")

	_, err := cp.InvokableRun(context.Background(), `{"source": "nonexistent.txt", "destination": "dst.txt"}`)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestFileCopySamePath(t *testing.T) {
	tools, _ := newTestTools(t, nil)
	cp := toolByName(t, tools, "file_copy")

	_, err := cp.InvokableRun(context.Background(), `{"source": "same.txt", "destination": "same.txt"}`)
	if err == nil || !strings.Contains(err.Error(), "same path") {
		t.Fatalf("expected same-path error, got %v", err)
	}
}

func TestFileCopyDestInsideSource(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	cp := toolByName(t, tools, "file_copy")

	mustWriteFile(t, filepath.Join(testSessionDir(cfg.Workdir), "sub", "a.txt"), []byte("a"))

	_, err := cp.InvokableRun(context.Background(), `{"source": "sub", "destination": "sub/sub2"}`)
	if err == nil || !strings.Contains(err.Error(), "inside the source directory") {
		t.Fatalf("expected nested-destination error, got %v", err)
	}
	// Verify no self-nested directories were created.
	if _, statErr := os.Stat(filepath.Join(testSessionDir(cfg.Workdir), "sub", "sub2")); !os.IsNotExist(statErr) {
		t.Fatal("destination must not be created when it is inside the source")
	}
}

func TestFileCopyTypeMismatch(t *testing.T) {
	t.Run("file to existing directory", func(t *testing.T) {
		tools, cfg := newTestTools(t, nil)
		cp := toolByName(t, tools, "file_copy")

		sessionDir := testSessionDir(cfg.Workdir)
		mustWriteFile(t, filepath.Join(sessionDir, "src.txt"), []byte("x"))
		if err := os.MkdirAll(filepath.Join(sessionDir, "adir"), 0o755); err != nil {
			t.Fatal(err)
		}

		_, err := cp.InvokableRun(context.Background(), `{"source": "src.txt", "destination": "adir"}`)
		if err == nil || !strings.Contains(err.Error(), "is a directory but source is a file") {
			t.Fatalf("expected type-mismatch error, got %v", err)
		}
	})

	t.Run("directory to existing file", func(t *testing.T) {
		tools, cfg := newTestTools(t, nil)
		cp := toolByName(t, tools, "file_copy")

		sessionDir := testSessionDir(cfg.Workdir)
		mustWriteFile(t, filepath.Join(sessionDir, "sub", "a.txt"), []byte("a"))
		mustWriteFile(t, filepath.Join(sessionDir, "file.txt"), []byte("f"))

		_, err := cp.InvokableRun(context.Background(), `{"source": "sub", "destination": "file.txt"}`)
		if err == nil || !strings.Contains(err.Error(), "is a file but source is a directory") {
			t.Fatalf("expected type-mismatch error, got %v", err)
		}
	})
}

func TestFileCopyPathTraversal(t *testing.T) {
	tests := []struct {
		name string
		args string
	}{
		{name: "traversal in source", args: `{"source": "../../etc/passwd", "destination": "dst.txt"}`},
		{name: "traversal in destination", args: `{"source": "src.txt", "destination": "../../evil.txt"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools, _ := newTestTools(t, nil)
			cp := toolByName(t, tools, "file_copy")

			_, err := cp.InvokableRun(context.Background(), tt.args)
			if err == nil || !strings.Contains(err.Error(), "escapes root directory") {
				t.Fatalf("expected traversal error, got %v", err)
			}
		})
	}
}

func TestFileCopySymlinkSource(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	cp := toolByName(t, tools, "file_copy")

	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionDir := mustCreateSessionDir(t, cfg.Workdir)
	if err := os.Symlink(outsideDir, filepath.Join(sessionDir, "link")); err != nil {
		t.Fatal(err)
	}

	_, err := cp.InvokableRun(context.Background(), `{"source": "link/secret.txt", "destination": "stolen.txt"}`)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
	_, statErr := os.Stat(filepath.Join(sessionDir, "stolen.txt"))
	if !os.IsNotExist(statErr) {
		t.Fatal("file must not be copied via symlink")
	}
}

func TestFileCopySkipsSymlinkInsideTree(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	cp := toolByName(t, tools, "file_copy")

	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionDir := testSessionDir(cfg.Workdir)
	mustWriteFile(t, filepath.Join(sessionDir, "sub", "a.txt"), []byte("kept"))
	if err := os.Symlink(filepath.Join(outsideDir, "secret.txt"), filepath.Join(sessionDir, "sub", "link.txt")); err != nil {
		t.Fatal(err)
	}

	if _, err := cp.InvokableRun(context.Background(), `{"source": "sub", "destination": "sub2"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, readErr := os.ReadFile(filepath.Join(sessionDir, "sub2", "a.txt"))
	if readErr != nil || string(data) != "kept" {
		t.Fatalf("regular files must be copied, got %q err=%v", string(data), readErr)
	}
	if _, statErr := os.Lstat(filepath.Join(sessionDir, "sub2", "link.txt")); !os.IsNotExist(statErr) {
		t.Fatal("symlink inside the copied tree must not be copied")
	}
	if _, statErr := os.Stat(filepath.Join(sessionDir, "sub2", "secret.txt")); !os.IsNotExist(statErr) {
		t.Fatal("symlink target content must not be copied")
	}
}

// ---- file_move ----

func TestFileMoveFile(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	mv := toolByName(t, tools, "file_move")

	sessionDir := testSessionDir(cfg.Workdir)
	mustWriteFile(t, filepath.Join(sessionDir, "src.txt"), []byte("moving on"))

	result, err := mv.InvokableRun(context.Background(), `{"source": "src.txt", "destination": "dst.txt"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var output MoveOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatal(err)
	}
	if output.Source != "src.txt" || output.Destination != "dst.txt" || output.Type != "file" || !output.Moved {
		t.Fatalf("unexpected output: %+v", output)
	}
	if _, statErr := os.Stat(filepath.Join(sessionDir, "src.txt")); !os.IsNotExist(statErr) {
		t.Fatal("source must be gone after move")
	}
	dst, err := os.ReadFile(filepath.Join(sessionDir, "dst.txt"))
	if err != nil || string(dst) != "moving on" {
		t.Fatalf("moved content mismatch: %q err=%v", string(dst), err)
	}
}

func TestFileMoveFileRename(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	mv := toolByName(t, tools, "file_move")

	sessionDir := testSessionDir(cfg.Workdir)
	mustWriteFile(t, filepath.Join(sessionDir, "original.txt"), []byte("content"))

	if _, err := mv.InvokableRun(context.Background(), `{"source": "original.txt", "destination": "renamed.txt"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(sessionDir, "original.txt")); !os.IsNotExist(statErr) {
		t.Fatal("original must be gone after rename")
	}
	if _, statErr := os.Stat(filepath.Join(sessionDir, "renamed.txt")); statErr != nil {
		t.Fatalf("renamed file must exist, got %v", statErr)
	}
}

func TestFileMoveDirectory(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	mv := toolByName(t, tools, "file_move")

	sessionDir := testSessionDir(cfg.Workdir)
	mustWriteFile(t, filepath.Join(sessionDir, "sub", "a.txt"), []byte("aaa"))

	result, err := mv.InvokableRun(context.Background(), `{"source": "sub", "destination": "sub2"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var output MoveOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatal(err)
	}
	if output.Type != "dir" || !output.Moved {
		t.Fatalf("unexpected output: %+v", output)
	}
	if _, statErr := os.Stat(filepath.Join(sessionDir, "sub")); !os.IsNotExist(statErr) {
		t.Fatal("source directory must be gone after move")
	}
	a, err := os.ReadFile(filepath.Join(sessionDir, "sub2", "a.txt"))
	if err != nil || string(a) != "aaa" {
		t.Fatalf("moved tree mismatch: %q err=%v", string(a), err)
	}
}

func TestFileMoveOverwrite(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	mv := toolByName(t, tools, "file_move")

	sessionDir := testSessionDir(cfg.Workdir)
	mustWriteFile(t, filepath.Join(sessionDir, "src.txt"), []byte("fresh"))
	mustWriteFile(t, filepath.Join(sessionDir, "dst.txt"), []byte("stale"))

	if _, err := mv.InvokableRun(context.Background(), `{"source": "src.txt", "destination": "dst.txt"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dst, err := os.ReadFile(filepath.Join(sessionDir, "dst.txt"))
	if err != nil || string(dst) != "fresh" {
		t.Fatalf("destination must be overwritten, got %q err=%v", string(dst), err)
	}
	if _, statErr := os.Stat(filepath.Join(sessionDir, "src.txt")); !os.IsNotExist(statErr) {
		t.Fatal("source must be gone after move")
	}
}

func TestFileMoveSourceNotFound(t *testing.T) {
	tools, _ := newTestTools(t, nil)
	mv := toolByName(t, tools, "file_move")

	_, err := mv.InvokableRun(context.Background(), `{"source": "nonexistent.txt", "destination": "dst.txt"}`)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestFileMoveSamePath(t *testing.T) {
	tools, _ := newTestTools(t, nil)
	mv := toolByName(t, tools, "file_move")

	_, err := mv.InvokableRun(context.Background(), `{"source": "same.txt", "destination": "same.txt"}`)
	if err == nil || !strings.Contains(err.Error(), "same path") {
		t.Fatalf("expected same-path error, got %v", err)
	}
}

func TestFileMoveDestInsideSource(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	mv := toolByName(t, tools, "file_move")

	sessionDir := testSessionDir(cfg.Workdir)
	mustWriteFile(t, filepath.Join(sessionDir, "sub", "a.txt"), []byte("a"))

	_, err := mv.InvokableRun(context.Background(), `{"source": "sub", "destination": "sub/sub2"}`)
	if err == nil || !strings.Contains(err.Error(), "inside the source directory") {
		t.Fatalf("expected nested-destination error, got %v", err)
	}
	// Verify the source was not touched.
	data, readErr := os.ReadFile(filepath.Join(sessionDir, "sub", "a.txt"))
	if readErr != nil || string(data) != "a" {
		t.Fatalf("source must not be touched, got %q err=%v", string(data), readErr)
	}
}

func TestFileMoveTypeMismatch(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	mv := toolByName(t, tools, "file_move")

	sessionDir := testSessionDir(cfg.Workdir)
	mustWriteFile(t, filepath.Join(sessionDir, "src.txt"), []byte("x"))
	if err := os.MkdirAll(filepath.Join(sessionDir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := mv.InvokableRun(context.Background(), `{"source": "src.txt", "destination": "adir"}`)
	if err == nil || !strings.Contains(err.Error(), "is a directory but source is a file") {
		t.Fatalf("expected type-mismatch error, got %v", err)
	}
}

func TestFileMovePathTraversal(t *testing.T) {
	tests := []struct {
		name string
		args string
	}{
		{name: "traversal in source", args: `{"source": "../../etc/passwd", "destination": "dst.txt"}`},
		{name: "traversal in destination", args: `{"source": "src.txt", "destination": "../../evil.txt"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools, _ := newTestTools(t, nil)
			mv := toolByName(t, tools, "file_move")

			_, err := mv.InvokableRun(context.Background(), tt.args)
			if err == nil || !strings.Contains(err.Error(), "escapes root directory") {
				t.Fatalf("expected traversal error, got %v", err)
			}
		})
	}
}

func TestFileMoveSymlinkSource(t *testing.T) {
	tools, cfg := newTestTools(t, nil)
	mv := toolByName(t, tools, "file_move")

	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionDir := mustCreateSessionDir(t, cfg.Workdir)
	if err := os.Symlink(outsideDir, filepath.Join(sessionDir, "link")); err != nil {
		t.Fatal(err)
	}

	_, err := mv.InvokableRun(context.Background(), `{"source": "link/secret.txt", "destination": "stolen.txt"}`)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
	// The symlink must remain and nothing may be stolen.
	if _, statErr := os.Lstat(filepath.Join(sessionDir, "link")); statErr != nil {
		t.Fatalf("symlink must remain, got %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(sessionDir, "stolen.txt")); !os.IsNotExist(statErr) {
		t.Fatal("file must not be moved via symlink")
	}
}

// ---- NewAllTools / NewReadOnlyTools ----

func TestNewAllTools(t *testing.T) {
	tools, _ := newTestTools(t, nil)

	wantNames := []string{"file_read", "file_write", "file_delete", "file_copy", "file_move"}
	if len(tools) != len(wantNames) {
		t.Fatalf("expected %d tools, got %d", len(wantNames), len(tools))
	}
	for i, want := range wantNames {
		info, err := tools[i].Info(context.Background())
		if err != nil {
			t.Fatalf("Info() for tool %d: %v", i, err)
		}
		if info.Name != want {
			t.Fatalf("tool %d name = %q, want %q", i, info.Name, want)
		}
	}
}

func TestNewAllToolsNilConfig(t *testing.T) {
	// A nil config has no Workdir; validation must reject it instead of panicking.
	if _, err := NewAllTools(context.Background(), nil); err == nil {
		t.Fatal("expected validation error for nil config, got nil")
	}
}

func TestNewAllToolsInvalidWorkdir(t *testing.T) {
	_, err := NewAllTools(context.Background(), &Config{Workdir: "/etc"})
	if err == nil || !strings.Contains(err.Error(), "system directory") {
		t.Fatalf("expected system-directory error, got %v", err)
	}
}

func TestNewToolRejectsInvalidConfig(t *testing.T) {
	// Each individual tool constructor must validate its config (and tolerate
	// nil) instead of building a tool that panics or writes relative to the
	// process working directory on first use.
	constructors := map[string]func(context.Context, *Config) error{
		"file_read": func(ctx context.Context, cfg *Config) error {
			_, err := NewReadTool(ctx, cfg)
			return err
		},
		"file_write": func(ctx context.Context, cfg *Config) error {
			_, err := NewWriteTool(ctx, cfg)
			return err
		},
		"file_delete": func(ctx context.Context, cfg *Config) error {
			_, err := NewDeleteTool(ctx, cfg)
			return err
		},
		"file_copy": func(ctx context.Context, cfg *Config) error {
			_, err := NewCopyTool(ctx, cfg)
			return err
		},
		"file_move": func(ctx context.Context, cfg *Config) error {
			_, err := NewMoveTool(ctx, cfg)
			return err
		},
	}
	for name, ctor := range constructors {
		t.Run(name+" nil config", func(t *testing.T) {
			if err := ctor(context.Background(), nil); err == nil {
				t.Fatal("expected validation error for nil config, got nil")
			}
		})
		t.Run(name+" empty workdir", func(t *testing.T) {
			if err := ctor(context.Background(), &Config{}); err == nil {
				t.Fatal("expected validation error for empty Workdir, got nil")
			}
		})
		t.Run(name+" valid config", func(t *testing.T) {
			if err := ctor(context.Background(), &Config{Workdir: t.TempDir()}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestNewReadOnlyTools(t *testing.T) {
	tools, err := NewReadOnlyTools(context.Background(), &Config{Workdir: t.TempDir()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	info, err := tools[0].Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "file_read" {
		t.Fatalf("tool name = %q, want %q", info.Name, "file_read")
	}
}

// ---- WriteToolNames ----

func TestWriteToolNames(t *testing.T) {
	want := []string{"file_write", "file_delete", "file_copy", "file_move"}
	got := WriteToolNames()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("WriteToolNames() = %v, want %v", got, want)
	}
}
