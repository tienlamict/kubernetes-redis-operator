package redis

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	totalHashSlots = 16384
	migrateTimeout = 5000 // milliseconds
	migrateBatch   = 100  // keys per CLUSTER GETKEYSINSLOT call
)

// ClusterManager provides high-level Redis Cluster operations built on top of RedisClient.
type ClusterManager interface {
	FormCluster(ctx context.Context, masterAddrs []string, replicaAddrs []string, replicasPerMaster int) error
	AssignSlotsEvenly(ctx context.Context, masterAddrs []string) error
	MigrateSlot(ctx context.Context, sourceAddr, destAddr string, slot int) error
	MigrateSlotsRange(ctx context.Context, sourceAddr, destAddr string, startSlot, endSlot int) error
	RebalanceSlots(ctx context.Context, masterAddrs []string) error
	AddMaster(ctx context.Context, existingAddr, newAddr string) error
	RemoveMaster(ctx context.Context, clusterAddr, removeAddr string) error
	AddReplica(ctx context.Context, replicaAddr, masterAddr string) error
	RemoveReplica(ctx context.Context, clusterAddr, replicaAddr string) error
	GetClusterState(ctx context.Context, addr string) (*ClusterState, error)
	WaitForClusterReady(ctx context.Context, addr string, timeout time.Duration) error
	VerifySlotCoverage(ctx context.Context, addr string) (bool, error)
}

// ClusterState holds a snapshot of the Redis Cluster topology.
type ClusterState struct {
	State      string
	SlotsAssigned int
	SlotsOK    int
	SlotsPFail int
	SlotsFail  int
	KnownNodes int
	Size       int
	Nodes      []ClusterNode
}

type clusterManager struct {
	client RedisClient
}

// NewClusterManager creates a ClusterManager backed by the given RedisClient.
func NewClusterManager(c RedisClient) ClusterManager {
	return &clusterManager{client: c}
}

// FormCluster bootstraps a new Redis Cluster:
// 1. MEETs all masters into a single cluster
// 2. Assigns hash slots evenly across masters
// 3. MEETs replica nodes and assigns them to masters
func (m *clusterManager) FormCluster(ctx context.Context, masterAddrs, replicaAddrs []string, replicasPerMaster int) error {
	if len(masterAddrs) < 3 {
		return fmt.Errorf("need at least 3 masters to form a cluster, got %d", len(masterAddrs))
	}

	// Step 1: Meet all masters with each other.
	for i, addr := range masterAddrs {
		for j, peer := range masterAddrs {
			if i == j {
				continue
			}
			ip, port, err := splitAddr(peer)
			if err != nil {
				return fmt.Errorf("invalid master address %q: %w", peer, err)
			}
			if err := m.client.ClusterMeet(ctx, addr, ip, port); err != nil {
				return fmt.Errorf("CLUSTER MEET %s → %s: %w", addr, peer, err)
			}
		}
	}

	// Wait for all nodes to recognise each other.
	if err := m.waitForKnownNodes(ctx, masterAddrs[0], len(masterAddrs), 30*time.Second); err != nil {
		return fmt.Errorf("waiting for cluster formation: %w", err)
	}

	// Step 2: Assign hash slots evenly.
	if err := m.AssignSlotsEvenly(ctx, masterAddrs); err != nil {
		return fmt.Errorf("assigning slots: %w", err)
	}

	// Wait for cluster_state:ok.
	if err := m.WaitForClusterReady(ctx, masterAddrs[0], 60*time.Second); err != nil {
		return fmt.Errorf("waiting for cluster ready after slot assignment: %w", err)
	}

	if len(replicaAddrs) == 0 || replicasPerMaster == 0 {
		return nil
	}

	// Step 3: Add replicas.
	for _, rAddr := range replicaAddrs {
		ip, port, err := splitAddr(rAddr)
		if err != nil {
			return fmt.Errorf("invalid replica address %q: %w", rAddr, err)
		}
		if err := m.client.ClusterMeet(ctx, masterAddrs[0], ip, port); err != nil {
			return fmt.Errorf("CLUSTER MEET replica %s: %w", rAddr, err)
		}
	}

	// Wait for replicas to join.
	totalNodes := len(masterAddrs) + len(replicaAddrs)
	if err := m.waitForKnownNodes(ctx, masterAddrs[0], totalNodes, 60*time.Second); err != nil {
		return fmt.Errorf("waiting for replicas to join: %w", err)
	}

	// Resolve master node IDs.
	nodes, err := m.client.ClusterNodes(ctx, masterAddrs[0])
	if err != nil {
		return fmt.Errorf("fetching cluster nodes: %w", err)
	}
	masterIDs := masterNodeIDs(nodes)
	if len(masterIDs) != len(masterAddrs) {
		return fmt.Errorf("expected %d master IDs, found %d", len(masterAddrs), len(masterIDs))
	}

	// Assign replicas to masters in round-robin order.
	for i, rAddr := range replicaAddrs {
		masterID := masterIDs[i%len(masterIDs)]
		if err := m.client.ClusterReplicate(ctx, rAddr, masterID); err != nil {
			return fmt.Errorf("CLUSTER REPLICATE %s → %s: %w", rAddr, masterID, err)
		}
	}
	return nil
}

