package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeYAML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "syslogq.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.API.Address != ":8080" {
		t.Errorf("api.address: %q", cfg.API.Address)
	}
	if cfg.Storage.URL != "http://127.0.0.1:9428" || cfg.Storage.Timeout != 10*time.Second {
		t.Errorf("storage: %+v", cfg.Storage)
	}
	if cfg.Ingestion.QueueCapacity != 50000 || cfg.Ingestion.QueuePolicy != PolicyDropNewest {
		t.Errorf("ingestion: %+v", cfg.Ingestion)
	}
	if cfg.Ingestion.Workers < 1 || cfg.Ingestion.Workers > 8 {
		t.Errorf("workers default out of range: %d", cfg.Ingestion.Workers)
	}
	if cfg.Ingestion.Batch.MaxEntries != 1000 || cfg.Ingestion.Batch.MaxBytes != 4194304 ||
		cfg.Ingestion.Batch.FlushInterval != 500*time.Millisecond {
		t.Errorf("batch: %+v", cfg.Ingestion.Batch)
	}
	if len(cfg.Ingestion.Sources) != 3 {
		t.Fatalf("default sources: %d", len(cfg.Ingestion.Sources))
	}
	udp := cfg.Ingestion.Sources[0]
	if udp.ID != "syslog-udp-5140" || udp.Type != TypeSyslogUDP || udp.Address != ":5140" {
		t.Errorf("udp source: %+v", udp)
	}
	if len(udp.Parse) != 2 || udp.Parse[0] != ParseRFC5424 {
		t.Errorf("udp parse default: %v", udp.Parse)
	}
	httpSrc := cfg.Ingestion.Sources[2]
	if httpSrc.ID != "http-ingest" || httpSrc.Type != TypeHTTPJSON || httpSrc.Address != "" {
		t.Errorf("http source: %+v", httpSrc)
	}
	if !cfg.Ingestion.HTTP.Enabled || cfg.Ingestion.HTTP.RequireAuth {
		t.Errorf("http ingest defaults: %+v", cfg.Ingestion.HTTP)
	}
	if cfg.Auth.DBPath == "" || cfg.Auth.SessionTTL != 24*time.Hour {
		t.Errorf("auth defaults: %+v", cfg.Auth)
	}
	if len(cfg.EnabledSources()) != 3 {
		t.Errorf("all default sources should be enabled")
	}
}

func TestLoadYAMLOverridesDefaults(t *testing.T) {
	path := writeYAML(t, `
api:
  address: ":9090"
storage:
  url: http://vl:9428
  timeout: 3s
ingestion:
  queue_capacity: 1000
  workers: 2
  batch:
    flush_interval: 100ms
  sources:
    - id: udp-custom
      type: syslog_udp
      enabled: true
      address: ":1514"
      queue_policy: block
      parse: [rfc3164]
    - id: tls-src
      type: syslog_tls
      enabled: false
      address: ":6514"
      tls:
        cert_file: /certs/tls.crt
        key_file: /certs/tls.key
logging:
  level: debug
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.API.Address != ":9090" {
		t.Errorf("address: %q", cfg.API.Address)
	}
	if cfg.Storage.Timeout != 3*time.Second {
		t.Errorf("storage timeout: %v", cfg.Storage.Timeout)
	}
	if cfg.Ingestion.QueueCapacity != 1000 || cfg.Ingestion.Workers != 2 {
		t.Errorf("ingestion: %+v", cfg.Ingestion)
	}
	if cfg.Ingestion.Batch.FlushInterval != 100*time.Millisecond {
		t.Errorf("flush interval: %v", cfg.Ingestion.Batch.FlushInterval)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("level: %q", cfg.Logging.Level)
	}
	if len(cfg.Ingestion.Sources) != 2 {
		t.Fatalf("sources should be replaced, got %d", len(cfg.Ingestion.Sources))
	}
	s := cfg.Ingestion.Sources[0]
	if s.QueuePolicy != "block" || len(s.Parse) != 1 || s.Parse[0] != "rfc3164" {
		t.Errorf("source overrides: %+v", s)
	}
	// Disabled TLS source must not require cert files at validation time.
	if _, ok := cfg.Source("tls-src"); !ok {
		t.Error("Source lookup failed")
	}
	if len(cfg.EnabledSources()) != 1 {
		t.Errorf("enabled sources: %d", len(cfg.EnabledSources()))
	}
}

func TestEnvOverridesYAML(t *testing.T) {
	path := writeYAML(t, `
api:
  address: ":9090"
ingestion:
  queue_capacity: 1000
  sources:
    - id: s1
      type: syslog_udp
      enabled: true
      address: ":1514"
`)
	t.Setenv("SYSLOGQ_API__ADDRESS", ":7070")
	t.Setenv("SYSLOGQ_INGESTION__QUEUE_CAPACITY", "42")
	t.Setenv("SYSLOGQ_STORAGE__TIMEOUT", "2s")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.API.Address != ":7070" {
		t.Errorf("env should win: %q", cfg.API.Address)
	}
	if cfg.Ingestion.QueueCapacity != 42 {
		t.Errorf("env int: %d", cfg.Ingestion.QueueCapacity)
	}
	if cfg.Storage.Timeout != 2*time.Second {
		t.Errorf("env duration: %v", cfg.Storage.Timeout)
	}
}

func TestValidationErrors(t *testing.T) {
	path := writeYAML(t, `
storage:
  url: ftp://bad
logging:
  level: loud
ingestion:
  queue_capacity: 0
  sources:
    - id: dup
      type: bogus_type
      address: ":1"
      parse: [nope]
    - id: dup
      type: syslog_udp
      address: ""
    - id: tls-on
      type: syslog_tls
      enabled: true
      address: ":6514"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
	msg := err.Error()
	for _, want := range []string{
		"storage.url:", "logging.level:", "queue_capacity:", "duplicate", "bogus_type",
		"nope", "address: required", "cert_file and key_file",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q:\n%s", want, msg)
		}
	}
}

func TestLoadPathResolution(t *testing.T) {
	if got := LoadPath(""); got != "" {
		t.Errorf("empty flag, no env: %q", got)
	}
	t.Setenv("SYSLOGQ_CONFIG", "/etc/syslogq.yaml")
	if got := LoadPath(""); got != "/etc/syslogq.yaml" {
		t.Errorf("env path: %q", got)
	}
	if got := LoadPath("/flag/path.yaml"); got != "/flag/path.yaml" {
		t.Errorf("flag should win: %q", got)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load("/nonexistent/syslogq.yaml"); err == nil {
		t.Fatal("expected error for missing file")
	}
}
