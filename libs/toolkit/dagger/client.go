// Package dagger provides a wrapper around the Dagger engine client for
// building and managing OCI containers with shared cache volumes and egress
// proxy bindings. It is a reusable, non-component library.
//
// The Dagger engine must be reachable. The SDK discovers the engine via:
//   - DAGGER_SESSION_PORT / DAGGER_SESSION_TOKEN env vars (set by `dagger run`)
//   - _EXPERIMENTAL_DAGGER_CLI_BIN (local CLI path)
//   - Auto-downloaded CLI (fallback)
package dagger

import (
	"context"
	"io"

	"emperror.dev/errors"
	"dagger.io/dagger"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// RegistryAuth holds container registry credentials.
type RegistryAuth struct {
	Username string
	Password string
}

// EngineConfig holds configuration for the Dagger engine connection.
type EngineConfig struct {
	RegistryAuth map[string]RegistryAuth `validate:"omitempty" jsonschema:"description=Registry auth credentials keyed by hostname"`
	LogOutput    io.Writer               `validate:"-" json:"-"`
	Workdir      string                  `validate:"omitempty" jsonschema:"description=Project workdir to mount into containers"`
}

// Client is a wrapper around the Dagger engine client.
type Client struct {
	client *dagger.Client
}

var _ io.Closer = (*Client)(nil)

// NewClient connects to the Dagger engine with the given configuration.
func NewClient(ctx context.Context, cfg *EngineConfig) (*Client, error) {
	if cfg == nil {
		cfg = &EngineConfig{}
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}

	var opts []dagger.ClientOpt

	if cfg.LogOutput != nil {
		opts = append(opts, dagger.WithLogOutput(cfg.LogOutput))
	}

	if cfg.Workdir != "" {
		opts = append(opts, dagger.WithWorkdir(cfg.Workdir))
	}

	client, err := dagger.Connect(ctx, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to connect to Dagger engine")
	}

	return &Client{client: client}, nil
}

// Close shuts down the Dagger engine connection.
func (c *Client) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// Version returns the Dagger engine version string.
func (c *Client) Version(ctx context.Context) (string, error) {
	if c.client == nil {
		return "", errors.New("client not connected")
	}
	v, _ := c.client.Version(ctx)
	return v, nil
}

// Dagger returns the underlying Dagger client.
func (c *Client) Dagger() *dagger.Client {
	return c.client
}
