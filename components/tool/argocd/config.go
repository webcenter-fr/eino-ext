package argocd

import (
	"github.com/goccy/go-json"
	"github.com/thoas/go-funk"
)

type Config struct {
	ServerURL string `json:"serverURL" validate:"required"`
	Token     string `json:"token" validate:"required"`
	Insecure  bool   `json:"insecure,omitempty"`
}

func (c Config) MarshalJSON() ([]byte, error) {
	type Alias Config
	return json.Marshal(&struct {
		Token string `json:"token"`
		*Alias
	}{
		Token: "REDACTED",
		Alias: (*Alias)(&c),
	})
}

type Configs map[string]*Config

func (c Configs) GetConfig(instanceName string) *Config {
	return c[instanceName]
}

func (c Configs) GetInstanceNames() []string {
	return funk.Keys(c).([]string)
}
