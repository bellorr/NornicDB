// Package replication provides cluster replication for NornicDB.
//
// Transport Architecture:
//
// NornicDB cluster communication uses a hybrid approach:
//
// 1. **Client Bolt Protocol (default port 7687)** - Used for:
//   - Neo4j driver compatibility for client queries
//
//   Note: automatic "write forwarding" over Bolt (follower/standby -> leader/primary)
//   is not implemented yet. Today, clients should connect to the leader/primary for
//   writes (followers/standby will reject writes with ErrNotLeader).
//
// 2. **Cluster Protocol (default port 7688)** - Used for:
//   - Raft consensus (RequestVote, AppendEntries)
//   - WAL streaming for HA standby
//   - Heartbeats and health checks
//   - Cluster coordination
//
// This separation allows:
//   - Client-facing Bolt remains pure Neo4j compatible
//   - Cluster protocol is optimized for low-latency consensus
//   - Existing Bolt infrastructure reused where appropriate
//
// Example Configuration:
//
//	NORNICDB_CLUSTER_MODE=raft
//	NORNICDB_CLUSTER_BIND_ADDR=0.0.0.0:7688
//	NORNICDB_BOLT_PORT=7687
package replication

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// ClusterMessageType identifies cluster protocol messages.
type ClusterMessageType uint8

const (
	// Raft consensus messages
	ClusterMsgVoteRequest ClusterMessageType = iota + 1
	ClusterMsgVoteResponse
	ClusterMsgAppendEntries
	ClusterMsgAppendEntriesResponse

	// HA standby messages
	ClusterMsgWALBatch
	ClusterMsgWALBatchResponse
	ClusterMsgHeartbeat
	ClusterMsgHeartbeatResponse
	ClusterMsgFence
	ClusterMsgFenceResponse
	ClusterMsgPromote
	ClusterMsgPromoteResponse

	// Cluster management
	ClusterMsgJoin
	ClusterMsgJoinResponse
	ClusterMsgLeave
	ClusterMsgLeaveResponse
	ClusterMsgStatus
	ClusterMsgStatusResponse
)

// ClusterMessage is the on-wire format for cluster communication.
type ClusterMessage struct {
	Type    ClusterMessageType
	NodeID  string
	Payload []byte
}

// ClusterTransport handles cluster-to-cluster communication.
//
// For client-facing queries, use the standard Bolt server (pkg/bolt).
// ClusterTransport is specifically for:
//   - Raft consensus protocol
//   - WAL streaming for HA
//   - Cluster coordination
type ClusterTransport struct {
	mu           sync.RWMutex
	nodeID       string
	bindAddr     string
	listener     net.Listener
	connections  map[string]*ClusterConnection
	closed       atomic.Bool
	closeCh      chan struct{}
	wg           sync.WaitGroup
	dialTimeout  time.Duration
	readTimeout  time.Duration
	writeTimeout time.Duration
	maxMsgSize   int

	// Message handlers
	handlers map[ClusterMessageType]MessageHandler
}

// MessageHandler processes incoming cluster messages.
type MessageHandler func(ctx context.Context, nodeID string, msg *ClusterMessage) (*ClusterMessage, error)

// ClusterTransportConfig configures the cluster transport.
type ClusterTransportConfig struct {
	NodeID       string
	BindAddr     string
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	MaxMsgSize   int
}

// DefaultClusterTransportConfig returns production defaults.
func DefaultClusterTransportConfig() *ClusterTransportConfig {
	return &ClusterTransportConfig{
		DialTimeout:  5 * time.Second,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 10 * time.Second,
		MaxMsgSize:   64 * 1024 * 1024, // 64MB max
	}
}

// NewClusterTransport creates a cluster transport.
func NewClusterTransport(config *ClusterTransportConfig) *ClusterTransport {
	defaults := DefaultClusterTransportConfig()
	if config == nil {
		config = defaults
	} else {
		// Fill unset values with sensible defaults to avoid accidental "0 means no timeouts"
		// behavior, which can lead to hangs/timeouts that are hard to debug.
		if config.DialTimeout == 0 {
			config.DialTimeout = defaults.DialTimeout
		}
		if config.ReadTimeout == 0 {
			config.ReadTimeout = defaults.ReadTimeout
		}
		if config.WriteTimeout == 0 {
			config.WriteTimeout = defaults.WriteTimeout
		}
		if config.MaxMsgSize == 0 {
			config.MaxMsgSize = defaults.MaxMsgSize
		}
	}
	return &ClusterTransport{
		nodeID:       config.NodeID,
		bindAddr:     config.BindAddr,
		connections:  make(map[string]*ClusterConnection),
		closeCh:      make(chan struct{}),
		dialTimeout:  config.DialTimeout,
		readTimeout:  config.ReadTimeout,
		writeTimeout: config.WriteTimeout,
		maxMsgSize:   config.MaxMsgSize,
		handlers:     make(map[ClusterMessageType]MessageHandler),
	}
}

