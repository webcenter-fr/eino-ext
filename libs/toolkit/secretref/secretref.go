// Package secretref resolves secret references from files, environment
// variables, or literal values.
package secretref

import (
	"os"
	"strings"

	"emperror.dev/errors"
	"github.com/sirupsen/logrus"
)

// MaxBytes bounds a secret file so a misconfigured path cannot exhaust memory.
const MaxBytes = 64 << 10

// Resolve reads a secret from a file, an environment variable, or a literal
// value, in that order. A file wins when both file and env are set. The value
// is never logged.
func Resolve(file, env, literal, name string) (string, error) {
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", errors.Wrapf(err, "failed to read %s file %q", name, file)
		}
		if len(data) > MaxBytes {
			return "", errors.Errorf("%s file %q exceeds %d bytes", name, file, MaxBytes)
		}
		value := strings.TrimRight(string(data), "\r\n")
		if value == "" {
			return "", errors.Errorf("%s file %q is empty", name, file)
		}
		logrus.Debugf("secretref: resolved %s from file", name)
		return value, nil
	}
	if env != "" {
		if value, ok := os.LookupEnv(env); ok {
			value = strings.TrimRight(value, "\r\n")
			if value == "" {
				return "", errors.Errorf("%s environment variable %q is empty", name, env)
			}
			logrus.Debugf("secretref: resolved %s from environment", name)
			return value, nil
		}
	}
	return literal, nil
}