// AssignSlotsEvenly distributes all 16384 hash slots evenly across the given masters.
func (m *clusterManager) AssignSlotsEvenly(ctx context.Context, masterAddrs []string) error {
	n := len(masterAddrs)
	if n == 0 {
		return fmt.Errorf("no master addresses provided")
	}
	slotsPerMaster := totalHashSlots / n

	for i, addr := range masterAddrs {
		start := i * slotsPerMaster
		end := start + slotsPerMaster - 1
		if i == n-1 {
			end = totalHashSlots - 1 // last master gets the remainder
		}
		slots := make([]int, 0, end-start+1)
		for s := start; s <= end; s++ {
			slots = append(slots, s)
		}
		if err := m.client.ClusterAddSlots(ctx, addr, slots...); err != nil {
			return fmt.Errorf("CLUSTER ADDSLOTS on %s (slots %d–%d): %w", addr, start, end, err)
		}
	}
	return nil
}

// MigrateSlot migrates a single hash slot from sourceAddr to destAddr.
// If key migration fails after the slot has been placed in MIGRATING/IMPORTING state,
// CLUSTER SETSLOT STABLE is issued on both nodes so the cluster can recover cleanly.
func (m *clusterManager) MigrateSlot(ctx context.Context, sourceAddr, destAddr string, slot int) error {
	destIP, destPort, err := splitAddr(destAddr)
	if err != nil {
		return err
	}

	// Get the destination node ID.
	destNodes, err := m.client.ClusterNodes(ctx, destAddr)
	if err != nil {
		return fmt.Errorf("fetching dest nodes: %w", err)
	}
	destNodeID := selfNodeID(destNodes)
	if destNodeID == "" {
		return fmt.Errorf("could not determine destination node ID for %s", destAddr)
	}

	// Get the source node ID.
	srcNodes, err := m.client.ClusterNodes(ctx, sourceAddr)
	if err != nil {
		return fmt.Errorf("fetching source nodes: %w", err)
	}
	srcNodeID := selfNodeID(srcNodes)
	if srcNodeID == "" {
		return fmt.Errorf("could not determine source node ID for %s", sourceAddr)
	}

	// IMPORTING on destination.
	if err := m.client.ClusterSetSlot(ctx, destAddr, slot, "IMPORTING", srcNodeID); err != nil {
		return fmt.Errorf("CLUSTER SETSLOT %d IMPORTING: %w", slot, err)
	}

	// MIGRATING on source.
	if err := m.client.ClusterSetSlot(ctx, sourceAddr, slot, "MIGRATING", destNodeID); err != nil {
		// Roll back the IMPORTING state so the slot isn't stuck.
		_ = m.client.ClusterSetSlot(ctx, destAddr, slot, "STABLE", "")
		return fmt.Errorf("CLUSTER SETSLOT %d MIGRATING: %w", slot, err)
	}

	// Migrate all keys in batches.  On failure, reset both ends to STABLE so the
	// cluster doesn't get stuck in a partial-migration state (spec 11.4).
	if err := m.migrateKeys(ctx, sourceAddr, destIP, destPort, slot); err != nil {
		_ = m.client.ClusterSetSlot(ctx, sourceAddr, slot, "STABLE", "")
		_ = m.client.ClusterSetSlot(ctx, destAddr, slot, "STABLE", "")
		return fmt.Errorf("migrating keys in slot %d: %w", slot, err)
	}

	// Commit the slot to the destination node on all cluster members.
	allNodes, err := m.client.ClusterNodes(ctx, sourceAddr)
	if err != nil {
		return fmt.Errorf("fetching all nodes to commit slot %d: %w", slot, err)
	}
	for _, node := range allNodes {
		if err := m.client.ClusterSetSlot(ctx, node.Addr, slot, "NODE", destNodeID); err != nil {
			// Non-fatal: other nodes will receive this via gossip.
			_ = err
		}
	}
	return nil
}

