package fileutil

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRelativePath(t *testing.T) {
	root := string(filepath.Separator) + filepath.Join("tmp", "root")
	tests := []struct {
		name    string
		root    string
		relPath string
		want    string
		wantErr string
	}{
		{name: "empty relPath returns cleaned root", root: root, relPath: "", want: root},
		{name: "normal relative path", root: root, relPath: "sub/file.txt", want: filepath.Join(root, "sub", "file.txt")},
		{name: "dot path returns root", root: root, relPath: ".", want: root},
		{name: "inner dot-dot stays inside", root: root, relPath: "sub/../file.txt", want: filepath.Join(root, "file.txt")},
		{name: "trailing dot-dot resolving to root", root: root, relPath: "sub/..", want: root},
		{name: "absolute path rejected", root: root, relPath: "/etc/passwd", wantErr: "absolute path"},
		{name: "null byte rejected", root: root, relPath: "valid\x00../../etc/passwd", wantErr: "null byte"},
		{name: "escaping dot-dot rejected", root: root, relPath: "../../../etc/passwd", wantErr: "escapes root directory"},
		{name: "escaping trailing dot-dot rejected", root: root, relPath: "sub/../../x", wantErr: "escapes root directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateRelativePath(tt.root, tt.relPath)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (path %q)", tt.wantErr, got)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsWithinPath(t *testing.T) {
	tests := []struct {
		name string
		root string
		path string
		want bool
	}{
		{name: "equal paths", root: "/tmp/root", path: "/tmp/root", want: true},
		{name: "descendant", root: "/tmp/root", path: "/tmp/root/sub/file.txt", want: true},
		{name: "sibling", root: "/tmp/root", path: "/tmp/other", want: false},
		{name: "parent", root: "/tmp/root", path: "/tmp", want: false},
		{name: "prefix but not descendant", root: "/foo/bar", path: "/foo/barbaz", want: false},
		{name: "root is slash", root: "/", path: "/etc/passwd", want: true},
		{name: "relative path under absolute root", root: "/tmp/root", path: "sub/file.txt", want: false},
		{name: "uncleaned inputs", root: "/tmp/root/", path: "/tmp/root/./sub/../f.txt", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsWithinPath(tt.root, tt.path); got != tt.want {
				t.Fatalf("IsWithinPath(%q, %q) = %v, want %v", tt.root, tt.path, got, tt.want)
			}
		})
	}
}

func TestResolveSymlinkSafe(t *testing.T) {
	t.Run("cases", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "sub", "file.txt"), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
		outside := t.TempDir()
		if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("top secret"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "sub", "filelink.txt")); err != nil {
			t.Fatal(err)
		}

		tests := []struct {
			name       string
			fullPath   string
			createDirs bool
			wantErr    string
		}{
			{name: "existing path without symlinks", fullPath: filepath.Join(root, "sub", "file.txt")},
			{name: "root itself", fullPath: root},
			{name: "missing final component allowed", fullPath: filepath.Join(root, "sub", "new.txt")},
			{name: "missing intermediate with createDirs", fullPath: filepath.Join(root, "createdir", "a.txt"), createDirs: true},
			{name: "missing intermediate without createDirs", fullPath: filepath.Join(root, "missingdir", "a.txt"), wantErr: "does not exist"},
			{name: "symlink at intermediate component", fullPath: filepath.Join(root, "link", "secret.txt"), wantErr: "symlink"},
			{name: "symlink at final component", fullPath: filepath.Join(root, "sub", "filelink.txt"), wantErr: "symlink"},
			{name: "path escaping root", fullPath: filepath.Join(root, "..", "outside"), wantErr: "escapes root directory"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got, err := ResolveSymlinkSafe(root, tt.fullPath, tt.createDirs)
				if tt.wantErr != "" {
					if err == nil {
						t.Fatalf("expected error containing %q, got nil (path %q)", tt.wantErr, got)
					}
					if !strings.Contains(err.Error(), tt.wantErr) {
						t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
					}
					return
				}
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !IsWithinPath(root, got) {
					t.Fatalf("resolved path %q is not within root %q", got, root)
				}
				if got != filepath.Clean(tt.fullPath) {
					t.Fatalf("got %q, want %q", got, filepath.Clean(tt.fullPath))
				}
			})
		}
	})

	t.Run("createDirs creates missing intermediate directory", func(t *testing.T) {
		root := t.TempDir()
		full := filepath.Join(root, "a", "b", "file.txt")
		got, err := ResolveSymlinkSafe(root, full, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != filepath.Clean(full) {
			t.Fatalf("got %q, want %q", got, filepath.Clean(full))
		}
		fi, err := os.Stat(filepath.Join(root, "a", "b"))
		if err != nil || !fi.IsDir() {
			t.Fatalf("expected intermediate directory to be created, got err=%v", err)
		}
	})
}

