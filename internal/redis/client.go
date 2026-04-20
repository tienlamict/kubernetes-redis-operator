// Package redis provides a high-level wrapper around the go-redis client for
// executing Redis commands against individual pods within the cluster network.
package redis

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// RedisClient defines all Redis commands the operator needs to execute.
type RedisClient interface {
	// Basic commands
	Ping(ctx context.Context, addr string) error
	Info(ctx context.Context, addr string, section string) (map[string]string, error)
	ConfigSet(ctx context.Context, addr string, param, value string) error
	ConfigGet(ctx context.Context, addr string, param string) (string, error)
	Save(ctx context.Context, addr string) error

	// Cluster commands
	ClusterInfo(ctx context.Context, addr string) (*ClusterInfo, error)
	ClusterNodes(ctx context.Context, addr string) ([]ClusterNode, error)
	ClusterMeet(ctx context.Context, addr, targetIP string, targetPort int) error
	ClusterAddSlots(ctx context.Context, addr string, slots ...int) error
	ClusterDelSlots(ctx context.Context, addr string, slots ...int) error
	ClusterReplicate(ctx context.Context, addr, masterNodeID string) error
	ClusterFailover(ctx context.Context, addr string) error
	ClusterForget(ctx context.Context, addr, nodeID string) error
	ClusterReset(ctx context.Context, addr string, hard bool) error
	ClusterSetSlot(ctx context.Context, addr string, slot int, subcommand string, nodeID string) error
	ClusterGetKeysInSlot(ctx context.Context, addr string, slot, count int) ([]string, error)
	ClusterCountKeysInSlot(ctx context.Context, addr string, slot int) (int64, error)
	Migrate(ctx context.Context, addr, targetIP string, targetPort int, key string, db, timeoutMs int) error

	// Sentinel commands
	SentinelMaster(ctx context.Context, addr, masterName string) (map[string]string, error)
	SentinelGetMasterAddr(ctx context.Context, addr, masterName string) (string, int, error)
	SentinelFailover(ctx context.Context, addr, masterName string) error
	SentinelReset(ctx context.Context, addr, pattern string) error

	// Replication
	ReplicaOf(ctx context.Context, addr, masterHost string, masterPort int) error
	Role(ctx context.Context, addr string) (string, error)
}