// migrateKeys moves all keys in a slot from source to destination in batches.
func (m *clusterManager) migrateKeys(ctx context.Context, sourceAddr, destIP string, destPort, slot int) error {
	for {
		keys, err := m.client.ClusterGetKeysInSlot(ctx, sourceAddr, slot, migrateBatch)
		if err != nil {
			return fmt.Errorf("CLUSTER GETKEYSINSLOT %d: %w", slot, err)
		}
		if len(keys) == 0 {
			return nil
		}
		for _, key := range keys {
			if err := m.client.Migrate(ctx, sourceAddr, destIP, destPort, key, 0, migrateTimeout); err != nil {
				return fmt.Errorf("MIGRATE key %q to %s:%d: %w", key, destIP, destPort, err)
			}
		}
	}
}

// MigrateSlotsRange migrates a contiguous range of slots.
func (m *clusterManager) MigrateSlotsRange(ctx context.Context, sourceAddr, destAddr string, startSlot, endSlot int) error {
	for slot := startSlot; slot <= endSlot; slot++ {
		if err := m.MigrateSlot(ctx, sourceAddr, destAddr, slot); err != nil {
			return err
		}
	}
	return nil
}

// RebalanceSlots redistributes existing slots so each master holds approximately 16384/N slots.
func (m *clusterManager) RebalanceSlots(ctx context.Context, masterAddrs []string) error {
	n := len(masterAddrs)
	target := totalHashSlots / n

	nodes, err := m.client.ClusterNodes(ctx, masterAddrs[0])
	if err != nil {
		return err
	}

	slotCounts := make(map[string]int, n)
	nodeAddrByID := make(map[string]string, n)
	for _, node := range nodes {
		if isMaster(node) {
			total := 0
			for _, sr := range node.Slots {
				total += sr.End - sr.Start + 1
			}
			slotCounts[node.NodeID] = total
			nodeAddrByID[node.NodeID] = node.Addr
		}
	}

	// Identify donors (have more than target) and receivers (have less).
	for donorID, count := range slotCounts {
		excess := count - target
		if excess <= 0 {
			continue
		}
		donorAddr := nodeAddrByID[donorID]
		donorNodes, err := m.client.ClusterNodes(ctx, donorAddr)
		if err != nil {
			return err
		}
		for _, receiverAddr := range masterAddrs {
			if receiverAddr == donorAddr || excess <= 0 {
				continue
			}
			receiverNodeID := nodeIDForAddr(donorNodes, receiverAddr)
			if receiverNodeID == "" {
				continue
			}
			toMove := slotCounts[receiverNodeID]
			if toMove >= target {
				continue
			}
			needed := target - toMove
			if needed > excess {
				needed = excess
			}
			slotsToDonate := collectSlots(donorNodes, donorID, needed)
			for _, slot := range slotsToDonate {
				if err := m.MigrateSlot(ctx, donorAddr, receiverAddr, slot); err != nil {
					return err
				}
			}
			excess -= needed
		}
	}
	return nil
}

// AddMaster adds a new node to the cluster and meets it with an existing member.
func (m *clusterManager) AddMaster(ctx context.Context, existingAddr, newAddr string) error {
	ip, port, err := splitAddr(newAddr)
	if err != nil {
		return err
	}
	return m.client.ClusterMeet(ctx, existingAddr, ip, port)
}

// RemoveMaster removes a master node from the cluster (its slots must already be migrated away).
func (m *clusterManager) RemoveMaster(ctx context.Context, clusterAddr, removeAddr string) error {
	nodes, err := m.client.ClusterNodes(ctx, clusterAddr)
	if err != nil {
		return err
	}
	removeID := nodeIDForAddr(nodes, removeAddr)
	if removeID == "" {
		return fmt.Errorf("node %s not found in cluster", removeAddr)
	}
	if err := m.client.ClusterReset(ctx, removeAddr, false); err != nil {
		return fmt.Errorf("CLUSTER RESET on %s: %w", removeAddr, err)
	}
	for _, node := range nodes {
		if node.Addr == removeAddr {
			continue
		}
		if err := m.client.ClusterForget(ctx, node.Addr, removeID); err != nil {
			// Non-fatal: node may already be forgotten.
			_ = err
		}
	}
	return nil
}

// AddReplica adds a replica node to the cluster and replicates from the given master.
func (m *clusterManager) AddReplica(ctx context.Context, replicaAddr, masterAddr string) error {
	ip, port, err := splitAddr(replicaAddr)
	if err != nil {
		return err
	}
	if err := m.client.ClusterMeet(ctx, masterAddr, ip, port); err != nil {
		return err
	}
	nodes, err := m.client.ClusterNodes(ctx, masterAddr)
	if err != nil {
		return err
	}
	masterID := nodeIDForAddr(nodes, masterAddr)
	if masterID == "" {
		return fmt.Errorf("master %s not found in cluster nodes", masterAddr)
	}
	return m.client.ClusterReplicate(ctx, replicaAddr, masterID)
}

