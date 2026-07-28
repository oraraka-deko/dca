# Handoff Report: Milestone 2 (R2 - Requirements 3 & 4)
## King Control Plane Gateway Mode & Decoupled Router (`utils/king_ingress.go`)

**Agent:** Explorer 3 (Milestone 2)  
**Date:** 2026-07-28  
**Working Directory:** `d:\Documents\dca\.agents\explorer_m2_3`  
**Target Package:** `package utils` (`utils/king_ingress.go`, `utils/king_ingress_test.go`)

---

## 1. Observation

- **Project Infrastructure**: `PROJECT.md` (lines 15, 33–37, 44) specifies Requirement 3 (URL Route-Based MCP Ingress `/<device_id>/mcp`) and Requirement 4 (Transport-Agnostic Pending Map & ID Rewriting in `utils/king_ingress.go`).
- **Starter Info & Standards**: `starter_info_for_agent.md` and `utils/mcp_server.go` (lines 35–46, 1144–1182) demonstrate HTTP server setup and JSON-RPC tool handling patterns.
- **Worker Tunnel & Outbox**: `explorer_m1_1/analysis.md` (lines 95–139, 608–768) documents the Worker Daemon reverse WebSocket tunnel client (`wss://<king>/register`) and session-resumption Outbox queue buffering responses across network drops.
- **Requirements Contract**:
  - `/<device_id>/mcp` ingress receives JSON-RPC calls from agents.
  - Device active connection check against `ActiveConns` (`sync.Map`).
  - Calls forwarded intact down worker tunnel **without tool renaming or prefixing** (`submit_command` stays `submit_command`).
  - Request IDs rewritten to unique UUID strings before tunneling to worker.
  - `PendingRequests` (`sync.Map`) maps rewritten UUIDs to `*PendingRequest` containing response channels.
  - 30-second session recovery window decoupling logical HTTP requests from physical socket drops/reconnects.
  - Demuxing worker responses, restoring original request IDs, and returning HTTP 200 OK.

---

## 2. Logic Chain

1. **Routing Logic**:
   - `ServeHTTP` parses path `strings.Trim(r.URL.Path, "/")` splitting into `parts`. If `len(parts) != 2` or `parts[1] != "mcp"`, returns HTTP 404 Not Found.
   - Extracts `deviceID := parts[0]`. Verified method is `POST` (otherwise returns HTTP 405 Method Not Allowed).
2. **Device Active Connection Verification**:
   - Looks up `deviceID` in `ActiveConns` (`sync.Map`). If absent or nil, immediately returns HTTP 503 Service Unavailable with JSON-RPC error `-32001 Device not connected`.
3. **Transparent Forwarding (No Renaming)**:
   - Request payload is decoded into `JSONRPCRequest`. The method and tool names are passed unchanged without adding prefixes.
4. **ID Rewriting & Pending Map Registration**:
   - `originalID := req.ID` stored in `PendingRequest`.
   - `rewrittenUUID := uuid.New().String()` replaces `req.ID`.
   - `PendingRequests.Store(rewrittenUUID, pending)` registers the request before writing to worker WebSocket.
5. **Transport Decoupling & 30-Second Recovery Timeout**:
   - HTTP handler waits on `select` between `pending.RespChan` and `context.WithTimeout(r.Context(), 30*time.Second)`.
   - If socket drops mid-execution, worker Outbox queue buffers completed response and flushes upon reconnection within 30s.
   - Demuxer calls `PendingRequests.LoadAndDelete(rewrittenUUID)`, restores `resp.ID = pending.OriginalID`, and delivers response to `RespChan`.
   - If 30 seconds elapse, context timeout fires, `PendingRequests.Delete(rewrittenUUID)` cleans up memory, and HTTP 504 Gateway Timeout is returned.

---

## 3. Caveats

- **No Caveats**: The investigation fully covers `utils/king_ingress.go`, `ActiveConns` synchronization, `PendingRequests` UUID rewriting, socket drop recovery, error codes, and unit test strategies.

---

## 4. Conclusion

Requirements 3 & 4 can be implemented cleanly in `utils/king_ingress.go` and verified via `utils/king_ingress_test.go`. The design provides robust, thread-safe request routing, transparent tool invocation without prefixing, UUID request ID rewriting, fast demuxing, and zero memory leaks under network disconnections.

Full implementation details and unit test suites are recorded in `d:\Documents\dca\.agents\explorer_m2_3\analysis.md`.

---

## 5. Verification Method

1. **Inspect Analysis Report**:
   - Read `d:\Documents\dca\.agents\explorer_m2_3\analysis.md` for complete architectural design and code drafts.
2. **Unit Test Verification** (post-implementation):
   - Command: `go test -v ./utils -run TestKingIngress`
   - Invalidation conditions: Any test failure, non-200 HTTP response when worker completes within 30s, missing original ID in final response, or un-deleted items in `PendingRequests` after timeout.
