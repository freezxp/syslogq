// Command loggen is a minimal syslog load generator for soak tests: it
// sends RFC5424 messages at a fixed rate over UDP or TCP (newline or
// octet-counted framing) and prints a summary.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:5140", "target address")
	proto := flag.String("proto", "udp", "udp | tcp")
	rate := flag.Int("rate", 1000, "messages per second")
	duration := flag.Duration("duration", 10*time.Second, "how long to send")
	framing := flag.String("framing", "newline", "tcp framing: newline | octet")
	host := flag.String("host", "loggen", "hostname in generated messages")
	flag.Parse()

	if *rate <= 0 {
		log.Fatal("rate must be > 0")
	}
	switch *proto {
	case "udp", "tcp":
	default:
		log.Fatalf("proto must be udp or tcp, got %q", *proto)
	}

	conn, err := net.Dial(*proto, *addr)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	interval := time.Second / time.Duration(*rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	start := time.Now()
	deadline := start.Add(*duration)
	var sent int
loop:
	for {
		now := time.Now()
		if now.After(deadline) {
			break
		}
		select {
		case <-stop:
			break loop
		case <-ticker.C:
		}
		msg := fmt.Sprintf("<134>1 %s %s loggen %d - - load test message %d",
			now.UTC().Format(time.RFC3339), *host, os.Getpid(), sent)
		var payload []byte
		switch {
		case *proto == "udp":
			payload = []byte(msg)
		case *framing == "octet":
			payload = []byte(strconv.Itoa(len(msg)) + " " + msg)
		default:
			payload = append([]byte(msg), '\n')
		}
		if _, werr := conn.Write(payload); werr != nil {
			// UDP writes can only fail on a dead local socket; treat any
			// write error as fatal for the run.
			log.Fatalf("write after %d messages: %v", sent, werr)
		}
		sent++
	}

	elapsed := time.Since(start)
	effective := float64(sent) / elapsed.Seconds()
	fmt.Printf("sent=%d elapsed=%s effective_rate=%.0f/s\n", sent, elapsed.Round(time.Millisecond), effective)
}
