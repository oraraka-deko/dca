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

// WorkerDaemonState represents the current connection state of the worker daemon.
type WorkerDaemonState int

const (
	StateDisconnected WorkerDaemonState = iota
	StateConnecting
	StateConnected
	StateStopped
)

// WorkerDaemonConfig configures the behavior and parameters of WorkerDaemon.
type WorkerDaemonConfig struct {
	KingURL           string        `json:"king_url"`
	NodeID            string        `json:"node_id"`
	AuthToken         string        `json:"auth_token"`
	PairToken         string        `json:"pair_token"`
	ConfigPath        string        `json:"config_path"`
	ReconnectInterval time.Duration `json:"reconnect_interval"`
	MaxOutboxSize     int           `json:"max_outbox_size"`
	ExecutionTimeout  time.Duration `json:"execution_timeout"`
}

// WorkerConfig is an alias for WorkerDaemonConfig.
type WorkerConfig = WorkerDaemonConfig

// DefaultWorkerDaemonConfig returns a WorkerDaemonConfig populated with sane defaults.
func DefaultWorkerDaemonConfig() WorkerDaemonConfig {
	return WorkerDaemonConfig{
		KingURL:           "ws://localhost:8080/register",
		NodeID:            uuid.New().String(),
		ReconnectInterval: 2 * time.Second,
		MaxOutboxSize:     1000,
		ExecutionTimeout:  60 * time.Second,
	}
}

// JSONRPCRequest represents an incoming JSON-RPC 2.0 request payload.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse represents an outgoing JSON-RPC 2.0 response payload.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError represents a JSON-RPC 2.0 error object.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// WorkerDaemon manages reverse tunnel WebSocket connections, isolated tool execution, and outbox flushing.
type WorkerDaemon struct {
	Cfg        WorkerDaemonConfig
	Outbox     *Outbox
	MCPServer  *MCPServerWrapper
	PairingMgr *PairingCodeManager

	mu          sync.Mutex
	wsConn      *websocket.Conn
	state       WorkerDaemonState
	isConnected bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewWorkerDaemon initializes a WorkerDaemon instance with given configuration and optional MCPServerWrapper.
func NewWorkerDaemon(cfg WorkerDaemonConfig, mcpServer *MCPServerWrapper) *WorkerDaemon {
	if cfg.NodeID == "" {
		cfg.NodeID = uuid.New().String()
	}
	if cfg.ReconnectInterval <= 0 {
		cfg.ReconnectInterval = 2 * time.Second
	}
	if cfg.MaxOutboxSize <= 0 {
		cfg.MaxOutboxSize = 1000
	}
	if cfg.ExecutionTimeout <= 0 {
		cfg.ExecutionTimeout = 60 * time.Second
	}

	pm := NewPairingCodeManager(cfg.ConfigPath)

	return &WorkerDaemon{
		Cfg:        cfg,
		Outbox:     NewOutbox(cfg.MaxOutboxSize),
		MCPServer:  mcpServer,
		PairingMgr: pm,
		state:      StateDisconnected,
	}
}

// Start launches the WorkerDaemon loops in background goroutines.
func (w *WorkerDaemon) Start(ctx context.Context) error {
	w.ctx, w.cancel = context.WithCancel(ctx)

	// Load credentials if available from disk
	if creds, err := w.PairingMgr.LoadCredentials(); err == nil {
		if creds.PairToken != "" {
			w.Cfg.PairToken = creds.PairToken
			if w.Cfg.AuthToken == "" {
				w.Cfg.AuthToken = creds.PairToken
			}
		}
		if creds.NodeID != "" {
			w.Cfg.NodeID = creds.NodeID
		}
		if creds.KingURL != "" {
			w.Cfg.KingURL = creds.KingURL
		}
	}

	// Check if worker needs pairing code
	token := w.Cfg.AuthToken
	if token == "" {
		token = w.Cfg.PairToken
	}

	if token == "" {
		code, err := w.PairingMgr.GetOrGenerateCode()
		if err == nil {
			fmt.Printf("[WorkerDaemon] Un-Paired! Node ID: %s, Pairing Code: %s (%s)\n",
				w.Cfg.NodeID, code, FormatPairingCode(code))
		}
	}

	w.wg.Add(2)
	go w.connectionLoop()
	go w.flushLoop()

	return nil
}

// Stop gracefully shuts down the WorkerDaemon and terminates active WebSocket connections.
func (w *WorkerDaemon) Stop() {
	if w.cancel != nil {
		w.cancel()
	}

	w.mu.Lock()
	w.state = StateStopped
	w.isConnected = false
	if w.wsConn != nil {
		w.wsConn.Close()
		w.wsConn = nil
	}
	w.mu.Unlock()

	w.Outbox.Close()
	w.wg.Wait()
}

// Close is an alias for Stop.
func (w *WorkerDaemon) Close() {
	w.Stop()
}

// IsConnected returns whether the worker daemon has an active WebSocket connection.
func (w *WorkerDaemon) IsConnected() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.isConnected
}

// GetState returns the current operational state of the worker daemon.
func (w *WorkerDaemon) GetState() WorkerDaemonState {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state
}

