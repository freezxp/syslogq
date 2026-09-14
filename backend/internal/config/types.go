package config

import (
	"fmt"
	"runtime"
	"strings"
	"time"
)

// Source types (docs/ingestion.md §5).
const (
	TypeSyslogUDP = "syslog_udp"
	TypeSyslogTCP = "syslog_tcp"
	TypeSyslogTLS = "syslog_tls"
)

// Queue policies (docs/ingestion.md §1).
const (
	PolicyDropNewest = "drop_newest"
	PolicyDropOldest = "drop_oldest"
	PolicyBlock      = "block"
)

// Parse formats accepted in a source's parse list.
const (
	ParseRFC5424 = "rfc5424"
	ParseRFC3164 = "rfc3164"
)

type Config struct {
	API       APIConfig       `koanf:"api"`
	Storage   StorageConfig   `koanf:"storage"`
	Ingestion IngestionConfig `koanf:"ingestion"`
	Logging   LoggingConfig   `koanf:"logging"`
}

type APIConfig struct {
	// Address the HTTP server binds (":8080").
	Address string `koanf:"address"`
}

type StorageConfig struct {
	// URL of the VictoriaLogs HTTP endpoint.
	URL string `koanf:"url"`
	// Timeout per storage HTTP request.
	Timeout time.Duration `koanf:"timeout"`
	// AccountID is VL's numeric multi-tenancy account (0 = unset).
	AccountID uint32 `koanf:"account_id"`
}

type LoggingConfig struct {
	// Level: debug | info | warn | error.
	Level string `koanf:"level"`
}

type IngestionConfig struct {
	QueueCapacity        int           `koanf:"queue_capacity"`
	QueuePolicy          string        `koanf:"queue_policy"`
	Workers              int           `koanf:"workers"`
	MaxMessageBytes      int           `koanf:"max_message_bytes"`
	ActiveConnections    int           `koanf:"active_connections"`
	StoreUnknown         bool          `koanf:"store_unknown"`
	ShutdownDrainTimeout time.Duration `koanf:"shutdown_drain_timeout"`
	// IdleTimeout closes silent TCP/TLS connections (0 disables).
	IdleTimeout time.Duration  `koanf:"idle_timeout"`
	Batch       BatchConfig    `koanf:"batch"`
	Sources     []SourceConfig `koanf:"sources"`
}

type BatchConfig struct {
	MaxEntries    int           `koanf:"max_entries"`
	MaxBytes      int           `koanf:"max_bytes"`
	FlushInterval time.Duration `koanf:"flush_interval"`
}

type SourceConfig struct {
	ID          string    `koanf:"id"`
	Type        string    `koanf:"type"`
	Enabled     bool      `koanf:"enabled"`
	Address     string    `koanf:"address"`
	Parse       []string  `koanf:"parse"`
	QueuePolicy string    `koanf:"queue_policy"`
	TLS         TLSConfig `koanf:"tls"`
}

type TLSConfig struct {
	CertFile     string `koanf:"cert_file"`
	KeyFile      string `koanf:"key_file"`
	ClientCAFile string `koanf:"client_ca_file"`
}

func defaultWorkers() int {
	w := runtime.NumCPU() / 2
	if w < 1 {
		w = 1
	}
	if w > 8 {
		w = 8
	}
	return w
}

