package redis

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// SentinelManager provides high-level Sentinel operations built on top of RedisClient.
type SentinelManager interface {
	// GetMaster returns the current master IP and port for the named master set.
	GetMaster(ctx context.Context, sentinelAddr, masterName string) (string, int, error)

	// WaitForMaster polls until Sentinel reports a healthy master or the timeout elapses.
	WaitForMaster(ctx context.Context, sentinelAddr, masterName string, timeout time.Duration) (string, int, error)

	// TriggerFailover requests a Sentinel-initiated failover of the named master.
	TriggerFailover(ctx context.Context, sentinelAddr, masterName string) error

	// ResetMonitor resets Sentinel monitoring state for the named pattern.
	ResetMonitor(ctx context.Context, sentinelAddr, pattern string) error

	// GetMasterInfo returns the full INFO map reported by Sentinel for a master.
	GetMasterInfo(ctx context.Context, sentinelAddr, masterName string) (map[string]string, error)

	// IsReplicaOfMaster checks whether addrToCheck is currently a replica of masterAddr.
	IsReplicaOfMaster(ctx context.Context, addrToCheck, masterAddr string) (bool, error)

	// WaitForSentinelQuorum waits until at least `quorum` Sentinel instances report
	// the master as reachable.
	WaitForSentinelQuorum(ctx context.Context, sentinelAddrs []string, masterName string, quorum int, timeout time.Duration) error
}

type sentinelManager struct {
	client RedisClient
}

// NewSentinelManager creates a SentinelManager backed by the given RedisClient.
func NewSentinelManager(c RedisClient) SentinelManager {
	return &sentinelManager{client: c}
}

func (m *sentinelManager) GetMaster(ctx context.Context, sentinelAddr, masterName string) (string, int, error) {
	return m.client.SentinelGetMasterAddr(ctx, sentinelAddr, masterName)
}

func (m *sentinelManager) WaitForMaster(ctx context.Context, sentinelAddr, masterName string, timeout time.Duration) (string, int, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ip, port, err := m.client.SentinelGetMasterAddr(ctx, sentinelAddr, masterName)
		if err == nil && ip != "" {
			return ip, port, nil
		}
		select {
		case <-ctx.Done():
			return "", 0, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return "", 0, fmt.Errorf("sentinel at %s did not report a master for %q within %s", sentinelAddr, masterName, timeout)
}

func (m *sentinelManager) TriggerFailover(ctx context.Context, sentinelAddr, masterName string) error {
	return m.client.SentinelFailover(ctx, sentinelAddr, masterName)
}

func (m *sentinelManager) ResetMonitor(ctx context.Context, sentinelAddr, pattern string) error {
	return m.client.SentinelReset(ctx, sentinelAddr, pattern)
}

func (m *sentinelManager) GetMasterInfo(ctx context.Context, sentinelAddr, masterName string) (map[string]string, error) {
	return m.client.SentinelMaster(ctx, sentinelAddr, masterName)
}

func (m *sentinelManager) IsReplicaOfMaster(ctx context.Context, addrToCheck, masterAddr string) (bool, error) {
	info, err := m.client.Info(ctx, addrToCheck, "replication")
	if err != nil {
		return false, fmt.Errorf("INFO replication on %s: %w", addrToCheck, err)
	}
	role := info["role"]
	if role != "slave" {
		return false, nil
	}
	masterIP := info["master_host"]
	masterPortStr := info["master_port"]
	masterPort, _ := strconv.Atoi(masterPortStr)
	expected := fmt.Sprintf("%s:%d", masterIP, masterPort)
	return expected == masterAddr, nil
}

func (m *sentinelManager) WaitForSentinelQuorum(ctx context.Context, sentinelAddrs []string, masterName string, quorum int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		healthy := 0
		for _, addr := range sentinelAddrs {
			info, err := m.client.SentinelMaster(ctx, addr, masterName)
			if err != nil {
				continue
			}
			if info["status"] == "ok" || info["flags"] == "master" {
				healthy++
			}
		}
		if healthy >= quorum {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("sentinel quorum of %d not reached within %s", quorum, timeout)
}
