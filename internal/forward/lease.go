package forward

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type Lease struct {
	Destination Destination
	LocalPort   int
	Connected   time.Time

	host      string
	tunnel    Tunnel
	transport *http.Transport
	dialMu    sync.RWMutex
	closed    bool
}

func newLease(destination Destination, host string, port int, tunnel Tunnel) *Lease {
	lease := &Lease{Destination: destination, LocalPort: port, Connected: time.Now(), host: host, tunnel: tunnel}
	lease.transport = &http.Transport{
		DialContext:         lease.dial,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  true,
	}
	return lease
}

func (lease *Lease) Address() string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(lease.LocalPort))
}

func (lease *Lease) RoundTrip(request *http.Request) (*http.Response, error) {
	return lease.transport.RoundTrip(request)
}

func (lease *Lease) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	lease.dialMu.RLock()
	defer lease.dialMu.RUnlock()
	if lease.closed {
		return nil, net.ErrClosed
	}
	select {
	case <-lease.tunnel.Done:
		return nil, net.ErrClosed
	default:
	}
	dialer := net.Dialer{Timeout: time.Second}
	return dialer.DialContext(ctx, "tcp4", lease.Address())
}

func (lease *Lease) close() {
	// A reused port must never receive a dial from this generation.
	lease.dialMu.Lock()
	lease.closed = true
	lease.dialMu.Unlock()
	lease.transport.CloseIdleConnections()
	lease.tunnel.Close()
}

func probeHTTP(ctx context.Context, lease *Lease) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, "http://"+lease.Address()+"/", nil)
	if err != nil {
		return err
	}
	req.Host = lease.host
	transport := http.Transport{DialContext: lease.dial, DisableKeepAlives: true}
	response, err := transport.RoundTrip(req)
	if err == nil {
		_ = response.Body.Close()
	}
	return err
}
