package victorialogs

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/freezxp/syslogq/internal/storage"
)

// selectQuery runs a LogsQL query against /select/logsql/query and streams
// back JSONL rows decoded as maps. Pipes are appended here from integers
// and validated field names only — p.Filter must already be a
// query.Compile product, never raw user text (ADR-0001).
func (c *Client) selectQuery(ctx context.Context, p storage.RangeParams, pipes string) ([]map[string]string, error) {
	q := url.Values{}
	if c.accountID > 0 {
		q.Set("account_id", strconv.FormatUint(uint64(c.accountID), 10))
	}
	if !p.Start.IsZero() {
		q.Set("start", p.Start.UTC().Format(time.RFC3339Nano))
	}
	if !p.End.IsZero() {
		q.Set("end", p.End.UTC().Format(time.RFC3339Nano))
	}
	q.Set("query", baseFilter(p.Filter)+pipes)

	u := *c.base
	u.Path = u.Path + "/select/logsql/query"
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", storage.ErrBackendUnavailable, err)
	}
	req.Header.Set("User-Agent", "syslogq")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %v", storage.ErrTimeout, ctx.Err())
		}
		return nil, fmt.Errorf("%w: %v", storage.ErrBackendUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		if resp.StatusCode == http.StatusBadRequest {
			return nil, fmt.Errorf("%w: vl: %s", storage.ErrBadRequest, body)
		}
		return nil, fmt.Errorf("%w: vl %d: %s", storage.ErrBackendUnavailable, resp.StatusCode, body)
	}

	var rows []map[string]string
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m map[string]string
		if err := json.Unmarshal(line, &m); err != nil {
			return nil, fmt.Errorf("%w: decode row: %v", storage.ErrBackendUnavailable, err)
		}
		rows = append(rows, m)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: read rows: %v", storage.ErrBackendUnavailable, err)
	}
	return rows, nil
}

func baseFilter(filter string) string {
	if filter == "" {
		return "*"
	}
	return filter
}

// Search returns matching rows newest-first, one page at a time.
func (c *Client) Search(ctx context.Context, p storage.SearchParams) ([]map[string]string, error) {
	pipes := " | sort by (_time desc) | offset " + strconv.Itoa(p.Offset) + " | limit " + strconv.Itoa(p.Limit)
	return c.selectQuery(ctx, p.RangeParams, pipes)
}

// Count returns the total number of matching rows.
func (c *Client) Count(ctx context.Context, p storage.RangeParams) (int64, error) {
	rows, err := c.selectQuery(ctx, p, " | stats count()")
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	n, _ := strconv.ParseInt(rows[0]["count(*)"], 10, 64)
	return n, nil
}

// CountByTime buckets matches by step. Buckets with no data are omitted.
func (c *Client) CountByTime(ctx context.Context, p storage.RangeParams, step time.Duration) ([]storage.BucketCount, error) {
	if step <= 0 {
		return nil, fmt.Errorf("%w: bucket step must be positive", storage.ErrBadRequest)
	}
	pipes := " | stats by (_time:" + step.String() + ") count()"
	rows, err := c.selectQuery(ctx, p, pipes)
	if err != nil {
		return nil, err
	}
	out := make([]storage.BucketCount, 0, len(rows))
	for _, r := range rows {
		ts, ok := r["_time"]
		if !ok {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			continue
		}
		n, _ := strconv.ParseInt(r["count(*)"], 10, 64)
		out = append(out, storage.BucketCount{Time: t, Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out, nil
}

// FieldValues returns the top values of field with counts, highest first.
// Rows missing the field surface as the empty value.
func (c *Client) FieldValues(ctx context.Context, p storage.RangeParams, field string, limit int) ([]storage.ValueCount, error) {
	if !validQueryField(field) {
		return nil, fmt.Errorf("%w: invalid field %q", storage.ErrBadRequest, field)
	}
	if limit <= 0 {
		limit = 10
	}
	pipes := " | top " + strconv.Itoa(limit) + " by (" + field + ")"
	rows, err := c.selectQuery(ctx, p, pipes)
	if err != nil {
		return nil, err
	}
	out := make([]storage.ValueCount, 0, len(rows))
	for _, r := range rows {
		n, _ := strconv.ParseInt(r["hits"], 10, 64)
		out = append(out, storage.ValueCount{Value: r[field], Count: n})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out, nil
}

// fieldNamesResponse is the /select/logsql/field_names payload.
type fieldNamesResponse struct {
	Values []struct {
		Value string `json:"value"`
		Hits  int64  `json:"hits"`
	} `json:"values"`
}

// FieldNames returns fields present in the range with row counts.
func (c *Client) FieldNames(ctx context.Context, p storage.RangeParams) ([]storage.ValueCount, error) {
	q := url.Values{}
	if c.accountID > 0 {
		q.Set("account_id", strconv.FormatUint(uint64(c.accountID), 10))
	}
	if !p.Start.IsZero() {
		q.Set("start", p.Start.UTC().Format(time.RFC3339Nano))
	}
	if !p.End.IsZero() {
		q.Set("end", p.End.UTC().Format(time.RFC3339Nano))
	}
	q.Set("query", baseFilter(p.Filter))

	u := *c.base
	u.Path = u.Path + "/select/logsql/field_names"
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", storage.ErrBackendUnavailable, err)
	}
	req.Header.Set("User-Agent", "syslogq")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %v", storage.ErrTimeout, ctx.Err())
		}
		return nil, fmt.Errorf("%w: %v", storage.ErrBackendUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("%w: vl %d: %s", storage.ErrBackendUnavailable, resp.StatusCode, body)
	}

	var fr fieldNamesResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&fr); err != nil {
		return nil, fmt.Errorf("%w: decode field names: %v", storage.ErrBackendUnavailable, err)
	}
	out := make([]storage.ValueCount, 0, len(fr.Values))
	for _, v := range fr.Values {
		out = append(out, storage.ValueCount{Value: v.Value, Count: v.Hits})
	}
	return out, nil
}

// validQueryField guards the one position where a caller-supplied field
// name enters a query: the top-pipe. It matches query.validField's charset.
func validQueryField(f string) bool {
	if f == "" {
		return false
	}
	for i := 0; i < len(f); i++ {
		c := f[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '_' || c == '.'
		if !ok {
			return false
		}
	}
	return true
}
