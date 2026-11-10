package main

import (
	"os"
	"strings"

	"emperror.dev/errors"
	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
	"github.com/urfave/cli/v3"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/secretref"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// fileConfig is the YAML configuration of the approval server. Secret values
// are never stored here: only file/environment references are accepted.
type fileConfig struct {
	Addr                 string `mapstructure:"addr" validate:"omitempty"`
	BaseURL              string `mapstructure:"baseURL" validate:"omitempty,url"`
	Store                string `mapstructure:"store" validate:"omitempty,oneof=memory file redis postgresql opensearch"`
	StoreDir             string `mapstructure:"storeDir" validate:"omitempty,filepath"`
	RedisAddr            string `mapstructure:"redisAddr" validate:"omitempty"`
	PostgresDSNFile      string `mapstructure:"postgresDSNFile" validate:"omitempty,filepath"`
	PostgresDSNEnv       string `mapstructure:"postgresDSNEnv" validate:"omitempty"`
	OpenSearchURL        string `mapstructure:"openSearchURL" validate:"omitempty,url"`
	ReceiptSecretFile    string `mapstructure:"receiptSecretFile" validate:"omitempty,filepath"`
	ReceiptSecretEnv     string `mapstructure:"receiptSecretEnv" validate:"omitempty"`
	ReceiptKeyFile       string `mapstructure:"receiptKeyFile" validate:"omitempty,filepath"`
	ReceiptKeyEnv        string `mapstructure:"receiptKeyEnv" validate:"omitempty"`
	ReceiptAlg           string `mapstructure:"receiptAlg" validate:"omitempty,oneof=HS256 RS256 EdDSA"`
	OIDCIssuer           string `mapstructure:"oidcIssuer" validate:"omitempty,url"`
	OIDCClientID         string `mapstructure:"oidcClientID" validate:"omitempty"`
	OIDCClientSecretFile string `mapstructure:"oidcClientSecretFile" validate:"omitempty,filepath"`
	OIDCClientSecretEnv  string `mapstructure:"oidcClientSecretEnv" validate:"omitempty"`
	OIDCRedirectURL      string `mapstructure:"oidcRedirectURL" validate:"omitempty,url"`
	PolicyFile           string `mapstructure:"policyFile" validate:"omitempty,filepath"`
	ServerCredentialFile string `mapstructure:"serverCredentialFile" validate:"omitempty,filepath"`
	LogLevel             string `mapstructure:"logLevel" validate:"omitempty,oneof=trace debug info warn warning error fatal panic"`
	LogFormat            string `mapstructure:"logFormat" validate:"omitempty,oneof=text json"`
}

// options is the fully resolved runtime configuration.
type options struct {
	addr             string
	baseURL          string
	storeType        string
	storeDir         string
	redisAddr        string
	postgresDSN      string
	opensearchURL    string
	receiptSecret    string
	receiptKey       string
	receiptAlg       string
	oidcIssuer       string
	oidcClientID     string
	oidcClientSecret string
	oidcRedirectURL  string
	policyFile       string
	credentials      []string
	logLevel         string
	logFormat        string
}

const (
	defaultAddr       = ":8080"
	defaultStore      = "memory"
	defaultStoreDir   = "./approval-data"
	defaultRedisAddr  = "localhost:6379"
	defaultReceiptAlg = "HS256"
	defaultLogLevel   = "info"
	defaultLogFormat  = "text"
)

// loadFileConfig builds the non-secret configuration. Secrets are deliberately
// kept out of viper so they never appear in AllSettings or debug dumps.
func loadFileConfig(cmd *cli.Command) (*viper.Viper, *fileConfig, error) {
	v := viper.New()
	v.SetConfigType("yaml")
	v.SetEnvPrefix("EINO_APPROVAL")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	for _, key := range []string{
		"addr", "baseURL", "store", "storeDir", "redisAddr",
		"postgresDSNFile", "postgresDSNEnv", "openSearchURL",
		"receiptSecretFile", "receiptSecretEnv", "receiptKeyFile", "receiptKeyEnv",
		"receiptAlg", "oidcIssuer", "oidcClientID", "oidcClientSecretFile",
		"oidcClientSecretEnv", "oidcRedirectURL", "policyFile",
		"serverCredentialFile", "logLevel", "logFormat",
	} {
		_ = v.BindEnv(key)
	}

	v.SetDefault("addr", defaultAddr)
	v.SetDefault("store", defaultStore)
	v.SetDefault("storeDir", defaultStoreDir)
	v.SetDefault("redisAddr", defaultRedisAddr)
	v.SetDefault("receiptAlg", defaultReceiptAlg)
	v.SetDefault("logLevel", defaultLogLevel)
	v.SetDefault("logFormat", defaultLogFormat)

	if path := cmd.String("config"); path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, nil, errors.Wrapf(err, "failed to read approval config file %q", path)
		}
	}

	for _, f := range []struct {
		flag string
		key  string
	}{
		{"addr", "addr"},
		{"base-url", "baseURL"},
		{"store", "store"},
		{"store-dir", "storeDir"},
		{"redis-addr", "redisAddr"},
		{"opensearch-url", "openSearchURL"},
		{"receipt-alg", "receiptAlg"},
		{"oidc-issuer", "oidcIssuer"},
		{"oidc-client-id", "oidcClientID"},
		{"oidc-redirect-url", "oidcRedirectURL"},
		{"policy-file", "policyFile"},
		{"log-level", "logLevel"},
		{"log-format", "logFormat"},
	} {
		if cmd.IsSet(f.flag) {
			v.Set(f.key, cmd.String(f.flag))
		}
	}

	cfg := &fileConfig{}
	if err := v.Unmarshal(cfg, func(dc *mapstructure.DecoderConfig) {
		dc.ErrorUnused = true
	}); err != nil {
		return nil, nil, errors.Wrap(err, "failed to decode approval config")
	}
	applyFileDefaults(cfg)
	if err := validate.Struct(cfg); err != nil {
		return nil, nil, err
	}
	if cfg.Store == "opensearch" && cfg.OpenSearchURL == "" {
		return nil, nil, errors.New("approval.store.type=opensearch requires openSearchURL")
	}
	return v, cfg, nil
}