// validate returns configuration problems as one error with field paths.
func (c *Config) validate() error {
	var problems []string

	if c.API.Address == "" {
		problems = append(problems, "api.address: required")
	}
	if c.Storage.URL == "" {
		problems = append(problems, "storage.url: required")
	} else if !strings.HasPrefix(c.Storage.URL, "http://") && !strings.HasPrefix(c.Storage.URL, "https://") {
		problems = append(problems, fmt.Sprintf("storage.url: must start with http:// or https:// (got %q)", c.Storage.URL))
	}
	if c.Storage.Timeout <= 0 {
		problems = append(problems, "storage.timeout: must be > 0")
	}

	switch c.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		problems = append(problems, fmt.Sprintf("logging.level: must be debug|info|warn|error (got %q)", c.Logging.Level))
	}

	ing := &c.Ingestion
	if ing.QueueCapacity < 1 {
		problems = append(problems, "ingestion.queue_capacity: must be >= 1")
	}
	if ing.Workers < 1 {
		problems = append(problems, "ingestion.workers: must be >= 1")
	}
	if ing.MaxMessageBytes < 1024 {
		problems = append(problems, "ingestion.max_message_bytes: must be >= 1024")
	}
	if ing.ActiveConnections < 1 {
		problems = append(problems, "ingestion.active_connections: must be >= 1")
	}
	if ing.ShutdownDrainTimeout <= 0 {
		problems = append(problems, "ingestion.shutdown_drain_timeout: must be > 0")
	}
	if ing.IdleTimeout < 0 {
		problems = append(problems, "ingestion.idle_timeout: must be >= 0")
	}
	if ing.Batch.MaxEntries < 1 {
		problems = append(problems, "ingestion.batch.max_entries: must be >= 1")
	}
	if ing.Batch.MaxBytes < 1024 {
		problems = append(problems, "ingestion.batch.max_bytes: must be >= 1024")
	}
	if ing.Batch.FlushInterval <= 0 {
		problems = append(problems, "ingestion.batch.flush_interval: must be > 0")
	}
	if ing.QueuePolicy != "" {
		if err := validatePolicy(ing.QueuePolicy, "ingestion.queue_policy"); err != nil {
			problems = append(problems, err.Error())
		}
	}

	if len(ing.Sources) == 0 {
		problems = append(problems, "ingestion.sources: at least one source required")
	}
	seen := map[string]bool{}
	for i, s := range ing.Sources {
		prefix := fmt.Sprintf("ingestion.sources[%d]", i)
		if s.ID == "" {
			problems = append(problems, prefix+".id: required")
		} else if seen[s.ID] {
			problems = append(problems, prefix+fmt.Sprintf(".id: duplicate %q", s.ID))
		}
		seen[s.ID] = true
		switch s.Type {
		case TypeSyslogUDP, TypeSyslogTCP, TypeSyslogTLS:
		default:
			problems = append(problems, prefix+fmt.Sprintf(".type: must be %s|%s|%s (got %q)",
				TypeSyslogUDP, TypeSyslogTCP, TypeSyslogTLS, s.Type))
		}
		if s.Address == "" {
			problems = append(problems, prefix+".address: required")
		}
		if s.QueuePolicy != "" {
			if err := validatePolicy(s.QueuePolicy, prefix+".queue_policy"); err != nil {
				problems = append(problems, err.Error())
			}
		}
		if len(s.Parse) == 0 {
			problems = append(problems, prefix+".parse: at least one format required")
		}
		for _, f := range s.Parse {
			if f != ParseRFC5424 && f != ParseRFC3164 {
				problems = append(problems, prefix+fmt.Sprintf(".parse: unknown format %q", f))
			}
		}
		if s.Type == TypeSyslogTLS && s.Enabled {
			if s.TLS.CertFile == "" || s.TLS.KeyFile == "" {
				problems = append(problems, prefix+".tls: cert_file and key_file required for enabled syslog_tls source")
			}
		}
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(problems, "\n  - "))
}

func validatePolicy(policy, field string) error {
	switch policy {
	case PolicyDropNewest, PolicyDropOldest, PolicyBlock:
		return nil
	default:
		return fmt.Errorf("%s: must be %s|%s|%s (got %q)",
			field, PolicyDropNewest, PolicyDropOldest, PolicyBlock, policy)
	}
}

// Source returns the source config with the given id.
func (c *Config) Source(id string) (SourceConfig, bool) {
	for _, s := range c.Ingestion.Sources {
		if s.ID == id {
			return s, true
		}
	}
	return SourceConfig{}, false
}

// EnabledSources returns the sources to start.
func (c *Config) EnabledSources() []SourceConfig {
	var out []SourceConfig
	for _, s := range c.Ingestion.Sources {
		if s.Enabled {
			out = append(out, s)
		}
	}
	return out
}
