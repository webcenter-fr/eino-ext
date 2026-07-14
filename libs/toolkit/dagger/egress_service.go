package dagger

import (
	"context"

	"emperror.dev/errors"
	"dagger.io/dagger"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/egress"
)

// EgressProxyService builds the egress proxy as a Dagger Service.
// This is a placeholder for in-engine proxy transport (v2).
// The current egress enforcement uses environment variables (soft control).
func (c *Client) EgressProxyService(ctx context.Context, pol *egress.Policy) (*dagger.Service, error) {
	if c.client == nil {
		return nil, errors.New("client not connected")
	}
	if pol == nil {
		return nil, errors.New("egress policy is nil")
	}
	return c.client.Container().From("alpine:3.20").AsService(), nil
}
