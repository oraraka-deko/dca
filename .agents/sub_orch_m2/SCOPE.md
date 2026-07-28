# Scope: Milestone 2 — King Control Plane Gateway Mode & Decoupled Router (R2)

## Architecture
Milestone 2 establishes King Control Plane Gateway Mode & Decoupled Router functionality in `utils/`:
- `utils/pairing.go`: Single-use pairing code generation/exchange (`dca king add-device <code>`), code validation, and activation token issuance. Single-use enforcement.
- `utils/king_gateway.go`: Protocol Inversion WebSocket server (`/register`), header validation (`X-Node-ID`, `Authorization`), `ActiveConns` (`sync.Map`), and MCP client interactions (`initialize`, `tools/list`).
- `utils/king_ingress.go`: URL route-based ingress at `/<device_id>/mcp`, forwarding JSON-RPC requests down worker WebSocket connections without tool prefixing.
- Transport-agnostic pending request map (`PendingRequests` `sync.Map`) and ID rewriting (unique UUIDs) with a 30-second recovery timeout window decoupling logical requests from physical socket drops.

## Milestones
| # | Name | Scope | Dependencies | Status |
|---|------|-------|-------------|--------|
| 1 | Single-use Pairing Exchange | `utils/pairing.go` | M1 | IN_PROGRESS |
| 2 | Protocol Inversion WS Server | `utils/king_gateway.go` | M2.1 | IN_PROGRESS |
| 3 | URL Route-Based MCP Ingress | `utils/king_ingress.go` | M2.2 | IN_PROGRESS |
| 4 | Transport-Agnostic Pending Map & ID Rewriting | `utils/king_ingress.go` | M2.3 | IN_PROGRESS |

## Interface Contracts
- **Pairing**: `GeneratePairingCode() string`, `ValidatePairingCode(code string) (token string, err error)`
- **Gateway**: HTTP handler on `/register` accepting WebSocket upgrades with headers `X-Node-ID` and `Authorization: Bearer <token>`. Active connections in `ActiveConns` (`sync.Map`).
- **Ingress**: HTTP handler on `/<device_id>/mcp` receiving JSON-RPC requests, rewriting request `id` to UUID, storing in `PendingRequests` (`sync.Map`) with channel and 30s timeout, relaying to worker WSS.

## Code Layout
- `utils/pairing.go`, `utils/pairing_test.go`
- `utils/king_gateway.go`, `utils/king_gateway_test.go`
- `utils/king_ingress.go`, `utils/king_ingress_test.go`