// RegisterHandler registers a handler for a message type.
func (t *ClusterTransport) RegisterHandler(msgType ClusterMessageType, handler MessageHandler) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.handlers[msgType] = handler
}

// Connect establishes a connection to a peer node.
func (t *ClusterTransport) Connect(ctx context.Context, addr string) (PeerConnection, error) {
	if t.closed.Load() {
		return nil, errors.New("transport closed")
	}

	// Check for existing connection
	t.mu.RLock()
	if conn, ok := t.connections[addr]; ok && conn.IsConnected() {
		t.mu.RUnlock()
		return conn, nil
	}
	t.mu.RUnlock()

	// Dial with timeout (also set Dialer.Timeout so the net stack doesn't hang
	// if a caller passes a context without a deadline).
	dialTimeout := t.dialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 5 * time.Second
	}
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	var d net.Dialer
	d.Timeout = dialTimeout
	netConn, err := d.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", addr, err)
	}

	conn := t.createConnection(addr, netConn)
	conn.wg.Add(1)
	go conn.readLoop()

	// Store connection
	t.mu.Lock()
	t.connections[addr] = conn
	t.mu.Unlock()

	log.Printf("[Cluster] Connected to peer %s", addr)
	return conn, nil
}

func (t *ClusterTransport) createConnection(addr string, netConn net.Conn) *ClusterConnection {
	return &ClusterConnection{
		transport:    t,
		addr:         addr,
		conn:         netConn,
		reader:       bufio.NewReader(netConn),
		writer:       bufio.NewWriter(netConn),
		readTimeout:  t.readTimeout,
		writeTimeout: t.writeTimeout,
		maxMsgSize:   t.maxMsgSize,
		closeCh:      make(chan struct{}),
		pendingRPCs:  make(map[uint64]chan *ClusterMessage),
	}
}

// Listen starts accepting cluster connections.
func (t *ClusterTransport) Listen(ctx context.Context, addr string, handler ConnectionHandler) error {
	if t.closed.Load() {
		return errors.New("transport closed")
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	t.mu.Lock()
	t.listener = listener
	// If an ephemeral port was requested (or the caller passes an empty addr in tests),
	// record the actual bound address for later dials/diagnostics.
	t.bindAddr = listener.Addr().String()
	t.mu.Unlock()

	log.Printf("[Cluster] Listening on %s", listener.Addr().String())

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.closeCh:
			return nil
		default:
		}

		// Set accept deadline for graceful shutdown
		if tcpListener, ok := listener.(*net.TCPListener); ok {
			tcpListener.SetDeadline(time.Now().Add(time.Second))
		}

		netConn, err := listener.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			if t.closed.Load() {
				return nil
			}
			log.Printf("[Cluster] Accept error: %v", err)
			continue
		}

		t.wg.Add(1)
		go t.handleIncoming(ctx, netConn, handler)
	}
}

func (t *ClusterTransport) handleIncoming(ctx context.Context, netConn net.Conn, onConnect ConnectionHandler) {
	defer t.wg.Done()

	remoteAddr := netConn.RemoteAddr().String()
	log.Printf("[Cluster] Accepted connection from %s", remoteAddr)

	// Treat inbound connections the same as outbound ones so they can both:
	// 1) serve incoming requests via registered handlers
	// 2) be used to send requests back to the peer (e.g., fencing during failover)
	conn := t.createConnection(remoteAddr, netConn)

	t.mu.Lock()
	t.connections[remoteAddr] = conn
	t.mu.Unlock()

	if onConnect != nil {
		onConnect(conn)
	}

	conn.wg.Add(1)
	go conn.readLoopWithContext(ctx)
	conn.wg.Wait()
}

