// Package github provides a document loader for GitHub source code repositories.
package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	urlloader "github.com/cloudwego/eino-ext/components/document/loader/url"
	"github.com/cloudwego/eino/components/document"
)

// NewGithubLoader creates a GitHub document loader. When token is empty it
// behaves as a plain URL loader; otherwise it adds a Bearer Authorization
// header on every request.
func NewGithubLoader(ctx context.Context, token string) (document.Loader, error) {
	if token == "" {
		return urlloader.NewLoader(ctx, &urlloader.LoaderConfig{})
	}
	return urlloader.NewLoader(ctx, &urlloader.LoaderConfig{
		RequestBuilder: func(ctx context.Context, source document.Source, opts ...document.LoaderOption) (*http.Request, error) {
			u, err := url.Parse(source.URI)
			if err != nil {
				return nil, err
			}
			req := &http.Request{Method: "GET", URL: u, Header: make(http.Header)}
			req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", token))
			return req, nil
		},
	})
}