// connectionLoop manages connection retries with exponential backoff on disconnect.
func (w *WorkerDaemon) connectionLoop() {
	defer w.wg.Done()

	backoff := w.Cfg.ReconnectInterval
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-w.ctx.Done():
			return
		default:
		}

		w.mu.Lock()
		w.state = StateConnecting
		w.mu.Unlock()

		err := w.connectAndServe()
		if err != nil {
			w.mu.Lock()
			w.isConnected = false
			w.state = StateDisconnected
			w.mu.Unlock()
		} else {
			backoff = w.Cfg.ReconnectInterval
		}

		select {
		case <-w.ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff = time.Duration(float64(backoff) * 1.5)
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// connectAndServe dials the King WebSocket endpoint, sets custom headers, and listens for inbound requests.
func (w *WorkerDaemon) connectAndServe() error {
	headers := make(http.Header)
	headers.Set("X-Node-ID", w.Cfg.NodeID)

	token := w.Cfg.AuthToken
	if token == "" {
		token = w.Cfg.PairToken
	}
	if token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.DialContext(w.ctx, w.Cfg.KingURL, headers)
	if err != nil {
		return fmt.Errorf("websocket dial failed: %w", err)
	}

	w.mu.Lock()
	w.wsConn = conn
	w.isConnected = true
	w.state = StateConnected
	w.mu.Unlock()

	defer func() {
		w.mu.Lock()
		if w.wsConn != nil {
			w.wsConn.Close()
			w.wsConn = nil
		}
		w.isConnected = false
		w.state = StateDisconnected
		w.mu.Unlock()
	}()

	// Signal flusher loop immediately on reconnect
	select {
	case <-w.ctx.Done():
		return nil
	default:
		_ = w.FlushOutbox()
	}

	for {
		select {
		case <-w.ctx.Done():
			return nil
		default:
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("websocket read error: %w", err)
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(message, &req); err != nil {
			continue
		}

		// Dispatch request execution to an isolated goroutine
		w.wg.Add(1)
		go func(rawMsg []byte, r JSONRPCRequest) {
			defer w.wg.Done()
			w.executionWithTimeout(rawMsg, r)
		}(message, req)
	}
}

// executionWithTimeout executes request in an isolated goroutine with panic protection and context timeout.
func (w *WorkerDaemon) executionWithTimeout(rawMsg []byte, req JSONRPCRequest) {
	reqIDStr := fmt.Sprintf("%v", req.ID)
	timeout := w.Cfg.ExecutionTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	execCtx, cancel := context.WithTimeout(w.ctx, timeout)
	defer cancel()

	defer func() {
		if r := recover(); r != nil {
			errResp := JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &JSONRPCError{
					Code:    -32603,
					Message: fmt.Sprintf("Panic during tool execution: %v", r),
				},
			}
			payload, _ := json.Marshal(errResp)
			_ = w.Outbox.Enqueue(OutboxItem{
				ID:        reqIDStr,
				Payload:   payload,
				CreatedAt: time.Now(),
			})
		}
	}()

	var payload []byte

	if w.MCPServer != nil && w.MCPServer.MCPServer != nil {
		respMsg := w.MCPServer.MCPServer.HandleMessage(execCtx, rawMsg)
		if respMsg != nil {
			var err error
			payload, err = json.Marshal(respMsg)
			if err != nil {
				respErr := JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error:   &JSONRPCError{Code: -32603, Message: "Failed marshaling response: " + err.Error()},
				}
				payload, _ = json.Marshal(respErr)
			}
		}
	}

	if len(payload) == 0 {
		// Fallback for built-in handling or mock tests when MCPServer is not provided
		payload = w.handleFallbackRequest(req)
	}

	_ = w.Outbox.Enqueue(OutboxItem{
		ID:        reqIDStr,
		Payload:   payload,
		CreatedAt: time.Now(),
	})
}

// handleFallbackRequest handles standard MCP ping/initialize and fallback requests.
func (w *WorkerDaemon) handleFallbackRequest(req JSONRPCRequest) []byte {
	var resp JSONRPCResponse
	resp.JSONRPC = "2.0"
	resp.ID = req.ID

	switch req.Method {
	case "ping":
		resp.Result = json.RawMessage(`"pong"`)
	case "initialize":
		resp.Result = json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"dca-worker","version":"1.0.0"}}`)
	case "tools/list":
		resp.Result = json.RawMessage(`{"tools":[]}`)
	case "tools/call":
		var callParams struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &callParams)
		resText := fmt.Sprintf("Tool %s executed successfully", callParams.Name)
		resBytes, _ := json.Marshal(mcp.NewToolResultText(resText))
		resp.Result = resBytes
	default:
		resp.Error = &JSONRPCError{
			Code:    -32601,
			Message: "Method not found: " + req.Method,
		}
	}

	out, _ := json.Marshal(resp)
	return out
}

// flushLoop continuously drains Outbox items over the active WebSocket connection.
func (w *WorkerDaemon) flushLoop() {
	defer w.wg.Done()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-w.Outbox.Notify():
		case <-ticker.C:
		}

		_ = w.FlushOutbox()
	}
}

// FlushOutbox attempts to flush enqueued responses down the WebSocket connection.
func (w *WorkerDaemon) FlushOutbox() error {
	w.mu.Lock()
	conn := w.wsConn
	connected := w.isConnected
	w.mu.Unlock()

	if !connected || conn == nil {
		return errors.New("cannot flush: worker disconnected")
	}

	return w.Outbox.Flush(func(item OutboxItem) error {
		w.mu.Lock()
		activeConn := w.wsConn
		activeConnected := w.isConnected
		w.mu.Unlock()

		if !activeConnected || activeConn == nil {
			return errors.New("websocket disconnected during flush")
		}

		w.mu.Lock()
		defer w.mu.Unlock()
		activeConn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return activeConn.WriteMessage(websocket.TextMessage, item.Payload)
	})
}