func loadOptions(cmd *cli.Command) (*options, error) {
	_, cfg, err := loadFileConfig(cmd)
	if err != nil {
		return nil, err
	}

	receiptSecret, err := secretref.Resolve(
		firstNonEmpty(cmd.String("receipt-secret-file"), cfg.ReceiptSecretFile),
		firstNonEmpty(cfg.ReceiptSecretEnv, "EINO_APPROVAL_RECEIPT_SECRET"),
		cmd.String("receipt-secret"),
		"receipt secret",
	)
	if err != nil {
		return nil, err
	}
	receiptKey, err := secretref.Resolve(
		firstNonEmpty(cmd.String("receipt-key-file"), cfg.ReceiptKeyFile),
		firstNonEmpty(cfg.ReceiptKeyEnv, "EINO_APPROVAL_RECEIPT_KEY"),
		cmd.String("receipt-key"),
		"receipt private key",
	)
	if err != nil {
		return nil, err
	}
	oidcClientSecret, err := secretref.Resolve(
		firstNonEmpty(cmd.String("oidc-client-secret-file"), cfg.OIDCClientSecretFile),
		firstNonEmpty(cfg.OIDCClientSecretEnv, "EINO_APPROVAL_OIDC_CLIENT_SECRET"),
		cmd.String("oidc-client-secret"),
		"OIDC client secret",
	)
	if err != nil {
		return nil, err
	}
	postgresDSN, err := secretref.Resolve(
		firstNonEmpty(cmd.String("postgres-dsn-file"), cfg.PostgresDSNFile),
		firstNonEmpty(cfg.PostgresDSNEnv, "EINO_APPROVAL_POSTGRES_DSN"),
		cmd.String("postgres-dsn"),
		"PostgreSQL DSN",
	)
	if err != nil {
		return nil, err
	}
	if cfg.Store == "postgresql" && postgresDSN == "" {
		return nil, errors.New("approval.store.type=postgresql requires a PostgreSQL DSN")
	}

	credentials := make([]string, 0)
	if file := firstNonEmpty(cmd.String("server-credential-file"), cfg.ServerCredentialFile); file != "" {
		lines, err := readCredentialFile(file)
		if err != nil {
			return nil, err
		}
		credentials = append(credentials, lines...)
	}
	credentials = append(credentials, cmd.StringSlice("server-credential")...)

	return &options{
		addr:             cfg.Addr,
		baseURL:          cfg.BaseURL,
		storeType:        cfg.Store,
		storeDir:         cfg.StoreDir,
		redisAddr:        cfg.RedisAddr,
		postgresDSN:      postgresDSN,
		opensearchURL:    cfg.OpenSearchURL,
		receiptSecret:    receiptSecret,
		receiptKey:       receiptKey,
		receiptAlg:       cfg.ReceiptAlg,
		oidcIssuer:       cfg.OIDCIssuer,
		oidcClientID:     cfg.OIDCClientID,
		oidcClientSecret: oidcClientSecret,
		oidcRedirectURL:  cfg.OIDCRedirectURL,
		policyFile:       cfg.PolicyFile,
		credentials:      credentials,
		logLevel:         cfg.LogLevel,
		logFormat:        cfg.LogFormat,
	}, nil
}

func applyFileDefaults(cfg *fileConfig) {
	if cfg.Addr == "" {
		cfg.Addr = defaultAddr
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL(cfg.Addr)
	}
	if cfg.Store == "" {
		cfg.Store = defaultStore
	}
	if cfg.StoreDir == "" {
		cfg.StoreDir = defaultStoreDir
	}
	if cfg.RedisAddr == "" {
		cfg.RedisAddr = defaultRedisAddr
	}
	if cfg.ReceiptAlg == "" {
		cfg.ReceiptAlg = defaultReceiptAlg
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = defaultLogLevel
	}
	if cfg.LogFormat == "" {
		cfg.LogFormat = defaultLogFormat
	}
}

// readCredentialFile parses one name=token[@instance,...] entry per line.
// Blank lines and lines starting with '#' are skipped.
func readCredentialFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read server credential file %q", path)
	}
	lines := make([]string, 0)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return lines, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
