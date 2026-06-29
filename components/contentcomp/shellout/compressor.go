package shellout

import (
	"context"

	"github.com/webcenter-fr/eino-ext/components/contentcomp"
)

// compressor adapts Compress to the contentcomp.Compressor interface.
type compressor struct {
	opts []Option
}

var _ contentcomp.Compressor = (*compressor)(nil)

// NewCompressor returns a contentcomp.Compressor backed by Compress.
func NewCompressor(opts ...Option) contentcomp.Compressor {
	return &compressor{opts: opts}
}

func (c *compressor) Name() string { return "shellout" }

func (c *compressor) Compress(ctx context.Context, content string) (string, bool, error) {
	out, _, err := Compress(ctx, content, c.opts...)
	if err != nil {
		return "", false, err
	}
	return out, out != content, nil
}
