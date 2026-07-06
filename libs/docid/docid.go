// Package docid provides shared helpers to compute deterministic document
// base IDs and content hashes, usable both inside indexing graphs (as a
// lambda) and outside of them (e.g. in pre-checks) so the two never diverge.
package docid

import (
	"encoding/hex"

	"emperror.dev/errors"
	"github.com/google/uuid"
	"github.com/zeebo/xxh3"
)

// ComputeBaseID computes a deterministic UUID (derived from a 128 bit xxh3
// hash) from a stable identifier (e.g. a repo-relative path or a source URL).
// The same input always produces the same output, required to keep upserts
// landing on stable document IDs across runs.
func ComputeBaseID(identifier string) (string, error) {
	hash := xxh3.HashString128(identifier).Bytes()
	base, err := uuid.FromBytes(hash[:])
	if err != nil {
		return "", errors.Wrap(err, "failed to generate UUID from identifier")
	}
	return base.String(), nil
}

// ComputeContentHash computes a deterministic content hash (hex encoded
// 128 bit xxh3) from raw bytes. Used to decide whether a source has changed.
func ComputeContentHash(content []byte) string {
	hash := xxh3.Hash128(content).Bytes()
	return hex.EncodeToString(hash[:])
}
