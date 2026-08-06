// Package s3 provides eino tools for browsing and analyzing AWS S3 and
// S3-compatible object storage buckets.
//
// Supports AWS S3, MinIO, Cloudian, and other S3-compatible services.
package s3

import "github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"

// Configs is a map of S3 bucket instance configurations, where the key is the
// logical instance name (exposed to the LLM via s3_list_buckets).
type Configs map[string]Config

// Config holds the connection and identity configuration for a single S3 bucket.
type Config struct {
	// Endpoint is the S3 server URL (e.g. "https://s3.amazonaws.com", "http://minio:9000").
	// For AWS S3, this can be left empty to use the default regional endpoint.
	Endpoint string `validate:"omitempty,http_url" jsonschema:"description=S3 server URL, e.g. https://s3.amazonaws.com or http://minio:9000. Leave empty for default AWS regional endpoint."`

	// BucketName is the actual bucket name on the S3 server.
	BucketName string `validate:"required" jsonschema:"description=Actual S3 bucket name, e.g. my-logs-bucket"`

	// AccessKey is the S3 access key ID.
	AccessKey string `json:"-" validate:"required"`

	// SecretKey is the S3 secret access key.
	SecretKey string `json:"-" validate:"required"`

	// Region is the AWS region (e.g. "us-east-1"). For S3-compatible services,
	// this can often be set to "us-east-1" or any value accepted by the server.
	Region string `validate:"omitempty" jsonschema:"description=AWS region, e.g. us-east-1. For S3-compatible services, use us-east-1 or any valid region."`

	// PathStyle forces path-style addressing (s3.amazonaws.com/bucket/key) instead
	// of virtual-hosted style (bucket.s3.amazonaws.com/key). Required for MinIO,
	// Cloudian, and other S3-compatible services. Leave false for AWS S3.
	PathStyle bool `jsonschema:"description=Use path-style addressing. Required for MinIO and other S3-compatible services. Leave false for AWS S3."`

	// Description provides context about this bucket for LLM agents.
	// This is exposed by the s3_list_buckets tool.
	Description string `jsonschema:"description=Human-readable description of this bucket to help LLM agents understand its purpose."`

	// TLSSkipVerify disables TLS certificate verification.
	TLSSkipVerify bool

	// CACert is a PEM-encoded CA certificate used to validate the endpoint's TLS certificate.
	CACert string `json:"-"`
}

// GetConfig retrieves the configuration for a given instance name.
func (c Configs) GetConfig(instanceName string) Config {
	return c[instanceName]
}

// GetInstanceNames returns a sorted slice of all instance names in the Configs map.
func (c Configs) GetInstanceNames() []string {
	return toolutil.SortedKeys(c)
}
