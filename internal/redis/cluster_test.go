package redis

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"
)

// recordingRedisClient is a minimal test double that records CLUSTER SETSLOT calls
// and optionally fails ClusterGetKeysInSlot to simulate a migration error.
type recordingRedisClient struct {
	noopClient
	setSlotCalls        []setSlotCall
	getKeysInSlotErr    error
}

type setSlotCall struct {
	addr       string
	slot       int
	subcommand string
	nodeID     string
}

func (r *recordingRedisClient) ClusterSetSlot(_ context.Context, addr string, slot int, subcommand, nodeID string) error {
	r.setSlotCalls = append(r.setSlotCalls, setSlotCall{addr, slot, subcommand, nodeID})
	return nil
}

func (r *recordingRedisClient) ClusterGetKeysInSlot(_ context.Context, _ string, _, _ int) ([]string, error) {
	if r.getKeysInSlotErr != nil {
		return nil, r.getKeysInSlotErr
	}
	return nil, nil // no keys → migration complete
}

func (r *recordingRedisClient) ClusterNodes(_ context.Context, addr string) ([]ClusterNode, error) {
	// Return two nodes: one for source (myself), one for dest.
	if addr == "10.0.0.1:6379" {
		return []ClusterNode{
			{NodeID: "src-id", Addr: "10.0.0.1:6379", Flags: []string{"myself", "master"}},
			{NodeID: "dst-id", Addr: "10.0.0.2:6379", Flags: []string{"master"}},
		}, nil
	}
	return []ClusterNode{
		{NodeID: "dst-id", Addr: "10.0.0.2:6379", Flags: []string{"myself", "master"}},
		{NodeID: "src-id", Addr: "10.0.0.1:6379", Flags: []string{"master"}},
	}, nil
}

// noopClient provides zero-value implementations for all RedisClient methods so
// recordingRedisClient only needs to override the methods under test.
type noopClient struct{}

func (noopClient) Ping(_ context.Context, _ string) error                            { return nil }
func (noopClient) Info(_ context.Context, _, _ string) (map[string]string, error)    { return nil, nil }
func (noopClient) ConfigSet(_ context.Context, _, _, _ string) error                 { return nil }
func (noopClient) ConfigGet(_ context.Context, _, _ string) (string, error)          { return "", nil }
func (noopClient) Save(_ context.Context, _ string) error                            { return nil }
func (noopClient) ClusterInfo(_ context.Context, _ string) (*ClusterInfo, error)     { return &ClusterInfo{}, nil }
func (noopClient) ClusterNodes(_ context.Context, _ string) ([]ClusterNode, error)   { return nil, nil }
func (noopClient) ClusterMeet(_ context.Context, _, _ string, _ int) error           { return nil }
func (noopClient) ClusterAddSlots(_ context.Context, _ string, _ ...int) error       { return nil }
func (noopClient) ClusterDelSlots(_ context.Context, _ string, _ ...int) error       { return nil }
func (noopClient) ClusterReplicate(_ context.Context, _, _ string) error             { return nil }
func (noopClient) ClusterFailover(_ context.Context, _ string) error                 { return nil }
func (noopClient) ClusterForget(_ context.Context, _, _ string) error                { return nil }
func (noopClient) ClusterReset(_ context.Context, _ string, _ bool) error            { return nil }
func (noopClient) ClusterSetSlot(_ context.Context, _ string, _ int, _, _ string) error { return nil }
func (noopClient) ClusterGetKeysInSlot(_ context.Context, _ string, _, _ int) ([]string, error) {
	return nil, nil
}
func (noopClient) ClusterCountKeysInSlot(_ context.Context, _ string, _ int) (int64, error) {
	return 0, nil
}
func (noopClient) Migrate(_ context.Context, _, _ string, _ int, _ string, _, _ int) error {
	return nil
}
func (noopClient) SentinelMaster(_ context.Context, _, _ string) (map[string]string, error) {
	return nil, nil
}
func (noopClient) SentinelGetMasterAddr(_ context.Context, _, _ string) (string, int, error) {
	return "", 0, nil
}
func (noopClient) SentinelFailover(_ context.Context, _, _ string) error             { return nil }
func (noopClient) SentinelReset(_ context.Context, _, _ string) error                { return nil }
func (noopClient) ReplicaOf(_ context.Context, _ string, _ string, _ int) error      { return nil }
func (noopClient) Role(_ context.Context, _ string) (string, error)                  { return "", nil }

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestMigrateSlot_HappyPath verifies that on a successful migration the slot is
// committed with CLUSTER SETSLOT NODE on all known cluster members.
func TestMigrateSlot_HappyPath(t *testing.T) {
	g := NewWithT(t)
	rc := &recordingRedisClient{}
	mgr := NewClusterManager(rc).(*clusterManager)

	err := mgr.MigrateSlot(context.Background(), "10.0.0.1:6379", "10.0.0.2:6379", 42)
	g.Expect(err).NotTo(HaveOccurred())

	subcommands := make([]string, 0, len(rc.setSlotCalls))
	for _, c := range rc.setSlotCalls {
		subcommands = append(subcommands, c.subcommand)
	}
	g.Expect(subcommands).To(ContainElement("IMPORTING"))
	g.Expect(subcommands).To(ContainElement("MIGRATING"))
	g.Expect(subcommands).To(ContainElement("NODE"))
	g.Expect(subcommands).NotTo(ContainElement("STABLE"),
		"STABLE must not be issued on a successful migration")
}

// TestMigrateSlot_SetsSlotStableOnKeyMigrationFailure verifies that when key
// migration fails (CLUSTER GETKEYSINSLOT returns an error), the operator issues
// CLUSTER SETSLOT STABLE on both the source and destination nodes so the cluster
// does not remain stuck in a partial-migration state (spec section 11.4).
func TestMigrateSlot_SetsSlotStableOnKeyMigrationFailure(t *testing.T) {
	g := NewWithT(t)
	rc := &recordingRedisClient{
		getKeysInSlotErr: errors.New("simulated key migration failure"),
	}
	mgr := NewClusterManager(rc).(*clusterManager)

	err := mgr.MigrateSlot(context.Background(), "10.0.0.1:6379", "10.0.0.2:6379", 42)
	g.Expect(err).To(HaveOccurred(), "MigrateSlot should propagate the migration error")

	stableCalls := 0
	for _, c := range rc.setSlotCalls {
		if c.subcommand == "STABLE" {
			stableCalls++
		}
	}
	g.Expect(stableCalls).To(Equal(2),
		"CLUSTER SETSLOT STABLE must be issued on both source and destination on failure")
}
