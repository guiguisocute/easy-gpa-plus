package backupjob

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestRemoteTransportBlocksPrivateDestinations(t *testing.T) {
	transport := remoteTransport(false)
	defer transport.CloseIdleConnections()
	if transport.Proxy != nil {
		t.Fatal("ambient proxy must be disabled")
	}
	for _, address := range []string{"127.0.0.1:80", "192.0.2.1:33900", "169.254.169.254:80", "198.18.0.1:80", "[::1]:80"} {
		conn, err := transport.DialContext(context.Background(), "tcp", address)
		if conn != nil {
			conn.Close()
			t.Fatalf("connected to blocked address %s", address)
		}
		if err == nil || !strings.Contains(err.Error(), "blocked") {
			t.Fatalf("%s: %v", address, err)
		}
	}
}

func TestRemoteTransportDevelopmentOnlyAllowsLoopback(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	transport := remoteTransport(true)
	defer transport.CloseIdleConnections()
	conn, err := transport.DialContext(context.Background(), "tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if _, err := transport.DialContext(context.Background(), "tcp", "192.0.2.1:33900"); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("private LAN allowed: %v", err)
	}
}
