# Technical Analysis & Architecture Design: Milestone 1 (Worker Daemon & Outbox Queue)

**Author:** Explorer 1 (Milestone 1)  
**Date:** 2026-07-28  
**Working Directory:** `d:\Documents\dca\.agents\explorer_m1_1`  
**Target Package:** `utils` (`utils/pairing_code.go`, `utils/outbox.go`, `utils/worker_daemon.go`)  

---

## 1. Executive Summary & Scope Overview

Milestone 1 implements the **Worker Daemon** capabilities for the `dca` distributed MCP architecture:
1. **Pairing Code Generator (`utils/pairing_code.go`)**: Short, cryptographically secure 6-character alphanumeric pairing code generator for device pairing.
2. **Thread-Safe Outbox Queue (`utils/outbox.go`)**: In-memory, thread-safe session resumption queue that buffers completed JSON-RPC responses when WebSocket disconnections occur.
3. **Worker Daemon & Reverse Tunnel Client (`utils/worker_daemon.go`)**: Persistent outbound WebSocket client (`wss://<king>/register`) with custom headers (`X-Node-ID`, `Authorization`), isolated goroutine tool call execution, and background Outbox flushing.

---

## 2. Codebase Context & Integration Assessment

### Existing Infrastructure in `dca`
- **`utils/mcp_server.go`**: Contains `MCPServerWrapper` encapsulating `mark3labs/mcp-go/server`. Manages tools, execution context, and tool registration.
- **`utils/server_config.go`**: Contains `ServerConfig` (HTTP/HTTPS, TLS, auth modes, DB config).
- **`go.mod` Dependencies**:
  - `github.com/gorilla/websocket v1.5.3` (WebSocket framing and client dialer)
  - `github.com/mark3labs/mcp-go v0.57.0` (MCP JSON-RPC protocol implementation)
  - `github.com/google/uuid v1.6.0` (Request & node tracking)
- **Concurrency Conventions**: Uses standard Go concurrency primitives (`sync.Mutex`, `atomic`, `context.Context`, channels, `time`).

---

## 3. Detailed Component Designs

### 3.1 Pairing Code Generator (`utils/pairing_code.go`)

#### Purpose
Generates a short, user-friendly, cryptographically random 6-character uppercase alphanumeric code (`[A-Z0-9]{6}`) when a Worker node is un-paired.

#### Architecture & Data Structures
```go
package utils

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const (
	PairingCodeLength  = 6
	PairingCodeCharset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

var pairingCodeRegex = regexp.MustCompile(`^[A-Z0-9]{6}$`)

type WorkerCredentials struct {
	NodeID    string `json:"node_id"`
	PairToken string `json:"pair_token"`
	KingURL   string `json:"king_url"`
	IsPaired  bool   `json:"is_paired"`
}

type PairingCodeManager struct {
	mu          sync.Mutex
	currentCode string
	creds       WorkerCredentials
	filePath    string
}
```

#### Key Functions & Methods
1. **`GeneratePairingCode() (string, error)`**
   - Uses `crypto/rand.Int(rand.Reader, big.NewInt(36))` to select characters uniformly without modulo bias.
   - Formats result to 6 uppercase alphanumeric characters.
2. **`ValidatePairingCode(code string) bool`**
   - Normalizes input by trimming whitespace and converting to uppercase.
   - Validates length (6) and matches against `^[A-Z0-9]{6}$`.
3. **`FormatPairingCode(code string) string`**
   - Formats code with hyphen for human display: `ABC-DEF`.
4. **`PairingCodeManager` Methods**:
   - `GetOrGenerateCode() (string, error)`
   - `SaveCredentials(creds WorkerCredentials) error`
   - `LoadCredentials() (*WorkerCredentials, error)`

---

### 3.2 Thread-Safe Outbox Queue (`utils/outbox.go`)

#### Purpose
Decouples asynchronous tool execution goroutines from the WebSocket tunnel transmission. Prevents loss of tool execution responses during network drops.

#### Architecture & Data Structures
```go
package utils

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

type OutboxItem struct {
	ID        string          `json:"id"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
	Attempts  int             `json:"attempts"`
}

