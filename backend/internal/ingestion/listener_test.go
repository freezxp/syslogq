package ingestion

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/freezxp/syslogq/internal/config"
	"github.com/freezxp/syslogq/internal/metrics"
	"github.com/freezxp/syslogq/internal/storage/memory"

	"github.com/prometheus/client_golang/prometheus"
)

// startStack builds one pipeline + one listener for cfg's first source,
// sharing a metrics registry, runs both, and stops them on cleanup in
// production order (listener first, then pipeline drain).
func startStack(t *testing.T, cfg config.IngestionConfig, store *memory.Store) (net.Addr, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	m := metrics.NewIngestion(reg)
	pipe, err := NewPipeline(cfg, store, m, discardLogger())
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	pipe.Start()

	l, err := NewListener(cfg.Sources[0], pipe, m, discardLogger())
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- l.Run() }()
	t.Cleanup(func() {
		l.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("listener Run did not exit after Close")
		}
		pipe.Stop()
	})
	return l.Addr(), reg
}

func udpDial(t *testing.T, addr net.Addr) net.Conn {
	t.Helper()
	conn, err := net.Dial("udp", addr.String())
	if err != nil {
		t.Fatalf("dial udp: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestUDPListenerRoundTrip(t *testing.T) {
	store := &memory.Store{}
	cfg := testConfig()
	addr, reg := startStack(t, cfg, store)

	conn := udpDial(t, addr)
	if _, err := conn.Write([]byte("<34>Oct 11 22:14:15 mymachine su: 'su root' failed")); err != nil {
		t.Fatalf("write: %v", err)
	}

	if !waitStored(t, store, 1) {
		t.Fatalf("datagram never stored; store has %d", store.Len())
	}
	got := store.All()[0]
	if got.Format != "rfc3164" || got.Hostname != "mymachine" {
		t.Fatalf("parsed entry: format=%q hostname=%q", got.Format, got.Hostname)
	}
	if got.Protocol != "udp" {
		t.Fatalf("protocol: %q", got.Protocol)
	}
	if dropped := droppedCount(t, reg, "oversized"); dropped != 0 {
		t.Fatalf("unexpected drops: %d", dropped)
	}
}

func TestUDPListenerOversizedDatagram(t *testing.T) {
	store := &memory.Store{}
	cfg := testConfig()
	cfg.MaxMessageBytes = 128
	addr, reg := startStack(t, cfg, store)

	conn := udpDial(t, addr)
	big := "<34>Oct 11 22:14:15 host su: " + strings.Repeat("x", 200)
	if _, err := conn.Write([]byte(big)); err != nil {
		t.Fatalf("write: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && droppedCount(t, reg, "oversized") == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	if dropped := droppedCount(t, reg, "oversized"); dropped == 0 {
		t.Fatal("oversized datagram not counted")
	}
	time.Sleep(50 * time.Millisecond)
	if store.Len() != 0 {
		t.Fatalf("oversized datagram stored: %d", store.Len())
	}
}

func TestTCPListenerFraming(t *testing.T) {
	store := &memory.Store{}
	cfg := testConfig()
	cfg.Sources[0].Type = config.TypeSyslogTCP
	addr, _ := startStack(t, cfg, store)

	conn, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatalf("dial tcp: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	rfc5424 := "<165>1 2026-09-14T14:30:00Z fw01 vpn - - - TCP frame one"
	rfc3164 := "<34>Oct 11 22:14:15 host su: TCP frame two"
	w := bufio.NewWriter(conn)
	w.WriteString(strconv.Itoa(len(rfc5424)) + " " + rfc5424) // octet-counted
	w.WriteString(rfc3164 + "\n")                             // newline-delimited
	if err := w.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	if !waitStored(t, store, 2) {
		t.Fatalf("frames never stored; store has %d", store.Len())
	}
	byFormat := map[string]bool{}
	for _, e := range store.All() {
		byFormat[e.Format] = true
		if e.Protocol != "tcp" {
			t.Errorf("protocol: %q", e.Protocol)
		}
	}
	if !byFormat["rfc5424"] || !byFormat["rfc3164"] {
		t.Fatalf("expected both formats, got %v", byFormat)
	}
}

func TestTCPListenerConnectionLimit(t *testing.T) {
	store := &memory.Store{}
	cfg := testConfig()
	cfg.Sources[0].Type = config.TypeSyslogTCP
	cfg.ActiveConnections = 1
	addr, reg := startStack(t, cfg, store)

	first, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatalf("dial tcp: %v", err)
	}
	t.Cleanup(func() { first.Close() })
	// A message guarantees the server registered the first connection.
	if _, err := first.Write([]byte("<34>Oct 11 22:14:15 host su: first\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !waitStored(t, store, 1) {
		t.Fatal("first connection never became active")
	}

	second, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatalf("dial tcp: %v", err)
	}
	defer second.Close()
	// The server closes the rejected connection: read must hit EOF, not the
	// read deadline.
	second.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	if _, rerr := second.Read(make([]byte, 16)); !errors.Is(rerr, io.EOF) {
		t.Fatalf("second connection was not rejected: %v", rerr)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && connRejected(t, reg) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	if n := connRejected(t, reg); n == 0 {
		t.Fatal("rejection not counted")
	}
}

func connRejected(t *testing.T, reg *prometheus.Registry) int {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "syslogq_connections_rejected_total" {
			continue
		}
		var n int
		for _, metric := range mf.GetMetric() {
			n += int(metric.GetCounter().GetValue())
		}
		return n
	}
	return 0
}

func TestTCPListenerOversizedOctetFrame(t *testing.T) {
	store := &memory.Store{}
	cfg := testConfig()
	cfg.Sources[0].Type = config.TypeSyslogTCP
	cfg.MaxMessageBytes = 128
	addr, reg := startStack(t, cfg, store)

	conn, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatalf("dial tcp: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	w := bufio.NewWriter(conn)
	w.WriteString("200 " + strings.Repeat("x", 200)) // oversized octet-counted frame
	w.WriteString("<34>Oct 11 22:14:15 host su: after oversize\n")
	if err := w.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	if !waitStored(t, store, 1) {
		t.Fatalf("post-oversize frame never stored; store has %d", store.Len())
	}
	if dropped := droppedCount(t, reg, "oversized"); dropped != 1 {
		t.Fatalf("oversized drops: %d", dropped)
	}
}

func TestTLSListenerRoundTrip(t *testing.T) {
	store := &memory.Store{}
	cfg := testConfig()
	cfg.Sources[0].Type = config.TypeSyslogTLS
	certFile, keyFile := writeSelfSignedCert(t)
	cfg.Sources[0].TLS = config.TLSConfig{CertFile: certFile, KeyFile: keyFile}
	addr, _ := startStack(t, cfg, store)

	roots := x509.NewCertPool()
	pemBytes, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	if !roots.AppendCertsFromPEM(pemBytes) {
		t.Fatal("append cert to pool")
	}
	conn, err := tls.Dial("tcp", addr.String(), &tls.Config{RootCAs: roots})
	if err != nil {
		t.Fatalf("dial tls: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	if _, err := conn.Write([]byte("<34>Oct 11 22:14:15 host su: over tls\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	if !waitStored(t, store, 1) {
		t.Fatal("frame never stored")
	}
	got := store.All()[0]
	if got.Protocol != "tls" {
		t.Fatalf("protocol: %q", got.Protocol)
	}
	if got.Message != "over tls" {
		t.Fatalf("message: %q", got.Message)
	}
}

func TestNewListenerRejectsBadType(t *testing.T) {
	cfg := testConfig()
	cfg.Sources[0].Type = "carrier-pigeon"
	if _, err := NewListener(cfg.Sources[0], nil, nil, discardLogger()); err == nil {
		t.Fatal("expected error for unknown source type")
	}
}

// writeSelfSignedCert generates a throwaway ECDSA cert/key pair for TLS tests.
func writeSelfSignedCert(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certFile, keyFile
}
