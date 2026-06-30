package jsoncrush

import (
	"context"

	"github.com/webcenter-fr/eino-ext/libs/contentcomp"
)

// compressor adapts Crush to the contentcomp.Compressor interface so it can be
// plugged into contextopt.Config.ContentCompressors.
type compressor struct {
	opts []Option
}

var _ contentcomp.Compressor = (*compressor)(nil)

// NewCompressor returns a contentcomp.Compressor backed by Crush. Pass WithStore
// to enable the lossy stage.
func NewCompressor(opts ...Option) contentcomp.Compressor {
	return &compressor{opts: opts}
}

func (c *compressor) Name() string { return "jsoncrush" }

// Compress crushes JSON-array-of-object content; non-matching content passes
// through unchanged (changed == false). It is idempotent: already-crushed content
// is left untouched.
func (c *compressor) Compress(ctx context.Context, content string) (string, bool, error) {
	if IsCrushed(content) {
		return content, false, nil
	}
	out, _, err := Crush(ctx, content, c.opts...)
	if err != nil {
		return "", false, err
	}
	return out, out != content, nil
}
