# Analysis and Design Report: WorkerDaemon WebSocket Client & Connection Lifecycle

**Agent**: Explorer 3 (Milestone 1)  
**Date**: 2026-07-28  
**Target Package**: `utils` (`utils/worker_daemon.go`, `utils/outbox.go`)  

---

## 1. Executive Summary

This report provides a comprehensive analysis and design specification for the `WorkerDaemon` WebSocket reverse tunnel client in Milestone 1 of the `dca` distributed MCP gateway architecture.

The `WorkerDaemon` operates on child devices behind NATs/firewalls. It establishes an outbound persistent reverse WebSocket connection to the central `King` control plane (`wss://<king>/register`). When incoming JSON-RPC requests (e.g. `tools/call`) arrive over the WebSocket tunnel, the worker dispatches tool executions to isolated goroutines. Completed tool responses are enqueued into a thread-safe `Outbox` queue and flushed across the WebSocket connection. If the connection drops at any point, items remain safely buffered in `Outbox` and are automatically flushed upon reconnection via an exponential backoff loop.

---

## 2. Dependency Analysis (`go.mod`)

A scan of `d:\Documents\dca\go.mod` reveals:
- Line 59: `github.com/gorilla/websocket v1.5.3` is **already installed** as a direct dependency.
- `nhooyr/websocket` (or `coder/websocket`) is **not present** in `go.mod`.
- Go standard library (`net/http`) does not provide built-in WebSocket client capabilities (`golang.org/x/net/websocket` is legacy and deprecated).
- `github.com/gorilla/websocket` is already successfully used by the test harness in `tests/e2e/harness.go` (`MockKing` and `MockWorker`).

### Recommendation
Use `github.com/gorilla/websocket` for the `WorkerDaemon` implementation. It aligns with existing project dependencies, requires no new `go.mod` modifications, and provides complete support for custom headers, ping/pong handlers, read deadlines, and control frames.

---

## 3. `WorkerDaemon` Component Design & Lifecycle

### 3.1 State Machine
The worker daemon transitions through 4 distinct states:
1. **`StateDisconnected`**: Initial state, or state entered immediately after socket drop.
2. **`StateConnecting`**: Attempting WSS handshake to `wss://<king>/register`.
3. **`StateConnected`**: Handshake successful, read loop active, ping ticker active, outbox flusher active.
4. **`StateStopped`**: Worker daemon shut down via context cancellation or `Stop()`.

```
[StateDisconnected] ---> (Backoff Timer) ---> [StateConnecting]
       ^                                            |
       | (Socket Error / Drop)                      | (Handshake Success)
       |                                            v
[StateConnected] <----------------------------------+
       |
       v (Shutdown / Cancel)
[StateStopped]
```

---

## 4. Connection Lifecycle Specifications

### 4.1 Initial Handshake & Headers
- **Target Endpoint**: `wss://<king_host>:<port>/register` (or `ws://` fallback for local dev/testing).
- **HTTP Upgrade Headers**:
  - `X-Node-ID`: Unique string identifying the worker node (e.g., node UUID or configured ID).
  - `Authorization`: `Bearer <pair_token>` (single-use or persistent token issued during pairing).
- **TLS Configuration**:
  - Custom `crypto/tls.Config` supporting self-signed certificates (`InsecureSkipVerify` option when configured or in dev mode).

```go
headers := http.Header{}
headers.Set("X-Node-ID", w.nodeID)
headers.Set("Authorization", "Bearer "+w.pairToken)

dialer := websocket.Dialer{
    TLSClientConfig: w.tlsConfig,
    HandshakeTimeout: 10 * time.Second,
}

conn, resp, err := dialer.DialContext(ctx, kingWSSURL, headers)
```

---

### 4.2 Keep-Alive Mechanism (Ping/Pong)
To detect broken TCP sockets behind NATs/firewalls early, the `WorkerDaemon` implements a bidirectional keep-alive protocol:
- **Ping Period (`pingPeriod`)**: 20 seconds (interval at which worker sends `PingMessage`).
- **Pong Wait (`pongWait`)**: 60 seconds (maximum duration worker waits for a Pong response from King).
- **Write Deadline (`writeWait`)**: 10 seconds (deadline for writing frames).

