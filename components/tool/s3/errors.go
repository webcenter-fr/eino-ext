package s3

import (
	"errors"

	"github.com/aws/smithy-go"
)

func isNoSuchLifecycleError(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "NoSuchLifecycleConfiguration"
	}
	return false
}