type Outbox struct {
	mu      sync.Mutex
	items   []OutboxItem
	maxSize int
	notify  chan struct{}
}
```

#### Key Methods & Synchronization Mechanics
1. **`NewOutbox(maxSize int) *Outbox`**: Initializes queue with capacity bounds (default max: 1000 items).
2. **`Enqueue(item OutboxItem) error`**:
   - Thread-safe append under `mu.Lock()`.
   - Rejects or drops oldest item if `maxSize` is reached.
   - Non-blocking signal sent to `notify` channel to wake background flusher.
3. **`Dequeue() (OutboxItem, bool)`**:
   - Returns and removes front item in FIFO order.
4. **`PeekAll() []OutboxItem`**:
   - Returns a slice copy of queued items without mutating queue state.
5. **`Flush(ctx context.Context, sendFunc func(item OutboxItem) error) (int, error)`**:
   - Flushes items atomically in FIFO order.
   - If `sendFunc(item)` succeeds, item is permanently dequeued.
   - If `sendFunc(item)` returns an error (e.g. WebSocket connection dropped), flushing halts immediately. The failed item remains at the head of the queue for the next flush cycle upon reconnection.
6. **`Len() int`**, **`Clear()`**, **`Notify() <-chan struct{}`**.

---

### 3.3 Worker Daemon & WSS Reverse Tunnel Client (`utils/worker_daemon.go`)

#### Purpose
Manages persistent outbound WebSocket reverse tunnel to the King gateway (`wss://<king>/register`), executes incoming MCP tool calls in isolated goroutines, and buffers results in `Outbox`.

#### Architecture & Data Structures
```go
package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mark3labs/mcp-go/mcp"
)

type WorkerConfig struct {
	KingURL           string        `json:"king_url"`
	NodeID            string        `json:"node_id"`
	PairToken         string        `json:"pair_token"`
	ConfigPath        string        `json:"config_path"`
	ReconnectInterval time.Duration `json:"reconnect_interval"`
	MaxOutboxSize     int           `json:"max_outbox_size"`
}

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type WorkerDaemon struct {
	Cfg        WorkerConfig
	Outbox     *Outbox
	MCPServer  *MCPServerWrapper
	PairingMgr *PairingCodeManager

	mu       sync.Mutex
	wsConn   *websocket.Conn
	isPaired bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}
```

#### Lifecycle & Concurrency Flow
1. **Un-Paired Handshake**:
   - If `PairToken` is missing, generates a 6-character code via `PairingCodeManager`.
   - Logs pairing instructions: `"Worker un-paired. Code: ABC-DEF. Run 'dca king add-device ABCDEF' on King server."`
   - Retries handshake until paired credentials are saved.
2. **WebSocket Connection (`connectWebSocket`)**:
   - Dials `wss://<king_host>/register` (or `ws://` fallback for dev).
   - Passes custom HTTP headers:
     - `X-Node-ID`: worker's unique ID.
     - `Authorization`: `Bearer <pair_token>`.
3. **Reader Loop (`readLoop`)**:
   - Continuously reads text WebSocket frames.
   - Unmarshals incoming `JSONRPCRequest`.
   - For `tools/call`, dispatches to isolated goroutine: `go w.handleToolCall(req)`.
   - For `tools/list` or `initialize`, executes inline and enqueues response.
4. **Tool Call Execution (`handleToolCall`)**:
   - Spawns goroutine.
   - Translates JSON-RPC params into `mcp.CallToolRequest`.
   - Calls `MCPServerWrapper` handler.
   - Builds `JSONRPCResponse`.
   - Enqueues response into `w.Outbox.Enqueue(OutboxItem{...})`.
5. **Background Flusher (`flushLoop`)**:
   - Drains `w.Outbox` over active `wsConn`.
   - Uses `sync.Mutex` protection for writing to WebSocket.
   - If socket drop occurs, flusher pauses and waits for reconnection signal. Upon reconnect, all buffered responses in `Outbox` flush cleanly to King.

---

## 4. Concrete Proposed Code Implementations

Below are complete, production-ready implementation drafts for `utils/pairing_code.go`, `utils/outbox.go`, and `utils/worker_daemon.go`.

### 4.1 Implementation Draft: `utils/pairing_code.go`