#### Implementation Protocol:
1. Upon connecting:
   ```go
   conn.SetReadDeadline(time.Now().Add(pongWait))
   conn.SetPongHandler(func(string) error {
       conn.SetReadDeadline(time.Now().Add(pongWait))
       return nil
   })
   ```
2. Ping Ticker Goroutine:
   ```go
   ticker := time.NewTicker(pingPeriod)
   defer ticker.Stop()
   for {
       select {
       case <-ticker.C:
           conn.SetWriteDeadline(time.Now().Add(writeWait))
           if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(writeWait)); err != nil {
               return // Trigger reconnection
           }
       case <-stopChan:
           return
       }
   }
   ```

---

### 4.3 Exponential Backoff Reconnect Loop
When the WebSocket connection fails or drops, the worker daemon must not overwhelm the King server with connection attempts. It uses an exponential backoff algorithm with full jitter:

- **Initial Interval**: 1.0 second
- **Max Interval**: 30.0 seconds
- **Multiplier**: 2.0
- **Jitter Factor**: ±20% random variation

```go
func (w *WorkerDaemon) connectLoop(ctx context.Context) {
    backoff := 1 * time.Second
    maxBackoff := 30 * time.Second

    for {
        select {
        case <-ctx.Done():
            return
        default:
        }

        err := w.connectAndServe(ctx)
        if err != nil {
            w.setState(StateDisconnected)
            
            // Calculate next backoff duration with jitter
            jitter := time.Duration(rand.Float64() * 0.4 * float64(backoff)) - (backoff / 5)
            sleepDuration := backoff + jitter
            
            select {
            case <-ctx.Done():
                return
            case <-time.After(sleepDuration):
            }

            backoff = time.Duration(float64(backoff) * 2.0)
            if backoff > maxBackoff {
                backoff = maxBackoff
            }
        } else {
            // Reset backoff on clean disconnection or successful long session
            backoff = 1 * time.Second
        }
    }
}
```

---

### 4.4 Outbox Automatic Flushing Strategy
The `Outbox` queue stores JSON-RPC responses when execution completes.

#### Automatic Flush Trigger Sequence:
1. **Handshake Completion**: Immediately after `dialer.DialContext` succeeds and `StateConnected` is set, `WorkerDaemon` triggers an initial `FlushOutbox()`.
2. **Real-time Enqueue Trigger**: Whenever an async tool execution finishes while connected, `FlushOutbox()` or direct write is executed. If direct write fails, item is enqueued into `Outbox`.
3. **Atomic Pop & Retry**: `Outbox.PopAll()` drains the queue. Items are sent over WSS. If `WriteMessage` fails mid-stream, un-sent items are pushed back to the head of `Outbox` for the next reconnect cycle.

```go
func (w *WorkerDaemon) FlushOutbox() error {
    w.connMu.Lock()
    defer w.connMu.Unlock()

    if w.conn == nil || w.state != StateConnected {
        return errors.New("cannot flush: worker disconnected")
    }

    items := w.outbox.PopAll()
    if len(items) == 0 {
        return nil
    }

    for i, item := range items {
        data, err := json.Marshal(item)
        if err != nil {
            continue
        }

        w.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
        if err := w.conn.WriteMessage(websocket.TextMessage, data); err != nil {
            // Re-enqueue remaining un-sent items
            w.outbox.Prepend(items[i:])
            return fmt.Errorf("outbox write error: %w", err)
        }
    }
    return nil
}
```

---

## 5. Summary of Recommended Code Architecture

For implementation in `utils/worker_daemon.go`:
- `WorkerDaemon`: Core struct holding `nodeID`, `pairToken`, `kingURL`, `outbox`, `wsConn`, state mutex, tool handlers map, `MCPServerWrapper` reference.
- `NewWorkerDaemon(nodeID, kingURL, pairToken string, mcpWrapper *MCPServerWrapper) *WorkerDaemon`
- Methods: `Start(ctx context.Context)`, `Stop()`, `FlushOutbox()`, `handleRequest(req *JSONRPCRequest)`.

---