// ClientOptions holds the connection configuration for a Redis client instance.
type ClientOptions struct {
	Password  string
	TLSConfig *tls.Config
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// DefaultClientOptions returns sensible operator defaults.
func DefaultClientOptions() ClientOptions {
	return ClientOptions{
		DialTimeout:  5 * time.Second,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
}

// client is the concrete implementation of RedisClient backed by go-redis.
type client struct {
	opts ClientOptions
}

// NewClient creates a new RedisClient.
func NewClient(opts ClientOptions) RedisClient {
	return &client{opts: opts}
}

// NewTLSConfig builds a *tls.Config from PEM-encoded certificate files.
func NewTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("loading TLS key pair: %w", err)
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("reading CA certificate: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// newGoRedisClient creates a connected go-redis client for the given address.
func (c *client) newGoRedisClient(addr string) *goredis.Client {
	opts := &goredis.Options{
		Addr:         addr,
		Password:     c.opts.Password,
		DialTimeout:  c.opts.DialTimeout,
		ReadTimeout:  c.opts.ReadTimeout,
		WriteTimeout: c.opts.WriteTimeout,
		TLSConfig:    c.opts.TLSConfig,
	}
	return goredis.NewClient(opts)
}

func (c *client) Ping(ctx context.Context, addr string) error {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	return r.Ping(ctx).Err()
}

func (c *client) Info(ctx context.Context, addr string, section string) (map[string]string, error) {
	r := c.newGoRedisClient(addr)
	defer r.Close()

	var raw string
	var err error
	if section == "" {
		raw, err = r.Info(ctx).Result()
	} else {
		raw, err = r.Info(ctx, section).Result()
	}
	if err != nil {
		return nil, err
	}
	return parseInfoOutput(raw), nil
}

// parseInfoOutput converts Redis INFO output into a key-value map.
func parseInfoOutput(raw string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return result
}

func (c *client) ConfigSet(ctx context.Context, addr string, param, value string) error {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	return r.ConfigSet(ctx, param, value).Err()
}

func (c *client) ConfigGet(ctx context.Context, addr string, param string) (string, error) {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	vals, err := r.ConfigGet(ctx, param).Result()
	if err != nil {
		return "", err
	}
	if v, ok := vals[param]; ok {
		return v, nil
	}
	return "", nil
}

func (c *client) Save(ctx context.Context, addr string) error {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	return r.Save(ctx).Err()
}

// ClusterInfo parses CLUSTER INFO output into a structured type.
func (c *client) ClusterInfo(ctx context.Context, addr string) (*ClusterInfo, error) {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	raw, err := r.ClusterInfo(ctx).Result()
	if err != nil {
		return nil, err
	}
	return parseClusterInfo(raw)
}

// ClusterInfo holds parsed output of CLUSTER INFO.
type ClusterInfo struct {
	State            string
	SlotsAssigned    int
	SlotsOK          int
	SlotsPFail       int
	SlotsFail        int
	KnownNodes       int
	ClusterSize      int
	CurrentEpoch     int64
	MyEpoch          int64
	StatsMessagesSent    int64
	StatsMessagesReceived int64
}

func parseClusterInfo(raw string) (*ClusterInfo, error) {
	info := &ClusterInfo{}
	m := parseInfoOutput(raw)
	info.State = m["cluster_state"]
	if v, err := strconv.Atoi(m["cluster_slots_assigned"]); err == nil {
		info.SlotsAssigned = v
	}
	if v, err := strconv.Atoi(m["cluster_slots_ok"]); err == nil {
		info.SlotsOK = v
	}
	if v, err := strconv.Atoi(m["cluster_slots_pfail"]); err == nil {
		info.SlotsPFail = v
	}
	if v, err := strconv.Atoi(m["cluster_slots_fail"]); err == nil {
		info.SlotsFail = v
	}
	if v, err := strconv.Atoi(m["cluster_known_nodes"]); err == nil {
		info.KnownNodes = v
	}
	if v, err := strconv.Atoi(m["cluster_size"]); err == nil {
		info.ClusterSize = v
	}
	return info, nil
}

// ClusterNode represents a single entry from CLUSTER NODES output.
type ClusterNode struct {
	NodeID      string
	Addr        string
	Flags       []string
	MasterID    string
	PingSent    int64
	PongRecv    int64
	ConfigEpoch int64
	LinkState   string
	Slots       []SlotRange
}

// SlotRange is an inclusive range of Redis hash slots.
type SlotRange struct {
	Start int
	End   int
}

func (c *client) ClusterNodes(ctx context.Context, addr string) ([]ClusterNode, error) {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	raw, err := r.ClusterNodes(ctx).Result()
	if err != nil {
		return nil, err
	}
	return parseClusterNodes(raw)
}

// parseClusterNodes parses the text output of CLUSTER NODES.
// Format: <id> <ip:port@busport> <flags> <master> <ping-sent> <pong-recv> <config-epoch> <link-state> <slot> ...
func parseClusterNodes(raw string) ([]ClusterNode, error) {
	var nodes []ClusterNode
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 8 {
			continue
		}
		node := ClusterNode{
			NodeID:    parts[0],
			Flags:     strings.Split(parts[2], ","),
			MasterID:  parts[3],
			LinkState: parts[7],
		}
		// Parse address: strip the cluster-bus port portion (@16379).
		addrFull := parts[1]
		if idx := strings.Index(addrFull, "@"); idx != -1 {
			addrFull = addrFull[:idx]
		}
		node.Addr = addrFull

		if v, err := strconv.ParseInt(parts[4], 10, 64); err == nil {
			node.PingSent = v
		}
		if v, err := strconv.ParseInt(parts[5], 10, 64); err == nil {
			node.PongRecv = v
		}
		if v, err := strconv.ParseInt(parts[6], 10, 64); err == nil {
			node.ConfigEpoch = v
		}
		for _, slotStr := range parts[8:] {
			sr, err := parseSlotRange(slotStr)
			if err == nil {
				node.Slots = append(node.Slots, sr)
			}
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func parseSlotRange(s string) (SlotRange, error) {
	if idx := strings.Index(s, "-"); idx != -1 {
		start, err1 := strconv.Atoi(s[:idx])
		end, err2 := strconv.Atoi(s[idx+1:])
		if err1 != nil || err2 != nil {
			return SlotRange{}, fmt.Errorf("invalid slot range: %s", s)
		}
		return SlotRange{Start: start, End: end}, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return SlotRange{}, fmt.Errorf("invalid slot: %s", s)
	}
	return SlotRange{Start: v, End: v}, nil
}

func (c *client) ClusterMeet(ctx context.Context, addr, targetIP string, targetPort int) error {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	return r.ClusterMeet(ctx, targetIP, strconv.Itoa(targetPort)).Err()
}

func (c *client) ClusterAddSlots(ctx context.Context, addr string, slots ...int) error {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	return r.ClusterAddSlots(ctx, slots...).Err()
}

func (c *client) ClusterDelSlots(ctx context.Context, addr string, slots ...int) error {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	return r.ClusterDelSlots(ctx, slots...).Err()
}

func (c *client) ClusterReplicate(ctx context.Context, addr, masterNodeID string) error {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	return r.ClusterReplicate(ctx, masterNodeID).Err()
}

func (c *client) ClusterFailover(ctx context.Context, addr string) error {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	return r.ClusterFailover(ctx).Err()
}

func (c *client) ClusterForget(ctx context.Context, addr, nodeID string) error {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	return r.ClusterForget(ctx, nodeID).Err()
}

func (c *client) ClusterReset(ctx context.Context, addr string, hard bool) error {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	resetType := "SOFT"
	if hard {
		resetType = "HARD"
	}
	return r.Do(ctx, "CLUSTER", "RESET", resetType).Err()
}

func (c *client) ClusterSetSlot(ctx context.Context, addr string, slot int, subcommand string, nodeID string) error {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	if nodeID != "" {
		return r.Do(ctx, "CLUSTER", "SETSLOT", slot, subcommand, nodeID).Err()
	}
	return r.Do(ctx, "CLUSTER", "SETSLOT", slot, subcommand).Err()
}

func (c *client) ClusterGetKeysInSlot(ctx context.Context, addr string, slot, count int) ([]string, error) {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	return r.ClusterGetKeysInSlot(ctx, slot, count).Result()
}

func (c *client) ClusterCountKeysInSlot(ctx context.Context, addr string, slot int) (int64, error) {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	return r.ClusterCountKeysInSlot(ctx, slot).Result()
}

func (c *client) Migrate(ctx context.Context, addr, targetIP string, targetPort int, key string, db, timeoutMs int) error {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	return r.Migrate(ctx, targetIP, strconv.Itoa(targetPort), key, db, time.Duration(timeoutMs)*time.Millisecond).Err()
}

func (c *client) SentinelMaster(ctx context.Context, addr, masterName string) (map[string]string, error) {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	result, err := r.Do(ctx, "SENTINEL", "MASTER", masterName).StringSlice()
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(result)/2)
	for i := 0; i+1 < len(result); i += 2 {
		m[result[i]] = result[i+1]
	}
	return m, nil
}

func (c *client) SentinelGetMasterAddr(ctx context.Context, addr, masterName string) (string, int, error) {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	result, err := r.Do(ctx, "SENTINEL", "GET-MASTER-ADDR-BY-NAME", masterName).StringSlice()
	if err != nil {
		return "", 0, err
	}
	if len(result) < 2 {
		return "", 0, fmt.Errorf("unexpected SENTINEL GET-MASTER-ADDR-BY-NAME response: %v", result)
	}
	port, err := strconv.Atoi(result[1])
	if err != nil {
		return "", 0, fmt.Errorf("invalid port in SENTINEL response: %w", err)
	}
	return result[0], port, nil
}

func (c *client) SentinelFailover(ctx context.Context, addr, masterName string) error {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	return r.Do(ctx, "SENTINEL", "FAILOVER", masterName).Err()
}

func (c *client) SentinelReset(ctx context.Context, addr, pattern string) error {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	return r.Do(ctx, "SENTINEL", "RESET", pattern).Err()
}

func (c *client) ReplicaOf(ctx context.Context, addr, masterHost string, masterPort int) error {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	return r.Do(ctx, "REPLICAOF", masterHost, strconv.Itoa(masterPort)).Err()
}

func (c *client) Role(ctx context.Context, addr string) (string, error) {
	r := c.newGoRedisClient(addr)
	defer r.Close()
	result, err := r.Do(ctx, "ROLE").Result()
	if err != nil {
		return "", err
	}
	switch v := result.(type) {
	case []interface{}:
		if len(v) > 0 {
			if role, ok := v[0].(string); ok {
				return role, nil
			}
		}
	}
	return "", fmt.Errorf("unexpected ROLE response: %v", result)
}
