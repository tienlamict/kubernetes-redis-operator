#!/bin/sh
# Graceful shutdown script for Redis pods (reference copy — the operator inlines this
# logic in the container pre-stop hook so no volume mount is required).
# Handles both standalone/sentinel replicas and sentinel master failover.

set -e

REDIS_PORT="${REDIS_PORT:-6379}"
SENTINEL_PORT="${SENTINEL_PORT:-26379}"
SENTINEL_SVC="${SENTINEL_SVC:-}"
MASTER_NAME="${MASTER_NAME:-mymaster}"

if [ -n "${REDIS_PASSWORD}" ]; then
    AUTH_ARGS="-a ${REDIS_PASSWORD}"
else
    AUTH_ARGS=""
fi

redis_cli() {
    redis-cli -p "${REDIS_PORT}" ${AUTH_ARGS} "$@"
}

echo "Initiating graceful Redis shutdown..."

redis_cli SAVE || echo "SAVE failed, continuing shutdown"

ROLE=$(redis_cli ROLE 2>/dev/null | head -1 || echo "unknown")

if [ "${ROLE}" = "master" ] && [ -n "${SENTINEL_SVC}" ]; then
    echo "This pod is master. Triggering Sentinel failover before shutdown..."
    redis-cli -p "${SENTINEL_PORT}" -h "${SENTINEL_SVC}" SENTINEL FAILOVER "${MASTER_NAME}" || \
        echo "Sentinel failover request failed, continuing shutdown"
    sleep 5
else
    echo "Role: ${ROLE}. No failover needed."
fi

echo "Graceful shutdown complete."
