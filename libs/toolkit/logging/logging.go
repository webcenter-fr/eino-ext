// Package logging configures the shared logrus logger from a level and format.
package logging

import (
	"strings"

	"emperror.dev/errors"
	"github.com/sirupsen/logrus"
)

// Configure sets the standard logger's level and formatter.
//
// level accepts any logrus level name (trace, debug, info, warn, error,
// fatal, panic); an empty level defaults to "info". format accepts "text" or
// "json"; an empty format defaults to "text".
func Configure(level, format string) error {
	if level == "" {
		level = "info"
	}
	parsed, err := logrus.ParseLevel(strings.ToLower(level))
	if err != nil {
		return errors.Wrapf(err, "invalid log level %q", level)
	}

	if format == "" {
		format = "text"
	}
	switch strings.ToLower(format) {
	case "text":
		logrus.SetFormatter(&logrus.TextFormatter{})
	case "json":
		logrus.SetFormatter(&logrus.JSONFormatter{})
	default:
		return errors.Errorf("invalid log format %q: must be one of text, json", format)
	}

	logrus.SetLevel(parsed)
	return nil
}
