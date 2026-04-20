package redis

import (
	"context"
	"fmt"
	"time"
)

// WaitForPing retries Ping until it succeeds or the timeout elapses.
func WaitForPing(ctx context.Context, c RedisClient, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := c.Ping(ctx, addr); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("redis at %s did not respond to PING within %s: %w", addr, timeout, lastErr)
}

// WaitForAllPings calls WaitForPing for each address and returns the first error encountered.
func WaitForAllPings(ctx context.Context, c RedisClient, addrs []string, timeout time.Duration) error {
	for _, addr := range addrs {
		if err := WaitForPing(ctx, c, addr, timeout); err != nil {
			return err
		}
	}
	return nil
}

// GetRole returns the replication role of a Redis instance ("master" or "slave").
func GetRole(ctx context.Context, c RedisClient, addr string) (string, error) {
	return c.Role(ctx, addr)
}

// IsHealthy returns true if a Ping succeeds.
func IsHealthy(ctx context.Context, c RedisClient, addr string) bool {
	return c.Ping(ctx, addr) == nil
}

// GetRedisVersion extracts the Redis server version from INFO server output.
func GetRedisVersion(ctx context.Context, c RedisClient, addr string) (string, error) {
	info, err := c.Info(ctx, addr, "server")
	if err != nil {
		return "", fmt.Errorf("INFO server on %s: %w", addr, err)
	}
	version, ok := info["redis_version"]
	if !ok {
		return "", fmt.Errorf("redis_version not found in INFO server output")
	}
	return version, nil
}

// CountConnectedClients returns the number of currently connected clients.
func CountConnectedClients(ctx context.Context, c RedisClient, addr string) (int, error) {
	info, err := c.Info(ctx, addr, "clients")
	if err != nil {
		return 0, err
	}
	var n int
	if v := info["connected_clients"]; v != "" {
		if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
			return 0, nil
		}
	}
	return n, nil
}

// GetReplicationLag returns the replication lag in bytes for a replica node.
func GetReplicationLag(ctx context.Context, c RedisClient, addr string) (int64, error) {
	info, err := c.Info(ctx, addr, "replication")
	if err != nil {
		return 0, err
	}
	var lag int64
	if v := info["master_repl_offset"]; v != "" {
		fmt.Sscanf(v, "%d", &lag)
	}
	return lag, nil
}
