package api

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/freezxp/syslogq/internal/storage"
)

const (
	exportPageSize = 1000
	maxExportRows  = 10000
)

// handleExport streams matching rows as a downloadable file: format=ndjson
// (default), json, or csv. Pages of exportPageSize are fetched until the
// row cap, so a slow consumer cannot pin an unbounded buffer.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	p, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	p, err = defaultRange(p)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "ndjson"
	}
	if format != "ndjson" && format != "json" && format != "csv" {
		writeError(w, http.StatusBadRequest, "invalid_query", "format must be ndjson, json, or csv")
		return
	}
	limit := clampInt(r, "limit", maxExportRows, 1, maxExportRows)

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=syslogq-export.%s", format))

	switch format {
	case "csv":
		s.exportCSV(w, r, p, limit)
	case "json":
		s.exportJSON(w, r, p, limit)
	default:
		s.exportNDJSON(w, r, p, limit)
	}
}

func (s *Server) exportNDJSON(w http.ResponseWriter, r *http.Request, p storage.RangeParams, limit int) {
	w.Header().Del("Content-Type")
	w.Header().Set("Content-Type", "application/x-ndjson")
	enc := json.NewEncoder(w)
	s.pageRows(r, p, limit, func(rows []map[string]string) bool {
		for _, row := range rows {
			if err := enc.Encode(row); err != nil {
				return false
			}
		}
		return true
	})
}

func (s *Server) exportJSON(w http.ResponseWriter, r *http.Request, p storage.RangeParams, limit int) {
	w.Header().Set("Content-Type", "application/json")
	first := true
	s.pageRows(r, p, limit, func(rows []map[string]string) bool {
		for _, row := range rows {
			if first {
				w.Write([]byte("[\n"))
				first = false
			} else {
				w.Write([]byte(",\n"))
			}
			b, err := json.Marshal(row)
			if err != nil {
				return false
			}
			if _, werr := w.Write(b); werr != nil {
				return false
			}
		}
		return true
	})
	if first {
		w.Write([]byte("[]"))
	} else {
		w.Write([]byte("\n]\n"))
	}
}

func (s *Server) exportCSV(w http.ResponseWriter, r *http.Request, p storage.RangeParams, limit int) {
	w.Header().Set("Content-Type", "text/csv")
	cw := csv.NewWriter(w)
	var header []string
	s.pageRows(r, p, limit, func(rows []map[string]string) bool {
		if header == nil && len(rows) > 0 {
			header = make([]string, 0, len(rows[0]))
			for k := range rows[0] {
				header = append(header, k)
			}
			sortStrings(header)
			if err := cw.Write(header); err != nil {
				return false
			}
		}
		for _, row := range rows {
			rec := make([]string, len(header))
			for i, k := range header {
				rec[i] = row[k]
			}
			if err := cw.Write(rec); err != nil {
				return false
			}
		}
		return true
	})
	cw.Flush()
}

// pageRows walks the result set in pages, calling emit until it returns
// false, the limit is reached, or storage errors.
func (s *Server) pageRows(r *http.Request, p storage.RangeParams, limit int, emit func([]map[string]string) bool) {
	remaining := limit
	for remaining > 0 {
		page := exportPageSize
		if remaining < page {
			page = remaining
		}
		rows, err := s.deps.Reader.Search(r.Context(), storage.SearchParams{
			RangeParams: p,
			Offset:      limit - remaining,
			Limit:       page,
		})
		if err != nil {
			if errors.Is(err, storage.ErrBadRequest) {
				s.deps.Log.Warn("export: query rejected", "error", err)
			} else {
				s.deps.Log.Error("export: storage error", "error", err)
			}
			return
		}
		if len(rows) == 0 {
			return
		}
		if !emit(rows) {
			return
		}
		remaining -= len(rows)
		if len(rows) < page {
			return
		}
	}
}

func sortStrings(s []string) {
	sort.Strings(s)
}