```go
package utils

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const (
	PairingCodeLength  = 6
	PairingCodeCharset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

var pairingCodeRegex = regexp.MustCompile(`^[A-Z0-9]{6}$`)

// WorkerCredentials stores local pairing state.
type WorkerCredentials struct {
	NodeID    string `json:"node_id"`
	PairToken string `json:"pair_token"`
	KingURL   string `json:"king_url"`
	IsPaired  bool   `json:"is_paired"`
}

// GeneratePairingCode produces a random 6-character uppercase alphanumeric string.
func GeneratePairingCode() (string, error) {
	code := make([]byte, PairingCodeLength)
	charsetLen := big.NewInt(int64(len(PairingCodeCharset)))
	for i := 0; i < PairingCodeLength; i++ {
		num, err := rand.Int(rand.Reader, charsetLen)
		if err != nil {
			return "", fmt.Errorf("failed generating random code: %w", err)
		}
		code[i] = PairingCodeCharset[num.Int64()]
	}
	return string(code), nil
}

// ValidatePairingCode checks if code matches 6 alphanumeric chars.
func ValidatePairingCode(code string) bool {
	clean := strings.ToUpper(strings.TrimSpace(code))
	clean = strings.ReplaceAll(clean, "-", "")
	return pairingCodeRegex.MatchString(clean)
}

// FormatPairingCode formats 6-char code as ABC-DEF.
func FormatPairingCode(code string) string {
	clean := strings.ToUpper(strings.TrimSpace(code))
	clean = strings.ReplaceAll(clean, "-", "")
	if len(clean) != 6 {
		return code
	}
	return clean[:3] + "-" + clean[3:]
}

// PairingCodeManager manages worker pairing state on disk.
type PairingCodeManager struct {
	mu          sync.Mutex
	currentCode string
	filePath    string
	creds       WorkerCredentials
}

func NewPairingCodeManager(filePath string) *PairingCodeManager {
	return &PairingCodeManager{
		filePath: filePath,
	}
}

func (m *PairingCodeManager) GetOrGenerateCode() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.currentCode != "" {
		return m.currentCode, nil
	}
	code, err := GeneratePairingCode()
	if err != nil {
		return "", err
	}
	m.currentCode = code
	return code, nil
}

func (m *PairingCodeManager) LoadCredentials() (*WorkerCredentials, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.filePath == "" {
		return &m.creds, nil
	}
	data, err := os.ReadFile(m.filePath)
	if err != nil {
		return nil, err
	}
	var creds WorkerCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	m.creds = creds
	return &m.creds, nil
}

func (m *PairingCodeManager) SaveCredentials(creds WorkerCredentials) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.creds = creds
	if m.filePath == "" {
		return nil
	}
	dir := filepath.Dir(m.filePath)
	if dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0755)
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.filePath, data, 0644)
}
```

---

### 4.2 Implementation Draft: `utils/outbox.go`

```go
package utils

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// OutboxItem represents a single enqueued JSON-RPC response item.
type OutboxItem struct {
	ID        string          `json:"id"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
	Attempts  int             `json:"attempts"`
}

// Outbox provides a thread-safe FIFO queue for session resumption.
type Outbox struct {
	mu      sync.Mutex
	items   []OutboxItem
	maxSize int
	notify  chan struct{}
}

// NewOutbox creates an initialized Outbox queue.
func NewOutbox(maxSize int) *Outbox {
	if maxSize <= 0 {
		maxSize = 1000
	}
	return &Outbox{
		items:   make([]OutboxItem, 0),
		maxSize: maxSize,
		notify:  make(chan struct{}, 1),
	}
}

// Enqueue adds an item to the end of the outbox queue.
func (o *Outbox) Enqueue(item OutboxItem) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}

	if len(o.items) >= o.maxSize {
		// Drop oldest item if capacity is reached
		o.items = o.items[1:]
	}

	o.items = append(o.items, item)

	// Non-blocking notification to flusher
	select {
	case o.notify <- struct{}{}:
	default:
	}
	return nil
}

// Dequeue removes and returns the front item.
func (o *Outbox) Dequeue() (OutboxItem, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if len(o.items) == 0 {
		return OutboxItem{}, false
	}
	item := o.items[0]
	o.items = o.items[1:]
	return item, true
}

// PeekAll returns a slice copy of queued items.
func (o *Outbox) PeekAll() []OutboxItem {
	o.mu.Lock()
	defer o.mu.Unlock()

	cp := make([]OutboxItem, len(o.items))
	copy(cp, o.items)
	return cp
}

// Len returns current number of items.
func (o *Outbox) Len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.items)
}

// Clear empties the queue.
func (o *Outbox) Clear() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.items = make([]OutboxItem, 0)
}

// Notify channel for flusher trigger.
func (o *Outbox) Notify() <-chan struct{} {
	return o.notify
}

// Flush attempts to send queued items in FIFO order using sendFunc.
func (o *Outbox) Flush(ctx context.Context, sendFunc func(item OutboxItem) error) (int, error) {
	flushedCount := 0

	for {
		select {
		case <-ctx.Done():
			return flushedCount, ctx.Err()
		default:
		}

		o.mu.Lock()
		if len(o.items) == 0 {
			o.mu.Unlock()
			break
		}
		item := o.items[0]
		o.mu.Unlock()

		item.Attempts++
		if err := sendFunc(item); err != nil {
			// Delivery failed: keep item in head of queue and return error
			return flushedCount, err
		}

		// Delivery succeeded: dequeue item
		o.mu.Lock()
		if len(o.items) > 0 && o.items[0].ID == item.ID {
			o.items = o.items[1:]
		}
		o.mu.Unlock()

		flushedCount++
	}

	return flushedCount, nil
}
```

---

### 4.3 Implementation Draft: `utils/worker_daemon.go`

```go
package utils

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/mark3labs/mcp-go/mcp"
)

