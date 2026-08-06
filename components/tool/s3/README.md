# S3 Tools

eino tools for browsing and analyzing AWS S3 and S3-compatible object storage
(MinIO, Cloudian, etc.).

## Design

- **Multi-instance** — configure multiple named S3 bucket instances via a
  `Configs` map, each with its own endpoint, credentials, and description.
- **AWS-compatible** — works with AWS S3, MinIO, Cloudian, and any S3-compatible
  service via custom endpoint configuration.
- **TLS** — supports custom CA certificates via `CACert` and TLS verification
  skip via `TLSSkipVerify`.
- **Sorting** — list tools support sorting by name (alphanumeric), size
  (largest first), or last modified date (most recent first).
- **Context descriptions** — each bucket instance includes a `Description` field
  exposed to LLM agents via `s3_list_buckets`.
- **Read-only** — all tools are read-only.

## TLS Configuration

- `TLSSkipVerify` disables TLS certificate verification. Use **only** for local
  development or trusted internal endpoints. For production, prefer supplying the
  CA certificate via `CACert`.
- `CACert` is a PEM-encoded CA certificate used to validate the endpoint's TLS
  certificate. Use it when the endpoint uses a private/internal CA. It is not
  serialized to JSON.
- `PathStyle` forces path-style addressing. Enable it for MinIO/Cloudian or
  other S3-compatible services that do not support virtual-hosted style. Leave it
  false (the default) for AWS S3.

## Configuration

```go
import "github.com/webcenter-fr/eino-ext/components/tool/s3"

configs := s3.Configs{
    "prod-logs": s3.Config{
        Endpoint:    "https://s3.amazonaws.com",
        BucketName:  "my-logs-bucket",
        AccessKey:   os.Getenv("AWS_ACCESS_KEY_ID"),
        SecretKey:   os.Getenv("AWS_SECRET_ACCESS_KEY"),
        Region:      "us-east-1",
        Description: "Production application logs",
    },
    "minio-backups": s3.Config{
        Endpoint:      "http://minio:9000",
        BucketName:    "backups",
        AccessKey:     "minioadmin",
        SecretKey:     "minioadmin",
        Region:        "us-east-1",
        TLSSkipVerify: true,
        PathStyle:     true,
        Description:   "Backup storage on MinIO",
    },
}
```

## Available Tools

| Tool Name | Description |
|---|---|
| `s3_list_buckets` | List all configured bucket instances with names, endpoints, and descriptions |
| `s3_list_objects` | List objects and directories in a bucket with sorting and filtering |
| `s3_get_usage` | Compute total storage usage (size + object count) in human-readable units |
| `s3_list_objects_with_size` | List objects with detailed size information, sorted by size by default |
| `s3_get_lifecycle` | Retrieve lifecycle configuration to understand data retention/cleanup policies |

## Factory Functions

```go
// All tools
tools, err := s3.NewAllTools(ctx, configs)

// Read-only tools
tools, err := s3.NewReadOnlyTools(ctx, configs)

// All tools with safety middleware
tools, mw, err := s3.NewAllToolsWithSafety(ctx, configs, safetyCfg)
```

## Tool Details

### s3_list_buckets

Lists all configured S3 bucket instances with their descriptions.

No parameters required.

### s3_list_objects

Lists directories and/or files in a bucket.

| Parameter | Required | Description |
|---|---|---|
| `instance` | Yes | S3 bucket instance name |
| `prefix` | No | List only objects with this path prefix |
| `delimiter` | No | Use `/` to group into directories |
| `max_keys` | No | Max results (1–1000, default 200) |
| `sort_by` | No | `alphanumeric`, `size`, or `last_modified` |
| `filter` | No | Go RE2 regex filter on result JSON |

### s3_get_usage

Computes total storage usage for a bucket.

| Parameter | Required | Description |
|---|---|---|
| `instance` | Yes | S3 bucket instance name |

Returns `total_objects`, `total_size_bytes`, and `total_size_human`.

### s3_list_objects_with_size

Lists objects with detailed size info, sorted by size descending by default.

| Parameter | Required | Description |
|---|---|---|
| `instance` | Yes | S3 bucket instance name |
| `prefix` | No | List only objects with this path prefix |
| `max_keys` | No | Max results (1–1000, default 200) |
| `sort_by` | No | `alphanumeric`, `size` (default), or `last_modified` |
| `filter` | No | Go RE2 regex filter on result JSON |

### s3_get_lifecycle

Retrieves lifecycle configuration to check for automatic data expiration
or storage tier transitions.

| Parameter | Required | Description |
|---|---|---|
| `instance` | Yes | S3 bucket instance name |