// RemoveReplica removes a replica node from the cluster.
func (m *clusterManager) RemoveReplica(ctx context.Context, clusterAddr, replicaAddr string) error {
	nodes, err := m.client.ClusterNodes(ctx, clusterAddr)
	if err != nil {
		return err
	}
	replicaID := nodeIDForAddr(nodes, replicaAddr)
	if replicaID == "" {
		return fmt.Errorf("replica %s not found in cluster", replicaAddr)
	}
	if err := m.client.ClusterReset(ctx, replicaAddr, false); err != nil {
		return fmt.Errorf("CLUSTER RESET on %s: %w", replicaAddr, err)
	}
	for _, node := range nodes {
		if node.Addr == replicaAddr {
			continue
		}
		if err := m.client.ClusterForget(ctx, node.Addr, replicaID); err != nil {
			_ = err
		}
	}
	return nil
}

// GetClusterState returns a snapshot of the cluster topology from the given node.
func (m *clusterManager) GetClusterState(ctx context.Context, addr string) (*ClusterState, error) {
	info, err := m.client.ClusterInfo(ctx, addr)
	if err != nil {
		return nil, err
	}
	nodes, err := m.client.ClusterNodes(ctx, addr)
	if err != nil {
		return nil, err
	}
	return &ClusterState{
		State:         info.State,
		SlotsAssigned: info.SlotsAssigned,
		SlotsOK:       info.SlotsOK,
		SlotsPFail:    info.SlotsPFail,
		SlotsFail:     info.SlotsFail,
		KnownNodes:    info.KnownNodes,
		Size:          info.ClusterSize,
		Nodes:         nodes,
	}, nil
}

// WaitForClusterReady polls until cluster_state is "ok" or the timeout elapses.
func (m *clusterManager) WaitForClusterReady(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		info, err := m.client.ClusterInfo(ctx, addr)
		if err == nil && info.State == "ok" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("cluster at %s did not reach state 'ok' within %s", addr, timeout)
}

// VerifySlotCoverage checks that all 16384 slots are assigned.
func (m *clusterManager) VerifySlotCoverage(ctx context.Context, addr string) (bool, error) {
	info, err := m.client.ClusterInfo(ctx, addr)
	if err != nil {
		return false, err
	}
	return info.SlotsAssigned == totalHashSlots, nil
}

// waitForKnownNodes polls until the cluster reports the expected number of known nodes.
func (m *clusterManager) waitForKnownNodes(ctx context.Context, addr string, expected int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		info, err := m.client.ClusterInfo(ctx, addr)
		if err == nil && info.KnownNodes >= expected {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("cluster at %s did not reach %d known nodes within %s", addr, expected, timeout)
}

// --- helpers ---

func splitAddr(addr string) (string, int, error) {
	idx := strings.LastIndex(addr, ":")
	if idx < 0 {
		return "", 0, fmt.Errorf("invalid address (no port): %s", addr)
	}
	ip := addr[:idx]
	var port int
	if _, err := fmt.Sscanf(addr[idx+1:], "%d", &port); err != nil {
		return "", 0, fmt.Errorf("invalid port in address %s: %w", addr, err)
	}
	return ip, port, nil
}

func isMaster(node ClusterNode) bool {
	for _, f := range node.Flags {
		if f == "master" {
			return true
		}
	}
	return false
}

func masterNodeIDs(nodes []ClusterNode) []string {
	var ids []string
	for _, n := range nodes {
		if isMaster(n) {
			ids = append(ids, n.NodeID)
		}
	}
	return ids
}

func selfNodeID(nodes []ClusterNode) string {
	for _, n := range nodes {
		for _, f := range n.Flags {
			if f == "myself" {
				return n.NodeID
			}
		}
	}
	return ""
}

func nodeIDForAddr(nodes []ClusterNode, addr string) string {
	for _, n := range nodes {
		if n.Addr == addr {
			return n.NodeID
		}
	}
	return ""
}

// collectSlots returns up to `count` slot numbers that belong to the node with the given ID.
func collectSlots(nodes []ClusterNode, nodeID string, count int) []int {
	var slots []int
	for _, n := range nodes {
		if n.NodeID != nodeID {
			continue
		}
		for _, sr := range n.Slots {
			for s := sr.Start; s <= sr.End && len(slots) < count; s++ {
				slots = append(slots, s)
			}
			if len(slots) >= count {
				break
			}
		}
	}
	return slots
}