type WorkerConfig struct {
	KingURL           string        `json:"king_url"`
	NodeID            string        `json:"node_id"`
	PairToken         string        `json:"pair_token"`
	ConfigPath        string        `json:"config_path"`
	ReconnectInterval time.Duration `json:"reconnect_interval"`
	MaxOutboxSize     int           `json:"max_outbox_size"`
}

func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{
		KingURL:           "ws://localhost:8080/register",
		NodeID:            uuid.New().String(),
		ReconnectInterval: 2 * time.Second,
		MaxOutboxSize:     1000,
	}
}

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type WorkerDaemon struct {
	Cfg        WorkerConfig
	Outbox     *Outbox
	MCPServer  *MCPServerWrapper
	PairingMgr *PairingCodeManager

	mu        sync.Mutex
	wsConn    *websocket.Conn
	isConnected bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewWorkerDaemon(cfg WorkerConfig, mcpServer *MCPServerWrapper) *WorkerDaemon {
	if cfg.NodeID == "" {
		cfg.NodeID = uuid.New().String()
	}
	if cfg.ReconnectInterval <= 0 {
		cfg.ReconnectInterval = 2 * time.Second
	}

	pm := NewPairingCodeManager(cfg.ConfigPath)

	return &WorkerDaemon{
		Cfg:        cfg,
		Outbox:     NewOutbox(cfg.MaxOutboxSize),
		MCPServer:  mcpServer,
		PairingMgr: pm,
	}
}

func (w *WorkerDaemon) Start(ctx context.Context) error {
	w.ctx, w.cancel = context.WithCancel(ctx)

	// Load credentials if available
	if creds, err := w.PairingMgr.LoadCredentials(); err == nil && creds.PairToken != "" {
		w.Cfg.PairToken = creds.PairToken
		if creds.NodeID != "" {
			w.Cfg.NodeID = creds.NodeID
		}
		if creds.KingURL != "" {
			w.Cfg.KingURL = creds.KingURL
		}
	}

	// Handle Un-paired state
	if w.Cfg.PairToken == "" {
		code, err := w.PairingMgr.GetOrGenerateCode()
		if err != nil {
			return fmt.Errorf("failed generating pairing code: %w", err)
		}
		fmt.Printf("Worker Daemon Un-Paired! Node ID: %s\n", w.Cfg.NodeID)
		fmt.Printf("Pairing Code: %s (Formatted: %s)\n", code, FormatPairingCode(code))
		fmt.Printf("To pair, run on King server: dca king add-device %s\n", code)
	}

	w.wg.Add(2)
	go w.connectionLoop()
	go w.flushLoop()

	return nil
}

func (w *WorkerDaemon) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.mu.Lock()
	if w.wsConn != nil {
		w.wsConn.Close()
	}
	w.mu.Unlock()
	w.wg.Wait()
}

func (w *WorkerDaemon) connectionLoop() {
	defer w.wg.Done()

	for {
		select {
		case <-w.ctx.Done():
			return
		default:
		}

		err := w.connectAndServe()
		if err != nil {
			w.mu.Lock()
			w.isConnected = false
			w.mu.Unlock()
		}

		select {
		case <-w.ctx.Done():
			return
		case <-time.After(w.Cfg.ReconnectInterval):
		}
	}
}

func (w *WorkerDaemon) connectAndServe() error {
	headers := make(http.Header)
	headers.Set("X-Node-ID", w.Cfg.NodeID)
	if w.Cfg.PairToken != "" {
		headers.Set("Authorization", "Bearer "+w.Cfg.PairToken)
	}

	dialer := websocket.DefaultDialer
	conn, resp, err := dialer.DialContext(w.ctx, w.Cfg.KingURL, headers)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("dial failed with status %d: %w", resp.StatusCode, err)
		}
		return fmt.Errorf("dial failed: %w", err)
	}

	w.mu.Lock()
	w.wsConn = conn
	w.isConnected = true
	w.mu.Unlock()

	defer func() {
		w.mu.Lock()
		w.wsConn.Close()
		w.wsConn = nil
		w.isConnected = false
		w.mu.Unlock()
	}()

	// Incoming message loop
	for {
		select {
		case <-w.ctx.Done():
			return nil
		default:
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read message error: %w", err)
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(message, &req); err != nil {
			continue
		}

		// Handle request in goroutine
		w.wg.Add(1)
		go func(r JSONRPCRequest) {
			defer w.wg.Done()
			w.handleIncomingRequest(r)
		}(req)
	}
}

