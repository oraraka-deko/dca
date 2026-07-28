# Original User Request

## Initial Request — 2026-07-28T15:17:12Z

Distributed MCP Gateway Architecture (King-Worker Pattern in Go): Enhance the existing `dca` Go codebase to support a reverse-tunneling gateway ("King") and child daemon ("Worker") enabling external AI agents to securely call tools on devices behind NATs/firewalls via WebSockets, following the Protocol Inversion & Session Resumption blueprints.

Working directory: d:\Documents\dca
Integrity mode: development

## Requirements

### R1. Worker Daemon Mode & Reverse Tunnel with Outbox Pattern
The `dca` binary must support a Worker Daemon mode (`dca worker`).
- Generates a short 6-character pairing code when un-paired.
- Initiates an outbound persistent WebSocket connection (`wss://<king>/register`) to the King Gateway with HTTP headers (`X-Node-ID`, `Authorization`).
- **Outbox Pattern for Session Resumption:** Tool executions run in isolated goroutines. Completed JSON-RPC responses are placed into an internal `Outbox` queue. If the WebSocket connection drops during tool execution, the result remains safely queued in the `Outbox` and is automatically flushed to the King upon reconnection.

### R2. King Control Plane Gateway Mode & Decoupled Session Waiter
The `dca` binary must support a King Gateway mode (`dca king`).
- **Single-Use Pairing Exchange:** User pairs a child device using its 6-character code (`dca king add-device <code>`), issuing an encrypted pair token. Once consumed by the child during handshake, the activation key cannot be reused.
- **Protocol Inversion:** King listens for WebSocket connections from Workers (`/register`), stores connections in a thread-safe map (`sync.Map`), and acts as the MCP Client querying `initialize` and `tools/list`.
- **URL Route-Based MCP Ingress (`/<device_id>/mcp`):** Directs incoming agent MCP calls on `/<device_id>/mcp` directly down `<device_id>`'s WebSocket tunnel without renaming or prefixing tool names.
- **Transport-Agnostic Pending Map & ID Rewriting:** Rewrites incoming agent request `id`s to unique UUIDs to prevent ID collisions. Stores pending channels in a `PendingRequests` `sync.Map` with a recovery timeout window (e.g., 30s), decoupling logical agent requests from physical socket reconnections.

### R3. CLI & Config Integration
Integrate CLI commands (`dca king`, `dca worker`, `dca pair`) and extend `ServerConfig` in Go to seamlessly support standalone, king, and worker operational modes while preserving existing service and TUI functionality.

## Acceptance Criteria

### Worker Node & Outbox Queue
- [ ] `dca worker` generates a 6-character code if not paired.
- [ ] Worker establishes a persistent WebSocket connection to King with `X-Node-ID` headers.
- [ ] Asynchronous tool calls buffer results into an `Outbox` queue and flush immediately over WebSocket.
- [ ] If the socket disconnects mid-execution, the result stays in `Outbox` and flushes successfully when reconnected.

### King Control Plane & Decoupled Router
- [ ] `dca king add-device <code>` validates the 6-character pairing code and issues a single-use token.
- [ ] Attempting to reuse an already-consumed activation key fails.
- [ ] King handles WebSocket connections from paired Workers and registers active nodes in a thread-safe `ActiveConns` map.
- [ ] HTTP requests to `/<device_id>/mcp` dynamically route to the target device's WebSocket tunnel.
- [ ] Request IDs are rewritten to UUIDs in `PendingRequests` `sync.Map`, holding agent responses open cleanly during temporary connection drops.

### Verification & Compatibility
- [ ] `go test ./...` passes without errors.
- [ ] Standalone `dca run` and service commands (`status`, `start`, `stop`) continue to work without breaking.
