package s3

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/goccy/go-json"
)

type mockClient struct {
	listObjectsV2Fn                   func(ctx context.Context, params *s3sdk.ListObjectsV2Input, optFns ...func(*s3sdk.Options)) (*s3sdk.ListObjectsV2Output, error)
	getBucketLifecycleConfigurationFn func(ctx context.Context, params *s3sdk.GetBucketLifecycleConfigurationInput, optFns ...func(*s3sdk.Options)) (*s3sdk.GetBucketLifecycleConfigurationOutput, error)
}

func (m *mockClient) ListObjectsV2(ctx context.Context, params *s3sdk.ListObjectsV2Input, optFns ...func(*s3sdk.Options)) (*s3sdk.ListObjectsV2Output, error) {
	return m.listObjectsV2Fn(ctx, params, optFns...)
}

func (m *mockClient) GetBucketLifecycleConfiguration(ctx context.Context, params *s3sdk.GetBucketLifecycleConfigurationInput, optFns ...func(*s3sdk.Options)) (*s3sdk.GetBucketLifecycleConfigurationOutput, error) {
	return m.getBucketLifecycleConfigurationFn(ctx, params, optFns...)
}

func testNow() time.Time {
	t, _ := time.Parse(time.RFC3339, "2024-01-15T10:00:00Z")
	return t
}

func makeTestObjects() []types.Object {
	now := testNow()
	return []types.Object{
		{Key: aws.String("logs/2024/access.log"), Size: aws.Int64(1024 * 1024 * 100), LastModified: aws.Time(now)},
		{Key: aws.String("logs/2024/error.log"), Size: aws.Int64(1024 * 1024 * 10), LastModified: aws.Time(now.Add(-1 * time.Hour))},
		{Key: aws.String("data/file1.csv"), Size: aws.Int64(1024 * 1024 * 500), LastModified: aws.Time(now.Add(-2 * 24 * time.Hour))},
		{Key: aws.String("data/file2.json"), Size: aws.Int64(1024 * 1024 * 50), LastModified: aws.Time(now.Add(-3 * 24 * time.Hour))},
		{Key: aws.String("backups/db-backup.tar.gz"), Size: aws.Int64(1024 * 1024 * 1024 * 5), LastModified: aws.Time(now.Add(-5 * 24 * time.Hour))},
		{Key: aws.String("small.txt"), Size: aws.Int64(512), LastModified: aws.Time(now.Add(-1 * 24 * time.Hour))},
	}
}

func makeCommonPrefixes() []types.CommonPrefix {
	return []types.CommonPrefix{
		{Prefix: aws.String("logs/")},
		{Prefix: aws.String("data/")},
		{Prefix: aws.String("backups/")},
	}
}

func newMockListObjectsClient(objects []types.Object, prefixes []types.CommonPrefix, isTruncated bool) *mockClient {
	return &mockClient{
		listObjectsV2Fn: func(ctx context.Context, params *s3sdk.ListObjectsV2Input, optFns ...func(*s3sdk.Options)) (*s3sdk.ListObjectsV2Output, error) {
			return &s3sdk.ListObjectsV2Output{
				Contents:              objects,
				CommonPrefixes:        prefixes,
				IsTruncated:           aws.Bool(isTruncated),
				NextContinuationToken: nil,
			}, nil
		},
	}
}

func newMockEmptyListClient() *mockClient {
	return &mockClient{
		listObjectsV2Fn: func(ctx context.Context, params *s3sdk.ListObjectsV2Input, optFns ...func(*s3sdk.Options)) (*s3sdk.ListObjectsV2Output, error) {
			return &s3sdk.ListObjectsV2Output{
				Contents:       []types.Object{},
				CommonPrefixes: []types.CommonPrefix{},
				IsTruncated:    aws.Bool(false),
			}, nil
		},
	}
}

func newMockLifecycleClient(rules []types.LifecycleRule, err error) *mockClient {
	return &mockClient{
		getBucketLifecycleConfigurationFn: func(ctx context.Context, params *s3sdk.GetBucketLifecycleConfigurationInput, optFns ...func(*s3sdk.Options)) (*s3sdk.GetBucketLifecycleConfigurationOutput, error) {
			if err != nil {
				return nil, err
			}
			return &s3sdk.GetBucketLifecycleConfigurationOutput{
				Rules: rules,
			}, nil
		},
		listObjectsV2Fn: func(ctx context.Context, params *s3sdk.ListObjectsV2Input, optFns ...func(*s3sdk.Options)) (*s3sdk.ListObjectsV2Output, error) {
			return &s3sdk.ListObjectsV2Output{
				Contents:       []types.Object{},
				CommonPrefixes: []types.CommonPrefix{},
				IsTruncated:    aws.Bool(false),
			}, nil
		},
	}
}

func mustMarshal(t requireT, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	return string(b)
}

type requireT interface {
	Helper()
	Fatalf(format string, args ...interface{})
}

func testConfigs() Configs {
	return Configs{
		"prod-logs": {
			BucketName:  "my-logs-bucket",
			Endpoint:    "https://s3.amazonaws.com",
			AccessKey:   "AKIATEST",
			SecretKey:   "test-secret",
			Region:      "us-east-1",
			Description: "Production application logs",
		},
		"staging-data": {
			BucketName:  "staging-data-bucket",
			Endpoint:    "http://minio:9000",
			AccessKey:   "AKIAMINIO",
			SecretKey:   "minio-secret",
			Region:      "us-east-1",
			Description: "Staging environment data files",
		},
	}
}