// Close shuts down the transport.
func (t *ClusterTransport) Close() error {
	if t.closed.Swap(true) {
		return nil
	}

	close(t.closeCh)

	t.mu.Lock()
	if t.listener != nil {
		t.listener.Close()
	}
	for _, conn := range t.connections {
		conn.Close()
	}
	t.mu.Unlock()

	t.wg.Wait()
	log.Printf("[Cluster] Transport closed")
	return nil
}

// ClusterConnection implements PeerConnection for cluster communication.
type ClusterConnection struct {
	transport    *ClusterTransport
	addr         string
	conn         net.Conn
	reader       *bufio.Reader
	writer       *bufio.Writer
	mu           sync.Mutex
	connected    atomic.Bool
	closeCh      chan struct{}
	wg           sync.WaitGroup
	readTimeout  time.Duration
	writeTimeout time.Duration
	maxMsgSize   int

	// RPC tracking
	rpcMu       sync.Mutex
	nextRPCID   uint64
	pendingRPCs map[uint64]chan *ClusterMessage
}

func (c *ClusterConnection) sendRPC(ctx context.Context, msg *ClusterMessage) (*ClusterMessage, error) {
	if !c.connected.Load() {
		return nil, errors.New("not connected")
	}
	if msg.NodeID == "" && c.transport != nil && c.transport.nodeID != "" {
		msg.NodeID = c.transport.nodeID
	}

	// Create response channel
	c.rpcMu.Lock()
	rpcID := c.nextRPCID
	c.nextRPCID++
	respCh := make(chan *ClusterMessage, 1)
	c.pendingRPCs[rpcID] = respCh
	c.rpcMu.Unlock()

	defer func() {
		c.rpcMu.Lock()
		delete(c.pendingRPCs, rpcID)
		c.rpcMu.Unlock()
	}()

	// Send request
	c.mu.Lock()
	c.conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
	err := writeClusterMessage(c.writer, msg)
	if err == nil {
		err = c.writer.Flush()
	}
	c.mu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	// Wait for response
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closeCh:
		return nil, errors.New("connection closed")
	case resp := <-respCh:
		return resp, nil
	}
}

func (c *ClusterConnection) readLoop() {
	c.readLoopWithContext(context.Background())
}

func (c *ClusterConnection) readLoopWithContext(ctx context.Context) {
	defer c.wg.Done()
	c.connected.Store(true)
	defer func() {
		c.connected.Store(false)
		close(c.closeCh)
		if c.conn != nil {
			_ = c.conn.Close()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.transport.closeCh:
			return
		default:
		}

		readTimeout := c.readTimeout
		if readTimeout <= 0 {
			readTimeout = 30 * time.Second
		}
		c.conn.SetReadDeadline(time.Now().Add(readTimeout))
		msg, err := readClusterMessage(c.reader, c.maxMsgSize)
		if err != nil {
			if err != io.EOF {
				if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
					log.Printf("[Cluster] Read error: %v", err)
				}
			}
			return
		}

		// Dispatch to pending RPC (single outstanding RPC per connection is expected today).
		c.rpcMu.Lock()
		var (
			deliverCh chan *ClusterMessage
			deliverID uint64
		)
		for id, ch := range c.pendingRPCs {
			deliverCh = ch
			deliverID = id
			break
		}
		if deliverCh != nil {
			select {
			case deliverCh <- msg:
			default:
			}
			delete(c.pendingRPCs, deliverID)
			c.rpcMu.Unlock()
			continue
		}
		c.rpcMu.Unlock()

		// No pending RPC: treat as inbound request.
		if c.transport == nil {
			continue
		}

		c.transport.mu.RLock()
		handler, ok := c.transport.handlers[msg.Type]
		c.transport.mu.RUnlock()
		if !ok {
			log.Printf("[Cluster] No handler for message type %d", msg.Type)
			continue
		}

		resp, err := handler(ctx, msg.NodeID, msg)
		if err != nil {
			log.Printf("[Cluster] Handler error: %v", err)
			continue
		}
		if resp == nil {
			continue
		}

		c.mu.Lock()
		writeTimeout := c.writeTimeout
		if writeTimeout <= 0 {
			writeTimeout = 10 * time.Second
		}
		c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		werr := writeClusterMessage(c.writer, resp)
		if werr == nil {
			werr = c.writer.Flush()
		}
		c.mu.Unlock()

		if werr != nil {
			log.Printf("[Cluster] Write error to %s: %v", c.addr, werr)
			return
		}
	}
}

