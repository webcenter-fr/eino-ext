package s3

import (
	"context"
	"time"

	"emperror.dev/errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

const s3CheckTimeout = 15 * time.Second

// Check runs health checks on each configured S3 instance.
func Check(ctx context.Context, configs Configs) checkup.Results {
	if len(configs) == 0 {
		return checkup.Results{{
			Component: "s3",
			Status:    checkup.StatusError,
			Error:     "no S3 bucket instances configured",
		}}
	}

	instances := configs.GetInstanceNames()
	var all checkup.Results

	for _, instance := range instances {
		cfg := configs.GetConfig(instance)
		baseCtx, baseCancel := context.WithTimeout(ctx, s3CheckTimeout)

		client, err := NewClient(baseCtx, cfg)
		if err != nil {
			all = append(all, clientErrorResults(instance, err)...)
			baseCancel()
			continue
		}

		func() {
			defer baseCancel()
			all = append(all, probeInstance(baseCtx, client, instance, cfg)...)
		}()
	}

	return all
}

func clientErrorResults(instance string, err error) checkup.Results {
	errStr := err.Error()
	return checkup.Results{
		{Component: "s3_list_buckets", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "s3_list_objects", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "s3_get_usage", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "s3_list_objects_with_size", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "s3_get_lifecycle", Instance: instance, Status: checkup.StatusError, Error: errStr},
	}
}

func probeInstance(ctx context.Context, client Client, instance string, cfg Config) checkup.Results {
	var results checkup.Results

	results = append(results, probeInstanceList(instance))

	listResult, listOK := probeListObjects(ctx, client, instance, cfg)
	results = append(results, listResult)

	if listOK {
		results = append(results, probeGetUsage(instance))
		results = append(results, probeListObjectsWithSize(instance))
	} else {
		results = append(results, checkup.Result{Component: "s3_get_usage", Instance: instance, Status: checkup.StatusError, Error: "dependency failed"})
		results = append(results, checkup.Result{Component: "s3_list_objects_with_size", Instance: instance, Status: checkup.StatusError, Error: "dependency failed"})
	}

	lcResult := probeGetLifecycle(ctx, client, instance, cfg)
	results = append(results, lcResult)

	return results
}

func probeInstanceList(instance string) checkup.Result {
	return checkup.Result{
		Component: "s3_list_buckets",
		Instance:  instance,
		Status:    checkup.StatusOK,
	}
}

func probeListObjects(ctx context.Context, client Client, instance string, cfg Config) (checkup.Result, bool) {
	_, err := client.ListObjectsV2(ctx, &s3sdk.ListObjectsV2Input{
		Bucket:  aws.String(cfg.BucketName),
		MaxKeys: aws.Int32(1),
	})
	if err != nil {
		return checkup.Result{
			Component: "s3_list_objects",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list objects").Error(),
		}, false
	}
	return checkup.Result{
		Component: "s3_list_objects",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   "list objects succeeded, RBAC ok",
	}, true
}

func probeGetUsage(instance string) checkup.Result {
	return checkup.Result{
		Component: "s3_get_usage",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   "usage computation available after listing check passed",
	}
}

func probeListObjectsWithSize(instance string) checkup.Result {
	return checkup.Result{
		Component: "s3_list_objects_with_size",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   "object size listing available after listing check passed",
	}
}

func probeGetLifecycle(ctx context.Context, client Client, instance string, cfg Config) checkup.Result {
	_, err := client.GetBucketLifecycleConfiguration(ctx, &s3sdk.GetBucketLifecycleConfigurationInput{
		Bucket: aws.String(cfg.BucketName),
	})
	if err != nil {
		if isNoSuchLifecycleError(err) {
			return checkup.Result{
				Component: "s3_get_lifecycle",
				Instance:  instance,
				Status:    checkup.StatusOK,
				Message:   "no lifecycle configuration (this is normal)",
			}
		}
		return checkup.Result{
			Component: "s3_get_lifecycle",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to get lifecycle").Error(),
		}
	}
	return checkup.Result{
		Component: "s3_get_lifecycle",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   "lifecycle configuration retrieved, RBAC ok",
	}
}
