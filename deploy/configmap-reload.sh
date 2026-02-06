#!/bin/sh
# configmap-reload.sh — lightweight sidecar that watches config files
# and sends SIGHUP to the main process when changes are detected.
#
# Designed for Kubernetes ConfigMap mounts where fsnotify is unreliable
# due to the symlink-based update mechanism used by kubelet.
#
# Requires: shareProcessNamespace: true in the pod spec
#
# Environment variables:
#   WATCH_FILES      — comma-separated list of file paths to watch
#   WATCH_INTERVAL   — polling interval in seconds (default: 10)
#   SIGNAL           — signal to send (default: SIGHUP)
#   PROCESS_NAME     — name of the process to signal (default: metrics-governor)

set -e

WATCH_FILES="${WATCH_FILES:-/etc/metrics-governor/limits/limits.yaml}"
WATCH_INTERVAL="${WATCH_INTERVAL:-10}"
SIGNAL="${SIGNAL:-HUP}"
PROCESS_NAME="${PROCESS_NAME:-metrics-governor}"

log() {
    echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') [configmap-reload] $1"
}

# Compute sha256 hash of all watched files concatenated
compute_hash() {
    hash=""
    IFS=','
    for file in $WATCH_FILES; do
        if [ -f "$file" ]; then
            h=$(sha256sum "$file" 2>/dev/null | cut -d' ' -f1)
            hash="${hash}${h}"
        fi
    done
    unset IFS
    echo "$hash"
}

# Find the PID of the main process
find_pid() {
    # With shareProcessNamespace, we can see all processes
    # Look for the target process name
    pid=$(pgrep -x "$PROCESS_NAME" 2>/dev/null | head -1)
    if [ -z "$pid" ]; then
        # Fallback: try to find by /proc scanning (works without pgrep)
        for p in /proc/[0-9]*; do
            if [ -f "$p/cmdline" ]; then
                cmd=$(tr '\0' ' ' < "$p/cmdline" 2>/dev/null)
                case "$cmd" in
                    *"$PROCESS_NAME"*)
                        pid=$(basename "$p")
                        break
                        ;;
                esac
            fi
        done
    fi
    echo "$pid"
}

log "starting config watcher"
log "watching files: $WATCH_FILES"
log "poll interval: ${WATCH_INTERVAL}s"
log "signal: $SIGNAL"
log "target process: $PROCESS_NAME"

# Wait for files to appear (they may not be ready immediately on pod start)
retries=0
while true; do
    current_hash=$(compute_hash)
    if [ -n "$current_hash" ]; then
        break
    fi
    retries=$((retries + 1))
    if [ "$retries" -ge 30 ]; then
        log "ERROR: watched files not found after 30 retries, exiting"
        exit 1
    fi
    log "waiting for watched files to appear (attempt $retries/30)..."
    sleep 2
done

previous_hash="$current_hash"
log "initial config hash: ${previous_hash:0:16}..."

while true; do
    sleep "$WATCH_INTERVAL"

    current_hash=$(compute_hash)

    if [ "$current_hash" != "$previous_hash" ]; then
        log "config change detected (hash: ${current_hash:0:16}...)"

        pid=$(find_pid)
        if [ -n "$pid" ]; then
            log "sending SIG${SIGNAL} to ${PROCESS_NAME} (pid: ${pid})"
            kill -"$SIGNAL" "$pid" 2>/dev/null
            if [ $? -eq 0 ]; then
                log "signal sent successfully"
            else
                log "ERROR: failed to send signal to pid $pid"
            fi
        else
            log "ERROR: process '$PROCESS_NAME' not found"
        fi

        previous_hash="$current_hash"
    fi
done