// SendWALBatch sends WAL entries to the peer.
func (c *ClusterConnection) SendWALBatch(ctx context.Context, entries []*WALEntry) (*WALBatchResponse, error) {
	payload, err := encodeGob(entries)
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	msg := &ClusterMessage{
		Type:    ClusterMsgWALBatch,
		Payload: payload,
	}

	resp, err := c.sendRPC(ctx, msg)
	if err != nil {
		return nil, err
	}

	var result WALBatchResponse
	if err := decodeGob(resp.Payload, &result); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &result, nil
}

// SendHeartbeat sends a heartbeat to the peer.
func (c *ClusterConnection) SendHeartbeat(ctx context.Context, req *HeartbeatRequest) (*HeartbeatResponse, error) {
	payload, err := encodeGob(req)
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	msg := &ClusterMessage{
		Type:    ClusterMsgHeartbeat,
		Payload: payload,
	}

	resp, err := c.sendRPC(ctx, msg)
	if err != nil {
		return nil, err
	}

	var result HeartbeatResponse
	if err := decodeGob(resp.Payload, &result); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &result, nil
}

// SendFence sends a fence request to the peer.
func (c *ClusterConnection) SendFence(ctx context.Context, req *FenceRequest) (*FenceResponse, error) {
	payload, err := encodeGob(req)
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	msg := &ClusterMessage{
		Type:    ClusterMsgFence,
		Payload: payload,
	}

	resp, err := c.sendRPC(ctx, msg)
	if err != nil {
		return nil, err
	}

	var result FenceResponse
	if err := decodeGob(resp.Payload, &result); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &result, nil
}

// SendPromote sends a promote request to the peer.
func (c *ClusterConnection) SendPromote(ctx context.Context, req *PromoteRequest) (*PromoteResponse, error) {
	payload, err := encodeGob(req)
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	msg := &ClusterMessage{
		Type:    ClusterMsgPromote,
		Payload: payload,
	}

	resp, err := c.sendRPC(ctx, msg)
	if err != nil {
		return nil, err
	}

	var result PromoteResponse
	if err := decodeGob(resp.Payload, &result); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &result, nil
}

// SendRaftVote sends a Raft vote request to the peer.
func (c *ClusterConnection) SendRaftVote(ctx context.Context, req *RaftVoteRequest) (*RaftVoteResponse, error) {
	payload, err := encodeGob(req)
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	msg := &ClusterMessage{
		Type:    ClusterMsgVoteRequest,
		Payload: payload,
	}

	resp, err := c.sendRPC(ctx, msg)
	if err != nil {
		return nil, err
	}

	var result RaftVoteResponse
	if err := decodeGob(resp.Payload, &result); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &result, nil
}

// SendRaftAppendEntries sends Raft append entries to the peer.
func (c *ClusterConnection) SendRaftAppendEntries(ctx context.Context, req *RaftAppendEntriesRequest) (*RaftAppendEntriesResponse, error) {
	payload, err := encodeGob(req)
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	msg := &ClusterMessage{
		Type:    ClusterMsgAppendEntries,
		Payload: payload,
	}

	resp, err := c.sendRPC(ctx, msg)
	if err != nil {
		return nil, err
	}

	var result RaftAppendEntriesResponse
	if err := decodeGob(resp.Payload, &result); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &result, nil
}

// Close closes the connection.
func (c *ClusterConnection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		c.conn.Close()
	}
	c.wg.Wait()
	return nil
}

// IsConnected returns true if the connection is active.
func (c *ClusterConnection) IsConnected() bool {
	return c.connected.Load()
}

// Wire protocol helpers

func writeClusterMessage(w *bufio.Writer, msg *ClusterMessage) error {
	data, err := encodeGob(msg)
	if err != nil {
		return err
	}

	// Length prefix (4 bytes, big endian)
	length := uint32(len(data))
	if err := binary.Write(w, binary.BigEndian, length); err != nil {
		return err
	}

	_, err = w.Write(data)
	return err
}

func readClusterMessage(r *bufio.Reader, maxSize int) (*ClusterMessage, error) {
	// Read length prefix
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, err
	}

	if int(length) > maxSize {
		return nil, fmt.Errorf("message too large: %d > %d", length, maxSize)
	}

	// Read data
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}

	var msg ClusterMessage
	if err := decodeGob(data, &msg); err != nil {
		return nil, err
	}

	return &msg, nil
}

// Verify interface compliance
var _ Transport = (*ClusterTransport)(nil)
var _ PeerConnection = (*ClusterConnection)(nil)
