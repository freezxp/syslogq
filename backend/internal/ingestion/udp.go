package ingestion

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/freezxp/syslogq/internal/config"
	"github.com/freezxp/syslogq/internal/metrics"
)

// udpListener reads one datagram per message (RFC3164/5424-over-UDP
// tradition). The socket is bound in the constructor so bind errors surface
// at startup.
type udpListener struct {
	cfg   config.SourceConfig
	pipe  *Pipeline
	m     *metrics.Ingestion
	log   *slog.Logger
	conn  net.PacketConn
	close sync.Once
}

func newUDPListener(cfg config.SourceConfig, pipe *Pipeline, m *metrics.Ingestion, log *slog.Logger) (*udpListener, error) {
	conn, err := net.ListenPacket("udp", cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("source %s: bind udp: %w", cfg.ID, err)
	}
	return &udpListener{cfg: cfg, pipe: pipe, m: m, log: log, conn: conn}, nil
}

func (u *udpListener) SourceID() string { return u.cfg.ID }

func (u *udpListener) Addr() net.Addr { return u.conn.LocalAddr() }

func (u *udpListener) Run() error {
	u.log.Info("udp listener started", "source_id", u.cfg.ID, "addr", u.conn.LocalAddr().String())

	max := u.pipe.MaxMessageBytes()
	buf := make([]byte, max+1) // +1: detect oversized datagrams, not just clip them
	for {
		n, addr, err := u.conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("source %s: udp read: %w", u.cfg.ID, err)
		}
		if n > max {
			u.pipe.DropOversized(u.cfg.ID)
			continue
		}
		ip, port := splitAddr(addr)
		u.pipe.Ingest(u.cfg.ID, buf[:n], ip, port)
	}
}

func (u *udpListener) Close() error {
	var err error
	u.close.Do(func() {
		if u.conn != nil {
			err = u.conn.Close()
		}
	})
	return err
}

func splitAddr(addr net.Addr) (string, int) {
	switch a := addr.(type) {
	case *net.UDPAddr:
		return a.IP.String(), a.Port
	case *net.TCPAddr:
		return a.IP.String(), a.Port
	default:
		return "unknown", 0
	}
}
