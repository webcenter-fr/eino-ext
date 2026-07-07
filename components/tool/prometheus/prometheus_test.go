package prometheus

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfigs_GetInstanceNames(t *testing.T) {
	t.Run("empty configs", func(t *testing.T) {
		configs := Configs{}
		names := configs.GetInstanceNames()
		assert.Empty(t, names)
	})

	t.Run("single instance", func(t *testing.T) {
		configs := Configs{
			"prod": {Address: "http://localhost:9090"},
		}
		names := configs.GetInstanceNames()
		assert.Equal(t, []string{"prod"}, names)
	})

	t.Run("multiple instances sorted", func(t *testing.T) {
		configs := Configs{
			"prod":    {Address: "http://prod:9090"},
			"staging": {Address: "http://staging:9090"},
			"dev":     {Address: "http://dev:9090"},
		}
		names := configs.GetInstanceNames()
		assert.Equal(t, []string{"dev", "prod", "staging"}, names)
	})
}

func TestConfigs_GetConfig(t *testing.T) {
	t.Run("existing instance", func(t *testing.T) {
		configs := Configs{
			"prod": {
				Address:            "http://prod:9090",
				Username:           "admin",
				Password:           "secret",
				BearerToken:        "token123",
				TLSSkipVerify: true,
			},
		}
		cfg := configs.GetConfig("prod")
		assert.Equal(t, "http://prod:9090", cfg.Address)
		assert.Equal(t, "admin", cfg.Username)
		assert.Equal(t, "secret", cfg.Password)
		assert.Equal(t, "token123", cfg.BearerToken)
		assert.True(t, cfg.TLSSkipVerify)
	})

	t.Run("non-existent instance returns zero value", func(t *testing.T) {
		configs := Configs{
			"prod": {Address: "http://prod:9090"},
		}
		cfg := configs.GetConfig("nonexistent")
		assert.Equal(t, Config{}, cfg)
	})
}

func TestInstanceNotFoundError(t *testing.T) {
	t.Run("formats instance name and known instances", func(t *testing.T) {
		err := instanceNotFoundError("staging", []string{"dev", "prod"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "staging")
		assert.Contains(t, err.Error(), "dev, prod")
	})

	t.Run("empty known instances", func(t *testing.T) {
		err := instanceNotFoundError("unknown", []string{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unknown")
	})
}

func TestValidateParams(t *testing.T) {
	t.Run("valid params", func(t *testing.T) {
		params := &AlertListParams{
			Instance: "prod",
		}
		err := validateParams(params)
		assert.NoError(t, err)
	})

	t.Run("valid params with state", func(t *testing.T) {
		params := &AlertListParams{
			Instance: "prod",
			State:    "firing",
		}
		err := validateParams(params)
		assert.NoError(t, err)
	})

	t.Run("valid params with pagination", func(t *testing.T) {
		params := &AlertListParams{
			Instance: "prod",
			Paginate: &AlertListPaginate{
				PageSize: 10,
			},
		}
		err := validateParams(params)
		assert.NoError(t, err)
	})

	t.Run("missing required instance", func(t *testing.T) {
		params := &AlertListParams{}
		err := validateParams(params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid parameters")
	})

	t.Run("invalid state value", func(t *testing.T) {
		params := &AlertListParams{
			Instance: "prod",
			State:    "invalid-state",
		}
		err := validateParams(params)
		assert.Error(t, err)
	})

	t.Run("page size below minimum", func(t *testing.T) {
		params := &AlertListParams{
			Instance: "prod",
			Paginate: &AlertListPaginate{
				PageSize: -1,
			},
		}
		err := validateParams(params)
		assert.Error(t, err)
	})

	t.Run("page size above maximum", func(t *testing.T) {
		params := &AlertListParams{
			Instance: "prod",
			Paginate: &AlertListPaginate{
				PageSize: 501,
			},
		}
		err := validateParams(params)
		assert.Error(t, err)
	})
}
