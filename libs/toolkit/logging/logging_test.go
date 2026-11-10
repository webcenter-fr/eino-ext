package logging

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigure(t *testing.T) {
	t.Cleanup(func() {
		logrus.SetLevel(logrus.InfoLevel)
		logrus.SetFormatter(&logrus.TextFormatter{})
	})

	tests := []struct {
		name       string
		level      string
		format     string
		wantLevel  logrus.Level
		wantFormat any
		wantErr    bool
	}{
		{name: "defaults", wantLevel: logrus.InfoLevel, wantFormat: &logrus.TextFormatter{}},
		{name: "debug text", level: "debug", format: "text", wantLevel: logrus.DebugLevel, wantFormat: &logrus.TextFormatter{}},
		{name: "warn json", level: "warn", format: "json", wantLevel: logrus.WarnLevel, wantFormat: &logrus.JSONFormatter{}},
		{name: "case insensitive", level: "ERROR", format: "JSON", wantLevel: logrus.ErrorLevel, wantFormat: &logrus.JSONFormatter{}},
		{name: "invalid level", level: "verbose", wantErr: true},
		{name: "invalid format", format: "xml", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Configure(tt.level, tt.format)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantLevel, logrus.GetLevel())
			assert.IsType(t, tt.wantFormat, logrus.StandardLogger().Formatter)
		})
	}
}
