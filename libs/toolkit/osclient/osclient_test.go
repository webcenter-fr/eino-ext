package osclient

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"
)

func TestNewLogsHTTPResponseOnlyAtTrace(t *testing.T) {
	tests := []struct {
		name      string
		level     logrus.Level
		wantDebug bool
	}{
		{name: "panic level disables the HTTP dump", level: logrus.PanicLevel, wantDebug: false},
		{name: "info level disables the HTTP dump", level: logrus.InfoLevel, wantDebug: false},
		{name: "debug level disables the HTTP dump", level: logrus.DebugLevel, wantDebug: false},
		{name: "trace level enables the HTTP dump", level: logrus.TraceLevel, wantDebug: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prev := logrus.GetLevel()
			logrus.SetLevel(tt.level)
			defer logrus.SetLevel(prev)

			client, err := New(context.Background(), Config{
				URLs: []string{"http://localhost:9200"},
			}, 0)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			if got := client.RestyClient().Debug; got != tt.wantDebug {
				t.Errorf("resty debug = %v, want %v", got, tt.wantDebug)
			}
		})
	}
}