func TestIsBinary(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{name: "text data", data: []byte("hello world\n"), want: false},
		{name: "null in first 8KB", data: []byte{0x00, 0x01, 0x02}, want: true},
		{name: "null after 8KB", data: append([]byte(strings.Repeat("a", 9000)), 0), want: false},
		{name: "empty data", data: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsBinary(tt.data); got != tt.want {
				t.Fatalf("IsBinary() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRejectDotGitPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "empty path", path: ""},
		{name: "normal path", path: "src/main.go"},
		{name: "dot git", path: ".git", wantErr: true},
		{name: "dot git file", path: ".git/HEAD", wantErr: true},
		{name: "dot-prefixed dot git", path: "./.git/HEAD", wantErr: true},
		{name: "dot-dot bypass", path: "subdir/../.git/HEAD", wantErr: true},
		{name: "nested dot git", path: "subdir/.git/HEAD", wantErr: true},
		{name: "gitignore is allowed", path: ".gitignore"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RejectDotGitPath(tt.path)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error for path %q, got nil", tt.path)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error for path %q: %v", tt.path, err)
			}
		})
	}
}

func TestValidateRootDir(t *testing.T) {
	tests := []struct {
		name    string
		dir     string
		wantErr string
	}{
		{name: "valid absolute path", dir: "/home/user/work"},
		{name: "empty", dir: "", wantErr: "required"},
		{name: "relative", dir: "relative/path", wantErr: "absolute path"},
		{name: "contains dot-dot", dir: "/home/../etc", wantErr: "directory traversal"},
		{name: "system dir /etc", dir: "/etc", wantErr: "system directory"},
		{name: "system dir /tmp", dir: "/tmp", wantErr: "system directory"},
		{name: "system dir /", dir: "/", wantErr: "system directory"},
		{name: "under /proc", dir: "/proc/self", wantErr: "system mount"},
		{name: "under /sys", dir: "/sys/fs", wantErr: "system mount"},
		{name: "under /dev", dir: "/dev/shm", wantErr: "system mount"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRootDir(tt.dir)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

func TestCopyFileContents(t *testing.T) {
	t.Run("copies content", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "src.txt")
		dst := filepath.Join(t.TempDir(), "dst.txt")
		if err := os.WriteFile(src, []byte("file contents"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := CopyFileContents(src, dst); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		data, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "file contents" {
			t.Fatalf("got %q, want %q", string(data), "file contents")
		}
		// Created files must not be group/other writable (0o644 convention,
		// not os.Create's 0o666).
		fi, err := os.Stat(dst)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm&0o022 != 0 {
			t.Fatalf("destination permissions = %o, want no group/other write bits", perm)
		}
	})

	t.Run("missing source errors", func(t *testing.T) {
		err := CopyFileContents(filepath.Join(t.TempDir(), "missing.txt"), filepath.Join(t.TempDir(), "dst.txt"))
		if err == nil || !strings.Contains(err.Error(), "failed to open source file") {
			t.Fatalf("expected source-open error, got %v", err)
		}
	})

	t.Run("missing destination dir errors", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "src.txt")
		if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := CopyFileContents(src, filepath.Join(t.TempDir(), "missing-dir", "dst.txt"))
		if err == nil || !strings.Contains(err.Error(), "failed to create destination file") {
			t.Fatalf("expected destination-create error, got %v", err)
		}
	})
}

func TestCopyDir(t *testing.T) {
	setup := func(t *testing.T, withDotGit bool) (src string) {
		t.Helper()
		src = t.TempDir()
		if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("aaaa"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("bb"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(src, "a.txt"), filepath.Join(src, "link.txt")); err != nil {
			t.Fatal(err)
		}
		if withDotGit {
			if err := os.MkdirAll(filepath.Join(src, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(src, ".git", "HEAD"), []byte("ref: refs/heads/main"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return src
	}

	tests := []struct {
		name           string
		skipDotGit     bool
		withDotGit     bool
		wantFiles      []string
		wantTotalBytes int64
	}{
		{
			name:           "copy without dot-git skipping",
			withDotGit:     true,
			wantFiles:      []string{".git/HEAD", "a.txt", "sub/b.txt"},
			wantTotalBytes: int64(len("ref: refs/heads/main") + len("aaaa") + len("bb")),
		},
		{
			name:           "copy with dot-git skipping",
			skipDotGit:     true,
			withDotGit:     true,
			wantFiles:      []string{"a.txt", "sub/b.txt"},
			wantTotalBytes: int64(len("aaaa") + len("bb")),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := setup(t, tt.withDotGit)
			dst := filepath.Join(t.TempDir(), "dst")

			fileCount, totalBytes, err := CopyDir(src, dst, tt.skipDotGit)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if fileCount != len(tt.wantFiles) {
				t.Fatalf("fileCount = %d, want %d", fileCount, len(tt.wantFiles))
			}
			if totalBytes != tt.wantTotalBytes {
				t.Fatalf("totalBytes = %d, want %d", totalBytes, tt.wantTotalBytes)
			}

			got, err := WalkDirFiles(dst, false)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, ",") != strings.Join(tt.wantFiles, ",") {
				t.Fatalf("copied files = %v, want %v", got, tt.wantFiles)
			}
			// Symlinks are never copied.
			if _, err := os.Lstat(filepath.Join(dst, "link.txt")); !os.IsNotExist(err) {
				t.Fatalf("symlink must not be copied, got err=%v", err)
			}
			// .git is skipped when requested.
			if _, err := os.Stat(filepath.Join(dst, ".git")); tt.skipDotGit != os.IsNotExist(err) {
				t.Fatalf(".git presence mismatch: skipDotGit=%v err=%v", tt.skipDotGit, err)
			}
		})
	}

	t.Run("missing source errors", func(t *testing.T) {
		_, _, err := CopyDir(filepath.Join(t.TempDir(), "missing"), filepath.Join(t.TempDir(), "dst"), false)
		if err == nil {
			t.Fatal("expected error for missing source, got nil")
		}
	})
}

func TestWalkDirFiles(t *testing.T) {
	tests := []struct {
		name       string
		skipDotGit bool
		withDotGit bool
		want       []string
	}{
		{
			name:       "skip dot git",
			skipDotGit: true,
			withDotGit: true,
			want:       []string{"a.txt", "sub/b.txt"},
		},
		{
			name:       "keep dot git contents",
			withDotGit: true,
			want:       []string{".git/HEAD", "a.txt", "sub/b.txt"},
		},
		{
			name:       "no dot git in tree",
			skipDotGit: true,
			want:       []string{"a.txt", "sub/b.txt"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "sub", "b.txt"), []byte("b"), 0o644); err != nil {
				t.Fatal(err)
			}
			if tt.withDotGit {
				if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(filepath.Join(root, "a.txt"), filepath.Join(root, "link.txt")); err != nil {
				t.Fatal(err)
			}

			got, err := WalkDirFiles(root, tt.skipDotGit)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSanitizePathSegment(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		fallback string
		want     string
	}{
		{name: "plain name", s: "owner", fallback: "repo", want: "owner"},
		{name: "empty uses fallback", s: "", fallback: "repo", want: "repo"},
		{name: "dot uses fallback", s: ".", fallback: "session", want: "session"},
		{name: "slash replaced", s: "a/b", fallback: "repo", want: "a_b"},
		{name: "backslash replaced", s: `a\b`, fallback: "repo", want: "a_b"},
		{name: "dot-dot with slash collapsed", s: "../..", fallback: "repo", want: "_"},
		{name: "pure dot-dot uses fallback", s: "..", fallback: "repo", want: "repo"},
		{name: "inner dot-dot collapsed", s: "a..b", fallback: "repo", want: "ab"},
		{name: "null byte removed", s: "a\x00b", fallback: "repo", want: "ab"},
		{name: "control chars removed", s: "a\x01b\x7f", fallback: "repo", want: "ab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizePathSegment(tt.s, tt.fallback); got != tt.want {
				t.Fatalf("SanitizePathSegment(%q, %q) = %q, want %q", tt.s, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestSessionFromContext(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{name: "background context has no session", ctx: context.Background(), want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SessionFromContext(tt.ctx, "any_key"); got != tt.want {
				t.Fatalf("SessionFromContext() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestApplyLineRange(t *testing.T) {
	content := "line1\nline2\nline3\n" // lines: [line1 line2 line3 ""]
	tests := []struct {
		name               string
		startLine          int
		endLine            int
		want               string
		wantStart, wantEnd int
	}{
		{name: "no bounds", startLine: 0, endLine: 0, want: content, wantStart: 1, wantEnd: 4},
		{name: "middle range", startLine: 2, endLine: 3, want: "line2\nline3", wantStart: 2, wantEnd: 3},
		{name: "start only", startLine: 2, endLine: 0, want: "line2\nline3\n", wantStart: 2, wantEnd: 4},
		{name: "end only", startLine: 0, endLine: 2, want: "line1\nline2", wantStart: 1, wantEnd: 2},
		{name: "single line", startLine: 2, endLine: 2, want: "line2", wantStart: 2, wantEnd: 2},
		{name: "start after end", startLine: 5, endLine: 2, want: "", wantStart: 5, wantEnd: 2},
		{name: "out of range clamps to empty", startLine: 100, endLine: 200, want: "", wantStart: 5, wantEnd: 4},
		{name: "end beyond last line", startLine: 1, endLine: 99, want: content, wantStart: 1, wantEnd: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, actualStart, actualEnd := ApplyLineRange(content, tt.startLine, tt.endLine)
			if got != tt.want {
				t.Fatalf("result = %q, want %q", got, tt.want)
			}
			if actualStart != tt.wantStart || actualEnd != tt.wantEnd {
				t.Fatalf("bounds = (%d, %d), want (%d, %d)", actualStart, actualEnd, tt.wantStart, tt.wantEnd)
			}
		})
	}
}
