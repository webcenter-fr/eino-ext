package s3

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"time"

	"emperror.dev/errors"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

const defaultS3Timeout = 30 * time.Second

// Client is the interface for S3 operations used by the tools.
// It abstracts the AWS SDK to allow mocking in tests.
type Client interface {
	ListObjectsV2(ctx context.Context, params *s3sdk.ListObjectsV2Input, optFns ...func(*s3sdk.Options)) (*s3sdk.ListObjectsV2Output, error)
	GetBucketLifecycleConfiguration(ctx context.Context, params *s3sdk.GetBucketLifecycleConfigurationInput, optFns ...func(*s3sdk.Options)) (*s3sdk.GetBucketLifecycleConfigurationOutput, error)
}

type s3ClientWrapper struct {
	client *s3sdk.Client
}

func (w *s3ClientWrapper) ListObjectsV2(ctx context.Context, params *s3sdk.ListObjectsV2Input, optFns ...func(*s3sdk.Options)) (*s3sdk.ListObjectsV2Output, error) {
	return w.client.ListObjectsV2(ctx, params, optFns...)
}

func (w *s3ClientWrapper) GetBucketLifecycleConfiguration(ctx context.Context, params *s3sdk.GetBucketLifecycleConfigurationInput, optFns ...func(*s3sdk.Options)) (*s3sdk.GetBucketLifecycleConfigurationOutput, error) {
	return w.client.GetBucketLifecycleConfiguration(ctx, params, optFns...)
}

// NewClient creates a new S3 Client from the given configuration.
func NewClient(ctx context.Context, cfg Config) (Client, error) {
	if err := validate.Struct(&cfg); err != nil {
		return nil, errors.Wrap(err, "invalid S3 config")
	}

	creds := credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")

	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}

	tlsCfg, err := buildTLSConfig(cfg)
	if err != nil {
		return nil, err
	}

	httpClient := &http.Client{
		Timeout: defaultS3Timeout,
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
		},
	}

	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(creds),
		config.WithHTTPClient(httpClient),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load AWS config")
	}

	client := s3sdk.NewFromConfig(awsCfg, func(o *s3sdk.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		if cfg.PathStyle {
			o.UsePathStyle = true
		}
	})

	return &s3ClientWrapper{client: client}, nil
}

// BuildClients creates S3 Clients for all configurations in the Configs map.
func BuildClients(ctx context.Context, configs Configs) (map[string]Client, error) {
	clients := make(map[string]Client, len(configs))
	for instanceName, cfg := range configs {
		client, err := NewClient(ctx, cfg)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create client for instance %s", instanceName)
		}
		clients[instanceName] = client
	}
	return clients, nil
}

func buildTLSConfig(cfg Config) (*tls.Config, error) {
	tlsCfg := &tls.Config{}

	if cfg.TLSSkipVerify {
		tlsCfg.InsecureSkipVerify = true
		return tlsCfg, nil
	}

	if cfg.CACert != "" {
		caCertPool, err := x509.SystemCertPool()
		if err != nil {
			caCertPool = x509.NewCertPool()
		}
		if !caCertPool.AppendCertsFromPEM([]byte(cfg.CACert)) {
			return nil, errors.New("failed to parse CA certificate: invalid PEM data")
		}
		tlsCfg.RootCAs = caCertPool
	}

	return tlsCfg, nil
}
