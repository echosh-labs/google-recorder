#!/usr/bin/env bash
set -e

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="$DIR/backend/bin/recorder-server"

build_backend_if_needed() {
    if [ ! -f "$BIN" ] || [ "$DIR/backend/cmd/server/main.go" -nt "$BIN" ] || [ "$DIR/backend/pkg/recorder/sync.go" -nt "$BIN" ]; then
        echo "Building Go recorder-server..." >&2
        cd "$DIR/backend"
        go build -o bin/recorder-server ./cmd/server
    fi
}

case "$1" in
    auth)
        build_backend_if_needed
        "$BIN" auth "${@:2}"
        exit 0
        ;;
    mcp)
        build_backend_if_needed
        exec "$BIN" mcp "${@:2}"
        ;;
    sync)
        build_backend_if_needed
        "$BIN" sync "${@:2}"
        exit 0
        ;;
    download)
        build_backend_if_needed
        "$BIN" download "${@:2}"
        exit 0
        ;;
    list)
        build_backend_if_needed
        "$BIN" list "${@:2}"
        exit 0
        ;;
    server)
        build_backend_if_needed
        echo "Starting standalone Google Recorder API & MCP server..."
        exec "$BIN" server "${@:2}"
        ;;
esac

echo "=================================================="
echo "    Google Recorder Studio: Backend & Frontend   "
echo "=================================================="

# Function to clean up background processes on exit
cleanup() {
    echo ""
    echo "Shutting down servers..."
    kill $(jobs -p) 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# 1. Build and start Backend
echo "[1/2] Launching Go API server (REST API & MCP)..."
build_backend_if_needed
"$BIN" server &
BACKEND_PID=$!

# Wait briefly for backend to initialize
sleep 1

# 2. Start Frontend
echo "[2/2] Launching Next.js frontend on http://localhost:3000..."
cd "$DIR/frontend"
npm run dev

wait
