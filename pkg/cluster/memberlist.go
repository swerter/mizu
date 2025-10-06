package cluster

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/hashicorp/memberlist"
	"go.uber.org/zap"
)

// MessageType identifies the type of gossip message
type MessageType byte

const (
	MessageTypeConnectionState MessageType = iota
	MessageTypeRateLimit
)

// GossipMessage is the envelope for all gossip messages
type GossipMessage struct {
	Type    MessageType     `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// StateDelegate defines the interface for state synchronization
type StateDelegate interface {
	GetState() []byte
	MergeState(data []byte)
}

// EventDelegate defines the interface for cluster membership events
type EventDelegate interface {
	NotifyJoin(node *memberlist.Node)
	NotifyLeave(node *memberlist.Node)
	NotifyUpdate(node *memberlist.Node)
}

// Cluster manages memberlist-based cluster membership and gossip
type Cluster struct {
	ml                     *memberlist.Memberlist
	broadcasts             *memberlist.TransmitLimitedQueue
	logger                 *zap.Logger
	connectionStateHandler func(data []byte)
	rateLimitHandler       func(data []byte)
	stateDelegate          StateDelegate
	eventDelegate          EventDelegate
}

// Config holds configuration for creating a cluster
type Config struct {
	NodeName      string   // This node's name (defaults to hostname)
	BindAddr      string   // Address to bind to (e.g., "0.0.0.0")
	BindPort      int      // Port to bind to for memberlist (default: 7946)
	AdvertiseAddr string   // Address to advertise to other nodes (optional)
	AdvertisePort int      // Port to advertise (optional)
	SeedNodes     []string // Initial nodes to join (e.g., ["node1:7946", "node2:7946"])
	Logger        *zap.Logger
	StateDelegate StateDelegate
	EventDelegate EventDelegate
}

// NewCluster creates a new cluster instance with memberlist
func NewCluster(cfg Config) (*Cluster, error) {
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}

	cluster := &Cluster{
		logger:        cfg.Logger,
		stateDelegate: cfg.StateDelegate,
		eventDelegate: cfg.EventDelegate,
	}

	// Create memberlist configuration
	mlConfig := memberlist.DefaultLANConfig()

	// Set event delegate for cluster membership events
	mlConfig.Events = cluster

	// Set node name
	if cfg.NodeName != "" {
		mlConfig.Name = cfg.NodeName
	}

	// Set bind address and port
	if cfg.BindAddr != "" {
		mlConfig.BindAddr = cfg.BindAddr
	}
	if cfg.BindPort > 0 {
		mlConfig.BindPort = cfg.BindPort
	}

	// Set advertise address and port
	if cfg.AdvertiseAddr != "" {
		mlConfig.AdvertiseAddr = cfg.AdvertiseAddr
	}
	if cfg.AdvertisePort > 0 {
		mlConfig.AdvertisePort = cfg.AdvertisePort
	}

	// Set delegate for custom gossip messages
	mlConfig.Delegate = cluster

	// Disable memberlist's built-in logging (we'll use zap)
	mlConfig.LogOutput = &zapLogWriter{logger: cfg.Logger}

	// Create memberlist
	ml, err := memberlist.Create(mlConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create memberlist: %w", err)
	}

	cluster.ml = ml

	// Create broadcast queue for efficient message distribution
	cluster.broadcasts = &memberlist.TransmitLimitedQueue{
		NumNodes: func() int {
			return ml.NumMembers()
		},
		RetransmitMult: 3, // Retransmit to 3x nodes for reliability
	}

	// Join seed nodes if provided
	if len(cfg.SeedNodes) > 0 {
		_, err := ml.Join(cfg.SeedNodes)
		if err != nil {
			cfg.Logger.Warn("Failed to join some seed nodes", zap.Error(err))
			// Don't fail completely - we might be the first node
		}
	}

	cfg.Logger.Info("Cluster started",
		zap.String("node_name", ml.LocalNode().Name),
		zap.String("bind_addr", mlConfig.BindAddr),
		zap.Int("bind_port", mlConfig.BindPort),
		zap.Int("members", ml.NumMembers()))

	return cluster, nil
}

// SetStateDelegate sets the state delegate for the cluster
func (c *Cluster) SetStateDelegate(delegate StateDelegate) {
	c.stateDelegate = delegate
}

// SetEventDelegate sets the event delegate for the cluster
func (c *Cluster) SetEventDelegate(delegate EventDelegate) {
	c.eventDelegate = delegate
}

// RegisterConnectionStateHandler registers a handler for connection state gossip
func (c *Cluster) RegisterConnectionStateHandler(handler func(data []byte)) {
	c.connectionStateHandler = handler
}

// RegisterRateLimitHandler registers a handler for rate limit gossip
func (c *Cluster) RegisterRateLimitHandler(handler func(data []byte)) {
	c.rateLimitHandler = handler
}

// BroadcastConnectionState broadcasts connection state to the cluster
func (c *Cluster) BroadcastConnectionState(data []byte) error {
	msg := GossipMessage{
		Type:    MessageTypeConnectionState,
		Payload: json.RawMessage(data),
	}

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	c.broadcasts.QueueBroadcast(&broadcast{
		msg:    msgBytes,
		notify: nil,
	})

	return nil
}

// BroadcastRateLimit broadcasts rate limit data to the cluster
func (c *Cluster) BroadcastRateLimit(data []byte) error {
	msg := GossipMessage{
		Type:    MessageTypeRateLimit,
		Payload: json.RawMessage(data),
	}

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	c.broadcasts.QueueBroadcast(&broadcast{
		msg:    msgBytes,
		notify: nil,
	})

	return nil
}

// Members returns the list of cluster members
func (c *Cluster) Members() []*memberlist.Node {
	return c.ml.Members()
}

// NumMembers returns the number of cluster members
func (c *Cluster) NumMembers() int {
	return c.ml.NumMembers()
}

// LocalNode returns the local node
func (c *Cluster) LocalNode() *memberlist.Node {
	return c.ml.LocalNode()
}

// Shutdown gracefully shuts down the cluster
func (c *Cluster) Shutdown() error {
	if c.ml != nil {
		return c.ml.Shutdown()
	}
	return nil
}

// --- Memberlist Delegate Interface Implementation ---

// NodeMeta is used to retrieve meta-data about the current node
func (c *Cluster) NodeMeta(limit int) []byte {
	// Could include node metadata here (e.g., version, capabilities)
	return []byte{}
}

// NotifyMsg is called when a user-data message is received
func (c *Cluster) NotifyMsg(data []byte) {
	var msg GossipMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		c.logger.Warn("Failed to unmarshal gossip message", zap.Error(err))
		return
	}

	switch msg.Type {
	case MessageTypeConnectionState:
		if c.connectionStateHandler != nil {
			c.connectionStateHandler(msg.Payload)
		}
	case MessageTypeRateLimit:
		if c.rateLimitHandler != nil {
			c.rateLimitHandler(msg.Payload)
		}
	default:
		c.logger.Warn("Unknown gossip message type", zap.Uint8("type", uint8(msg.Type)))
	}
}

// GetBroadcasts is called when user data messages can be broadcast
func (c *Cluster) GetBroadcasts(overhead, limit int) [][]byte {
	return c.broadcasts.GetBroadcasts(overhead, limit)
}

// LocalState is used for state synchronization on node join
func (c *Cluster) LocalState(join bool) []byte {
	if c.stateDelegate != nil {
		return c.stateDelegate.GetState()
	}
	return []byte{}
}

// MergeRemoteState is called to merge state from a remote node
func (c *Cluster) MergeRemoteState(buf []byte, join bool) {
	if c.stateDelegate != nil {
		c.stateDelegate.MergeState(buf)
	}
}

// --- EventDelegate Interface Implementation ---

// NotifyJoin is called when a node joins the cluster
func (c *Cluster) NotifyJoin(node *memberlist.Node) {
	c.logger.Info("Node joined cluster",
		zap.String("node", node.Name),
		zap.String("addr", node.Address()))

	if c.eventDelegate != nil {
		c.eventDelegate.NotifyJoin(node)
	}
}

// NotifyLeave is called when a node leaves the cluster gracefully
func (c *Cluster) NotifyLeave(node *memberlist.Node) {
	c.logger.Info("Node left cluster",
		zap.String("node", node.Name),
		zap.String("addr", node.Address()))

	if c.eventDelegate != nil {
		c.eventDelegate.NotifyLeave(node)
	}
}

// NotifyUpdate is called when a node's metadata is updated
func (c *Cluster) NotifyUpdate(node *memberlist.Node) {
	c.logger.Debug("Node updated",
		zap.String("node", node.Name),
		zap.String("addr", node.Address()))

	if c.eventDelegate != nil {
		c.eventDelegate.NotifyUpdate(node)
	}
}

// --- Helper Types ---

// broadcast implements memberlist.Broadcast
type broadcast struct {
	msg    []byte
	notify chan<- struct{}
}

func (b *broadcast) Invalidates(other memberlist.Broadcast) bool {
	return false // Don't invalidate other broadcasts
}

func (b *broadcast) Message() []byte {
	return b.msg
}

func (b *broadcast) Finished() {
	if b.notify != nil {
		close(b.notify)
	}
}

// zapLogWriter adapts zap.Logger to io.Writer for memberlist
type zapLogWriter struct {
	logger *zap.Logger
}

func (w *zapLogWriter) Write(p []byte) (n int, err error) {
	w.logger.Debug(string(p))
	return len(p), nil
}

// HealthStatus returns cluster health information
func (c *Cluster) HealthStatus() map[string]interface{} {
	members := c.Members()
	alive := 0
	suspect := 0
	dead := 0

	for _, member := range members {
		switch member.State {
		case memberlist.StateAlive:
			alive++
		case memberlist.StateSuspect:
			suspect++
		case memberlist.StateDead:
			dead++
		}
	}

	return map[string]interface{}{
		"node_name":       c.LocalNode().Name,
		"total_members":   len(members),
		"alive_members":   alive,
		"suspect_members": suspect,
		"dead_members":    dead,
		"bind_addr":       net.JoinHostPort(c.ml.LocalNode().Addr.String(), fmt.Sprintf("%d", c.ml.LocalNode().Port)),
	}
}

// WaitForMembers waits until at least minMembers are in the cluster or timeout is reached
func (c *Cluster) WaitForMembers(minMembers int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c.NumMembers() >= minMembers {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %d members (current: %d)", minMembers, c.NumMembers())
}
