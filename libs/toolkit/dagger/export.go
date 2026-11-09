package dagger

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"

	"dagger.io/dagger"
	"emperror.dev/errors"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
)

// ExportWorkspace writes the container's /workspace directory back to hostPath,
// excluding .git. It exports to a temporary directory first and then syncs the
// non-.git entries onto hostPath, so a failed or partial export never leaves a
// half-synced tree and the live .git directory is never clobbered.
//
// Deletions performed in the container are propagated: host entries absent from
// the exported tree are removed, except .git.
func (c *Client) ExportWorkspace(ctx context.Context, cont *dagger.Container, hostPath string) error {
	if c.client == nil {
		return errors.New("client not connected")
	}
	if cont == nil {
		return errors.New("container is required")
	}
	if hostPath == "" {
		return errors.New("host workspace path is required")
	}

	tmpDir, err := os.MkdirTemp("", "eino-ext-workspace-export-")
	if err != nil {
		return errors.Wrap(err, "failed to create temporary export directory")
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	if _, err := cont.Directory("/workspace").Export(ctx, tmpDir, dagger.DirectoryExportOpts{Wipe: true}); err != nil {
		return errors.Wrap(err, "failed to export container workspace")
	}

	if err := syncWorkspace(tmpDir, hostPath); err != nil {
		return errors.Wrapf(err, "failed to sync exported workspace to %q", hostPath)
	}
	return nil
}

// syncWorkspace copies non-.git entries from src onto dst and removes dst
// entries that no longer exist in src, excluding .git.
func syncWorkspace(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return errors.Wrapf(err, "failed to create workspace directory %q", dst)
	}
	if _, _, err := fileutil.CopyDir(src, dst, true, 0); err != nil {
		return err
	}
	return removeMissingEntries(src, dst)
}

func removeMissingEntries(src, dst string) error {
	return filepath.WalkDir(dst, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == dst {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}

		rel, err := filepath.Rel(dst, path)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(filepath.Join(src, rel)); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}

		if d.IsDir() {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
			return filepath.SkipDir
		}
		return os.Remove(path)
	})
}
