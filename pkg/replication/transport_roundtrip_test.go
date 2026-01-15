package replication

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestClusterTransport_HeartbeatRoundTrip(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := NewClusterTransport(&ClusterTransportConfig{
		NodeID:   "server-1",
		BindAddr: "127.0.0.1:0",
	})

	var handled atomic.Bool
	server.RegisterHandler(ClusterMsgHeartbeat, func(ctx context.Context, nodeID string, msg *ClusterMessage) (*ClusterMessage, error) {
		handled.Store(true)
		return &ClusterMessage{Type: ClusterMsgHeartbeatResponse, Payload: msg.Payload}, nil
	})

	go func() {
		_ = server.Listen(ctx, server.bindAddr, nil)
	}()

	deadline := time.Now().Add(2 * time.Second)
	var boundAddr string
	for time.Now().Before(deadline) {
		server.mu.RLock()
		ln := server.listener
		boundAddr = server.bindAddr
		server.mu.RUnlock()
		if ln != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	server.mu.RLock()
	ln := server.listener
	boundAddr = server.bindAddr
	server.mu.RUnlock()
	if ln == nil {
		t.Fatalf("server did not start listening")
	}

	client := NewClusterTransport(&ClusterTransportConfig{NodeID: "client-1"})
	conn, err := client.Connect(ctx, boundAddr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	if !waitForConnected(conn, 2*time.Second) {
		t.Fatalf("client connection did not become connected")
	}

	// Ensure RPC path works end-to-end (request routed to handler and response read back).
	_, err = conn.SendHeartbeat(ctx, &HeartbeatRequest{
		NodeID:      "client-1",
		Role:        "test",
		WALPosition: 123,
		Timestamp:   time.Now().UnixNano(),
	})
	if err != nil {
		t.Fatalf("SendHeartbeat: %v", err)
	}
	if !handled.Load() {
		t.Fatalf("expected heartbeat handler to run")
	}
}

func waitForConnected(conn PeerConnection, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if conn.IsConnected() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return conn.IsConnected()
}