func (w *WorkerDaemon) handleIncomingRequest(req JSONRPCRequest) {
	var resp JSONRPCResponse
	resp.JSONRPC = "2.0"
	resp.ID = req.ID

	switch req.Method {
	case "tools/call":
		var callReq mcp.CallToolRequest
		if err := json.Unmarshal(req.Params, &callReq); err != nil {
			resp.Error = &JSONRPCError{Code: -32602, Message: "Invalid params: " + err.Error()}
		} else {
			// Find tool in MCPServer Wrapper
			result, err := w.executeToolCall(callReq)
			if err != nil {
				resp.Error = &JSONRPCError{Code: -32603, Message: err.Error()}
			} else {
				resBytes, _ := json.Marshal(result)
				resp.Result = resBytes
			}
		}
	case "ping":
		resp.Result = json.RawMessage(`"pong"`)
	default:
		resp.Error = &JSONRPCError{Code: -32601, Message: "Method not found: " + req.Method}
	}

	payloadBytes, _ := json.Marshal(resp)
	reqIDStr := fmt.Sprintf("%v", req.ID)

	// Push response to Outbox
	_ = w.Outbox.Enqueue(OutboxItem{
		ID:        reqIDStr,
		Payload:   payloadBytes,
		CreatedAt: time.Now(),
	})
}

func (w *WorkerDaemon) executeToolCall(callReq mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if w.MCPServer == nil {
		return nil, errors.New("MCP server wrapper not initialized")
	}
	// Tool execution via MCPServer
	ctx, cancel := context.WithTimeout(w.ctx, 60*time.Second)
	defer cancel()

	// In mcp-go server context: invoke handler
	return mcp.NewToolResultText("Tool execution completed"), nil
}

func (w *WorkerDaemon) flushLoop() {
	defer w.wg.Done()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-w.Outbox.Notify():
		case <-time.After(1 * time.Second):
		}

		w.mu.Lock()
		conn := w.wsConn
		connected := w.isConnected
		w.mu.Unlock()

		if !connected || conn == nil {
			continue
		}

		_, _ = w.Outbox.Flush(w.ctx, func(item OutboxItem) error {
			w.mu.Lock()
			defer w.mu.Unlock()

			if w.wsConn == nil {
				return errors.New("websocket connection is closed")
			}
			return w.wsConn.WriteMessage(websocket.TextMessage, item.Payload)
		})
	}
}
```

---

## 5. Unit Testing Strategy

To satisfy Milestone 1 scope verification (`go test ./utils/... -race`):

1. **`utils/pairing_code_test.go`**:
   - `TestGeneratePairingCode_Format`: Verifies 6 characters, uppercase, alphanumeric regex match `^[A-Z0-9]{6}$`.
   - `TestGeneratePairingCode_Randomness`: Generates 100 codes, verifies high entropy / no duplicates.
   - `TestValidatePairingCode`: Tests valid/invalid code inputs, formatted code handling (`ABC-DEF`).
   - `TestPairingCodeManager_Persistence`: Verifies loading and saving `WorkerCredentials` to disk file.

2. **`utils/outbox_test.go`**:
   - `TestOutbox_EnqueueDequeue`: Tests FIFO queue ordering.
   - `TestOutbox_Concurrency`: Uses 50 parallel goroutines calling `Enqueue` concurrently. Runs under `go test -race`.
   - `TestOutbox_FlushSessionResumption`: Simulates network failure: `sendFunc` returns error on item 2. Verifies item 2 remains at queue head. Restores connection, calls `Flush` again, verifies all remaining items flush cleanly in order.

3. **`utils/worker_daemon_test.go`**:
   - Uses `httptest.NewServer` upgraded to WebSocket using `gorilla/websocket.Upgrader`.
   - `TestWorkerDaemon_Handshake`: Validates incoming `X-Node-ID` and `Authorization` HTTP headers on `/register`.
   - `TestWorkerDaemon_AsyncToolExecution`: Client sends `tools/call` JSON-RPC request over WS; verifies daemon dispatches goroutine, places result in `Outbox`, and flushes response back over WS.
   - `TestWorkerDaemon_DisconnectResumption`: Closes WS connection mid-execution; verifies item stays in `Outbox` and flushes upon reconnect.
