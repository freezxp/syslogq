package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// EnvPrefix is the namespace for environment overrides. Nested keys use a
// double underscore: SYSLOGQ_INGESTION__QUEUE_CAPACITY=10000.
const EnvPrefix = "SYSLOGQ_"

// PathEnv names the env var pointing at the config file.
const PathEnv = "SYSLOGQ_CONFIG"

// Load builds the configuration with precedence defaults < YAML file <
// environment (docs/architecture.md §2.7). An empty path skips the file.
func Load(path string) (*Config, error) {
	k := koanf.New(".")

	if err := k.Load(confmap.Provider(defaults(), "."), nil); err != nil {
		return nil, fmt.Errorf("config: defaults: %w", err)
	}
	if path != "" {
		if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("config: load %s: %w", path, err)
		}
	}
	envProvider := env.Provider(EnvPrefix, ".", func(name string) string {
		name = strings.TrimPrefix(name, EnvPrefix)
		return strings.ToLower(strings.ReplaceAll(name, "__", "."))
	})
	if err := k.Load(envProvider, nil); err != nil {
		return nil, fmt.Errorf("config: env: %w", err)
	}

	var cfg Config
	if err := k.UnmarshalWithConf("", &cfg, koanf.UnmarshalConf{
		DecoderConfig: &mapstructure.DecoderConfig{
			DecodeHook: mapstructure.ComposeDecodeHookFunc(
				mapstructure.StringToTimeDurationHookFunc(),
				mapstructure.StringToSliceHookFunc(","),
			),
			WeaklyTypedInput: true,
			Result:           &cfg,
			TagName:          "koanf",
		},
	}); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}

	cfg.postProcess()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// LoadPath resolves the config file path: explicit argument, else the
// SYSLOGQ_CONFIG env var, else "" (defaults only).
func LoadPath(flagPath string) string {
	if flagPath != "" {
		return flagPath
	}
	return os.Getenv(PathEnv)
}

func (c *Config) postProcess() {
	if c.Ingestion.Workers <= 0 {
		c.Ingestion.Workers = defaultWorkers()
	}
	if c.Ingestion.QueuePolicy == "" {
		c.Ingestion.QueuePolicy = PolicyDropNewest
	}
	for i := range c.Ingestion.Sources {
		s := &c.Ingestion.Sources[i]
		if s.QueuePolicy == "" {
			s.QueuePolicy = c.Ingestion.QueuePolicy
		}
		if len(s.Parse) == 0 {
			s.Parse = []string{ParseRFC5424, ParseRFC3164}
		}
	}
}

func defaults() map[string]any {
	return map[string]any{
		"api.address":                      ":8080",
		"storage.url":                      "http://127.0.0.1:9428",
		"storage.timeout":                  "10s",
		"storage.account_id":               0,
		"logging.level":                    "info",
		"ingestion.queue_capacity":         50000,
		"ingestion.queue_policy":           PolicyDropNewest,
		"ingestion.workers":                0, // resolved in postProcess
		"ingestion.max_message_bytes":      262144,
		"ingestion.active_connections":     1000,
		"ingestion.store_unknown":          true,
		"ingestion.shutdown_drain_timeout": "5s",
		"ingestion.idle_timeout":           "120s",
		"ingestion.batch.max_entries":      1000,
		"ingestion.batch.max_bytes":        4194304,
		"ingestion.batch.flush_interval":   "500ms",
		"ingestion.sources": []any{
			map[string]any{
				"id": "syslog-udp-5140", "type": TypeSyslogUDP, "enabled": true,
				"address": ":5140", "parse": []any{ParseRFC5424, ParseRFC3164},
			},
			map[string]any{
				"id": "syslog-tcp-5140", "type": TypeSyslogTCP, "enabled": true,
				"address": ":5140",
			},
			map[string]any{
				"id": "http-ingest", "type": TypeHTTPJSON, "enabled": true,
				"parse": []any{ParseJSON},
			},
		},
		"ingestion.http.enabled":      true,
		"ingestion.http.require_auth": false,
		"auth.db_path":                "/var/lib/syslogq/syslogq.db",
		"auth.session_ttl":            "24h",
	}
}
