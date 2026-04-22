package controller

import (
	"context"
	"time"

	"github.com/example/redis-operator/internal/redis"
)

// noopRedisClient is a test double for redis.RedisClient that records calls to
// specific methods used in controller tests.  All other methods return nil / zero.
type noopRedisClient struct {
	// SentinelGetMasterAddr results — keyed by masterName.
	masterIP   string
	masterPort int

	// Calls recorded during test.
	sentinelResetCalls    []string // addresses SENTINEL RESET was called on
	clusterFailoverCalls  []string // addresses ClusterFailover was called on
}

func (c *noopRedisClient) Ping(_ context.Context, _ string) error                           { return nil }
func (c *noopRedisClient) Info(_ context.Context, _, _ string) (map[string]string, error)   { return nil, nil }
func (c *noopRedisClient) ConfigSet(_ context.Context, _, _, _ string) error                { return nil }
func (c *noopRedisClient) ConfigGet(_ context.Context, _, _ string) (string, error)         { return "", nil }
func (c *noopRedisClient) Save(_ context.Context, _ string) error                           { return nil }
func (c *noopRedisClient) ClusterInfo(_ context.Context, _ string) (*redis.ClusterInfo, error) {
	return &redis.ClusterInfo{State: "ok", SlotsAssigned: 16384}, nil
}
func (c *noopRedisClient) ClusterNodes(_ context.Context, _ string) ([]redis.ClusterNode, error) {
	return nil, nil
}
func (c *noopRedisClient) ClusterMeet(_ context.Context, _, _ string, _ int) error               { return nil }
func (c *noopRedisClient) ClusterAddSlots(_ context.Context, _ string, _ ...int) error           { return nil }
func (c *noopRedisClient) ClusterDelSlots(_ context.Context, _ string, _ ...int) error           { return nil }
func (c *noopRedisClient) ClusterReplicate(_ context.Context, _, _ string) error                 { return nil }
func (c *noopRedisClient) ClusterFailover(_ context.Context, addr string) error {
	c.clusterFailoverCalls = append(c.clusterFailoverCalls, addr)
	return nil
}
func (c *noopRedisClient) ClusterForget(_ context.Context, _, _ string) error { return nil }
func (c *noopRedisClient) ClusterReset(_ context.Context, _ string, _ bool) error { return nil }
func (c *noopRedisClient) ClusterSetSlot(_ context.Context, _ string, _ int, _, _ string) error {
	return nil
}
func (c *noopRedisClient) ClusterGetKeysInSlot(_ context.Context, _ string, _, _ int) ([]string, error) {
	return nil, nil
}
func (c *noopRedisClient) ClusterCountKeysInSlot(_ context.Context, _ string, _ int) (int64, error) {
	return 0, nil
}
func (c *noopRedisClient) Migrate(_ context.Context, _, _ string, _ int, _ string, _, _ int) error {
	return nil
}
func (c *noopRedisClient) SentinelMaster(_ context.Context, _, _ string) (map[string]string, error) {
	return nil, nil
}
func (c *noopRedisClient) SentinelGetMasterAddr(_ context.Context, _, _ string) (string, int, error) {
	return c.masterIP, c.masterPort, nil
}
func (c *noopRedisClient) SentinelFailover(_ context.Context, _, _ string) error { return nil }
func (c *noopRedisClient) SentinelReset(_ context.Context, addr, _ string) error {
	c.sentinelResetCalls = append(c.sentinelResetCalls, addr)
	return nil
}
func (c *noopRedisClient) ReplicaOf(_ context.Context, _ string, _ string, _ int) error { return nil }
func (c *noopRedisClient) Role(_ context.Context, _ string) (string, error)              { return "master", nil }

// noopClusterManager is a test double for redis.ClusterManager.
type noopClusterManager struct {
	state *redis.ClusterState
}

func (m *noopClusterManager) FormCluster(_ context.Context, _, _ []string, _ int) error { return nil }
func (m *noopClusterManager) AssignSlotsEvenly(_ context.Context, _ []string) error     { return nil }
func (m *noopClusterManager) MigrateSlot(_ context.Context, _, _ string, _ int) error  { return nil }
func (m *noopClusterManager) MigrateSlotsRange(_ context.Context, _, _ string, _, _ int) error {
	return nil
}
func (m *noopClusterManager) RebalanceSlots(_ context.Context, _ []string) error { return nil }
func (m *noopClusterManager) AddMaster(_ context.Context, _, _ string) error     { return nil }
func (m *noopClusterManager) RemoveMaster(_ context.Context, _, _ string) error  { return nil }
func (m *noopClusterManager) AddReplica(_ context.Context, _, _ string) error    { return nil }
func (m *noopClusterManager) RemoveReplica(_ context.Context, _, _ string) error { return nil }
func (m *noopClusterManager) GetClusterState(_ context.Context, _ string) (*redis.ClusterState, error) {
	if m.state != nil {
		return m.state, nil
	}
	return &redis.ClusterState{State: "ok", SlotsAssigned: 16384, Size: 3}, nil
}
func (m *noopClusterManager) WaitForClusterReady(_ context.Context, _ string, _ time.Duration) error {
	return nil
}
func (m *noopClusterManager) VerifySlotCoverage(_ context.Context, _ string) (bool, error) {
	return true, nil
}
