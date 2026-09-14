package victorialogs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/freezxp/syslogq/internal/model"
	"github.com/freezxp/syslogq/internal/storage"
)

type Config struct {
	// BaseURL is the VL HTTP address, e.g. "http://victorialogs:9428".
	BaseURL string
	// Timeout bounds each HTTP request (default 10s).
	Timeout time.Duration
	// AccountID, when > 0, is sent as VL's account_id multi-tenancy param.
	AccountID uint32
	// MaxIdleConnsPerHost sizes the connection pool (default 8).
	MaxIdleConnsPerHost int
}

type Client struct {
	base      *url.URL
	accountID uint32
	http      *http.Client
}

func New(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("victorialogs: BaseURL required")
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("victorialogs: invalid BaseURL %q", cfg.BaseURL)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	idle := cfg.MaxIdleConnsPerHost
	if idle <= 0 {
		idle = 8
	}
	return &Client{
		base:      u,
		accountID: cfg.AccountID,
		http: &http.Client{
			Timeout: timeout,
			// The storage backend is a fixed operator-configured address;
			// it has no business redirecting anywhere.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				MaxIdleConns:        idle * 2,
				MaxIdleConnsPerHost: idle,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}, nil
}

// WriteLogs POSTs the batch as NDJSON to /insert/jsonline, one JSON object
// per line, mapping LogEntry fields per docs/log-data-model.md §6.
func (c *Client) WriteLogs(ctx context.Context, batch []model.LogEntry) error {
	if len(batch) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for i := range batch {
		line, err := marshalEntry(&batch[i])
		if err != nil {
			return fmt.Errorf("%w: marshal entry: %v", storage.ErrBadRequest, err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}

	q := url.Values{}
	if c.accountID > 0 {
		q.Set("account_id", strconv.FormatUint(uint64(c.accountID), 10))
	}
	u := *c.base
	u.Path = u.Path + "/insert/jsonline"
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(buf.Bytes()))
	if err != nil {
		return fmt.Errorf("%w: build request: %v", storage.ErrBackendUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Header.Set("User-Agent", "syslogq")

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%w: %v", storage.ErrTimeout, ctx.Err())
		}
		return fmt.Errorf("%w: %v", storage.ErrBackendUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 == 2 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)) //nolint:errcheck
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode == http.StatusBadRequest {
		return fmt.Errorf("%w: vl %d: %s", storage.ErrBadRequest, resp.StatusCode, body)
	}
	return fmt.Errorf("%w: vl %d: %s", storage.ErrBackendUnavailable, resp.StatusCode, body)
}

// Health pings VL's /health endpoint.
func (c *Client) Health(ctx context.Context) error {
	u := *c.base
	u.Path = u.Path + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("%w: build request: %v", storage.ErrBackendUnavailable, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%w: %v", storage.ErrTimeout, ctx.Err())
		}
		return fmt.Errorf("%w: %v", storage.ErrBackendUnavailable, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1024)) //nolint:errcheck
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf("%w: vl /health status %d", storage.ErrBackendUnavailable, resp.StatusCode)
}

// marshalEntry maps one LogEntry to the VL JSON line. _time and _msg are the
// VL-reserved keys; standard fields keep their snake_case names; dynamic
// fields flatten as-is; labels gain the labels. prefix.
func marshalEntry(e *model.LogEntry) ([]byte, error) {
	m := make(map[string]any, 24)
	m["_time"] = e.Timestamp.UTC().Format(time.RFC3339Nano)
	m["_msg"] = e.Message
	putStr(m, "received_at", e.ReceivedAt.UTC().Format(time.RFC3339Nano))
	putStr(m, "hostname", e.Hostname)
	putStr(m, "source_ip", e.SourceIP)
	if e.SourcePort != 0 {
		m["source_port"] = e.SourcePort
	}
	putInt(m, "facility", e.Facility)
	putStr(m, "facility_name", e.FacilityName)
	putInt(m, "severity", e.Severity)
	putStr(m, "severity_name", e.SeverityName)
	putInt(m, "priority", e.Priority)
	putStr(m, "protocol", e.Protocol)
	putStr(m, "format", e.Format)
	putStr(m, "app_name", e.AppName)
	putStr(m, "process_id", e.ProcessID)
	putStr(m, "message_id", e.MessageID)
	putStr(m, "source_id", e.SourceID)
	putStr(m, "source_type", e.SourceType)
	putStr(m, "tenant_id", e.TenantID)
	putStr(m, "raw_message", e.RawMessage)
	for k, v := range e.Fields {
		m[k] = v
	}
	for k, v := range e.Labels {
		m["labels."+k] = v
	}
	return json.Marshal(m)
}

func putStr(m map[string]any, k, v string) {
	if v != "" {
		m[k] = v
	}
}

func putInt(m map[string]any, k string, v *int) {
	if v != nil {
		m[k] = *v
	}
}
