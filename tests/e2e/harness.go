package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// ============================================================================
// 1. JSON-RPC 2.0 Structures & Helper Functions
// ============================================================================

// JSONRPCRequest represents a JSON-RPC 2.0 request payload.
type JSONRPCRequest struct {
	JSONVersion string          `json:"jsonrpc"`
	ID          interface{}     `json:"id,omitempty"`
	Method      string          `json:"method"`
	Params      json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse represents a JSON-RPC 2.0 response payload.
type JSONRPCResponse struct {
	JSONVersion string          `json:"jsonrpc"`
	ID          interface{}     `json:"id"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError represents a standard JSON-RPC 2.0 error object.
type JSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// NewJSONRPCRequest constructs a new JSONRPCRequest.
func NewJSONRPCRequest(id interface{}, method string, params interface{}) (*JSONRPCRequest, error) {
	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal params: %w", err)
		}
		rawParams = b
	}
	return &JSONRPCRequest{
		JSONVersion: "2.0",
		ID:          id,
		Method:      method,
		Params:      rawParams,
	}, nil
}

// NewJSONRPCResponse constructs a successful JSONRPCResponse.
func NewJSONRPCResponse(id interface{}, result interface{}) (*JSONRPCResponse, error) {
	var rawResult json.RawMessage
	if result != nil {
		b, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal result: %w", err)
		}
		rawResult = b
	}
	return &JSONRPCResponse{
		JSONVersion: "2.0",
		ID:          id,
		Result:      rawResult,
	}, nil
}

// NewJSONRPCErrorResponse constructs an error JSONRPCResponse.
func NewJSONRPCErrorResponse(id interface{}, code int, message string, data interface{}) (*JSONRPCResponse, error) {
	var rawData json.RawMessage
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal error data: %w", err)
		}
		rawData = b
	}
	return &JSONRPCResponse{
		JSONVersion: "2.0",
		ID:          id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
			Data:    rawData,
		},
	}, nil
}

// ParseJSONRPCMessage parses raw JSON into either a Request or Response.
func ParseJSONRPCMessage(data []byte) (id interface{}, isResponse bool, req *JSONRPCRequest, resp *JSONRPCResponse, err error) {
	var raw map[string]interface{}
	if parseErr := json.Unmarshal(data, &raw); parseErr != nil {
		return nil, false, nil, nil, fmt.Errorf("invalid json: %w", parseErr)
	}

	rawID := raw["id"]

	if _, hasResult := raw["result"]; hasResult || raw["error"] != nil {
		var r JSONRPCResponse
		if err := json.Unmarshal(data, &r); err != nil {
			return rawID, true, nil, nil, err
		}
		return r.ID, true, nil, &r, nil
	}

	if _, hasMethod := raw["method"]; hasMethod {
		var r JSONRPCRequest
		if err := json.Unmarshal(data, &r); err != nil {
			return rawID, false, nil, nil, err
		}
		return r.ID, false, &r, nil, nil
	}

	return rawID, false, nil, nil, errors.New("unknown JSON-RPC message structure")
}

// AssertJSONRPCResponse verifies response structure, matching expected ID and lack of error.
func AssertJSONRPCResponse(t testing.TB, resp *JSONRPCResponse, expectedID interface{}, expectError bool) {
	t.Helper()
	if resp == nil {
		t.Fatalf("expected non-nil JSON-RPC response")
	}
	if fmt.Sprintf("%v", resp.ID) != fmt.Sprintf("%v", expectedID) {
		t.Fatalf("expected response ID %v, got %v", expectedID, resp.ID)
	}
	if expectError && resp.Error == nil {
		t.Fatalf("expected JSON-RPC error, got success result")
	}
	if !expectError && resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: code=%d, message=%s", resp.Error.Code, resp.Error.Message)
	}
}

// AssertJSONRPCError verifies response error code.
func AssertJSONRPCError(t testing.TB, resp *JSONRPCResponse, expectedCode int) {
	t.Helper()
	if resp == nil || resp.Error == nil {
		t.Fatalf("expected JSON-RPC response with error")
	}
	if resp.Error.Code != expectedCode {
		t.Fatalf("expected error code %d, got %d (message: %s)", expectedCode, resp.Error.Code, resp.Error.Message)
	}
}

// GenerateUUID produces a standard RFC4122 v4 UUID string.
func GenerateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// GeneratePairingCode produces a random 6-character uppercase alphanumeric code.
func GeneratePairingCode() string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 6)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			b[i] = charset[i%len(charset)]
		} else {
			b[i] = charset[n.Int64()]
		}
	}
	return string(b)
}

// ============================================================================
// 2. Outbox Queue Implementation
// ============================================================================

// OutboxQueue provides thread-safe buffering for JSONRPCResponse messages.
type OutboxQueue struct {
	mu    sync.Mutex
	items []*JSONRPCResponse
}

// NewOutboxQueue initializes a new OutboxQueue.
func NewOutboxQueue() *OutboxQueue {
	return &OutboxQueue{items: make([]*JSONRPCResponse, 0)}
}

// Enqueue appends a response to the queue.
func (q *OutboxQueue) Enqueue(resp *JSONRPCResponse) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, resp)
}

// PopAll drains and returns all current items in FIFO order.
func (q *OutboxQueue) PopAll() []*JSONRPCResponse {
	q.mu.Lock()
	defer q.mu.Unlock()
	res := q.items
	q.items = make([]*JSONRPCResponse, 0)
	return res
}

// Len returns the number of buffered items.
func (q *OutboxQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// Items returns a copy of the queued items without clearing them.
func (q *OutboxQueue) Items() []*JSONRPCResponse {
	q.mu.Lock()
	defer q.mu.Unlock()
	cp := make([]*JSONRPCResponse, len(q.items))
	copy(cp, q.items)
	return cp
}

// Clear drains the queue without returning items.
func (q *OutboxQueue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = make([]*JSONRPCResponse, 0)
}

// ============================================================================
// 3. MockKing Control Plane Server
// ============================================================================

// MockKingOption configures a MockKing instance.
type MockKingOption func(*MockKing)

// WithRecoveryWindow configures the session recovery timeout window.
func WithRecoveryWindow(d time.Duration) MockKingOption {
	return func(m *MockKing) {
		m.recoveryWindow = d
	}
}

// MockKing represents a programmatic mock of the King Gateway Server.
type MockKing struct {
	Server            *httptest.Server
	URL               string
	WSSURL            string
	pairingCodes      map[string]string            // pairingCode -> deviceID
	codeExpirations   map[string]time.Time         // pairingCode -> expiration time
	pairTokens        map[string]string            // pairToken -> deviceID
	consumedCodes     map[string]bool              // pairingCode -> true
	activeConns       map[string]*websocket.Conn   // deviceID -> WSS conn
	registeredHeaders map[string]http.Header       // deviceID -> HTTP Headers
	pendingRequests   map[string]chan *JSONRPCResponse // requestUUID -> channel
	requestIDMap      map[string]interface{}       // requestUUID -> original ID
	pendingDevice     map[string]string            // requestUUID -> deviceID
	pendingPayload    map[string][]byte            // requestUUID -> rewrittenBody
	recoveryWindow    time.Duration
	maxPayloadSize    int64
	upgrader          websocket.Upgrader
	mu                sync.RWMutex
	writeMu           sync.Mutex
}

func (k *MockKing) writeConn(conn *websocket.Conn, msgType int, data []byte) error {
	k.writeMu.Lock()
	defer k.writeMu.Unlock()
	return conn.WriteMessage(msgType, data)
}

// NewMockKing initializes and starts a new MockKing server.
func NewMockKing(opts ...MockKingOption) *MockKing {
	king := &MockKing{
		pairingCodes:      make(map[string]string),
		codeExpirations:   make(map[string]time.Time),
		pairTokens:        make(map[string]string),
		consumedCodes:     make(map[string]bool),
		activeConns:       make(map[string]*websocket.Conn),
		registeredHeaders: make(map[string]http.Header),
		pendingRequests:   make(map[string]chan *JSONRPCResponse),
		requestIDMap:      make(map[string]interface{}),
		pendingDevice:     make(map[string]string),
		pendingPayload:    make(map[string][]byte),
		recoveryWindow:    30 * time.Second,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}

	for _, opt := range opts {
		opt(king)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/register", king.HandleRegister)
	mux.HandleFunc("/", king.HandleIngress)

	king.Server = httptest.NewServer(mux)
	king.URL = king.Server.URL
	king.WSSURL = "ws" + strings.TrimPrefix(king.Server.URL, "http")

	return king
}

// WithMaxPayloadSize configures the maximum allowed HTTP ingress payload size.
func WithMaxPayloadSize(size int64) MockKingOption {
	return func(m *MockKing) {
		m.maxPayloadSize = size
	}
}

// SetMaxPayloadSize updates the maximum allowed payload size dynamically.
func (k *MockKing) SetMaxPayloadSize(size int64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.maxPayloadSize = size
}

// AddPairingCode registers a pairing code for a target device ID.
func (k *MockKing) AddPairingCode(code, deviceID string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.pairingCodes[code] = deviceID
}

// AddPairingCodeWithTTL registers a pairing code with a time-to-live expiration.
func (k *MockKing) AddPairingCodeWithTTL(code, deviceID string, ttl time.Duration) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.pairingCodes[code] = deviceID
	k.codeExpirations[code] = time.Now().Add(ttl)
}

// ValidateAndPair validates a pairing code, marks it consumed, and generates a pair token.
func (k *MockKing) ValidateAndPair(code string) (token string, deviceID string, err error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	trimmed := strings.TrimSpace(code)
	normalized := strings.ToUpper(trimmed)

	targetCode := trimmed
	deviceID, exists := k.pairingCodes[trimmed]
	if !exists {
		deviceID, exists = k.pairingCodes[normalized]
		if exists {
			targetCode = normalized
		}
	}

	if !exists {
		return "", "", fmt.Errorf("invalid pairing code %s", code)
	}

	if k.consumedCodes[targetCode] {
		return "", "", fmt.Errorf("pairing code %s already consumed", code)
	}

	if exp, hasExp := k.codeExpirations[targetCode]; hasExp && time.Now().After(exp) {
		return "", "", fmt.Errorf("pairing code %s expired", code)
	}

	k.consumedCodes[targetCode] = true
	token = "token-" + GenerateUUID()
	k.pairTokens[token] = deviceID
	return token, deviceID, nil
}

// RegisterDeviceToken directly assigns a pair token to a device ID.
func (k *MockKing) RegisterDeviceToken(deviceID, token string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.pairTokens[token] = deviceID
}

// HandleRegister manages worker WebSocket registrations over /register.
func (k *MockKing) HandleRegister(w http.ResponseWriter, r *http.Request) {
	nodeID := strings.TrimSpace(r.Header.Get("X-Node-ID"))
	if nodeID == "" {
		// Check case-insensitive fallback if any
		for hKey, hVals := range r.Header {
			if strings.EqualFold(hKey, "X-Node-ID") && len(hVals) > 0 {
				nodeID = strings.TrimSpace(hVals[0])
				break
			}
		}
	}

	if nodeID == "" {
		http.Error(w, "Missing X-Node-ID header", http.StatusBadRequest)
		return
	}

	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if authHeader == "" {
		for hKey, hVals := range r.Header {
			if strings.EqualFold(hKey, "Authorization") && len(hVals) > 0 {
				authHeader = strings.TrimSpace(hVals[0])
				break
			}
		}
	}

	if authHeader == "" || !strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		http.Error(w, "Missing or invalid Authorization header", http.StatusUnauthorized)
		return
	}

	token := strings.TrimSpace(authHeader[7:])
	k.mu.RLock()
	expectedDeviceID, tokenValid := k.pairTokens[token]
	k.mu.RUnlock()

	if !tokenValid || expectedDeviceID != nodeID {
		http.Error(w, "Unauthorized pair token", http.StatusForbidden)
		return
	}

	conn, err := k.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	var oldConn *websocket.Conn
	k.mu.Lock()
	if old, exists := k.activeConns[nodeID]; exists {
		oldConn = old
	}
	k.registeredHeaders[nodeID] = r.Header.Clone()
	k.activeConns[nodeID] = conn
	k.mu.Unlock()

	if oldConn != nil {
		_ = oldConn.Close()
	}

	go k.flushPendingRequests(nodeID, conn)
	go k.readLoop(nodeID, conn)
}

func (k *MockKing) flushPendingRequests(nodeID string, conn *websocket.Conn) {
	k.mu.RLock()
	var payloads [][]byte
	for reqUUID, devID := range k.pendingDevice {
		if devID == nodeID {
			if payload, ok := k.pendingPayload[reqUUID]; ok {
				payloads = append(payloads, payload)
			}
		}
	}
	k.mu.RUnlock()

	for _, payload := range payloads {
		_ = k.writeConn(conn, websocket.TextMessage, payload)
	}
}

// readLoop listens for outbox responses pushed from workers.
func (k *MockKing) readLoop(nodeID string, conn *websocket.Conn) {
	defer func() {
		k.mu.Lock()
		if curr, ok := k.activeConns[nodeID]; ok && curr == conn {
			delete(k.activeConns, nodeID)
		}
		_ = conn.Close()
		k.mu.Unlock()
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}

		_, isResp, _, resp, parseErr := ParseJSONRPCMessage(msg)
		if parseErr != nil || !isResp || resp == nil {
			continue
		}

		reqUUID := fmt.Sprintf("%v", resp.ID)
		k.mu.RLock()
		ch, exists := k.pendingRequests[reqUUID]
		k.mu.RUnlock()

		if exists && ch != nil {
			select {
			case ch <- resp:
			default:
			}
		}
	}
}

// HandleIngress routes incoming HTTP MCP calls (POST /<device_id>/mcp) to target worker.
func (k *MockKing) HandleIngress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[1] != "mcp" {
		http.Error(w, "Invalid ingress route. Format: /<device_id>/mcp", http.StatusNotFound)
		return
	}

	deviceID := parts[0]

	k.mu.RLock()
	conn, connected := k.activeConns[deviceID]
	_, hasHeaders := k.registeredHeaders[deviceID]
	isPairTokenDev := false
	for _, devID := range k.pairTokens {
		if devID == deviceID {
			isPairTokenDev = true
			break
		}
	}
	limit := k.maxPayloadSize
	recWin := k.recoveryWindow
	k.mu.RUnlock()

	if !connected && !hasHeaders && !isPairTokenDev {
		http.Error(w, fmt.Sprintf("Worker device %s not found", deviceID), http.StatusNotFound)
		return
	}

	var body []byte
	var err error
	if limit > 0 {
		limitedReader := io.LimitReader(r.Body, limit+1)
		body, err = io.ReadAll(limitedReader)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}
		if int64(len(body)) > limit {
			http.Error(w, "Payload too large", http.StatusRequestEntityTooLarge)
			return
		}
	} else {
		body, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}
	}

	origID, isResp, req, _, parseErr := ParseJSONRPCMessage(body)
	if parseErr != nil || isResp || req == nil {
		http.Error(w, `{"jsonrpc":"2.0","error":{"code":-32700,"message":"Parse error"},"id":null}`, http.StatusBadRequest)
		return
	}

	reqUUID := GenerateUUID()
	req.ID = reqUUID

	rewrittenBody, err := json.Marshal(req)
	if err != nil {
		http.Error(w, "Failed to encode rewritten request", http.StatusInternalServerError)
		return
	}

	ch := make(chan *JSONRPCResponse, 1)

	k.mu.Lock()
	k.pendingRequests[reqUUID] = ch
	k.requestIDMap[reqUUID] = origID
	k.pendingDevice[reqUUID] = deviceID
	k.pendingPayload[reqUUID] = rewrittenBody
	k.mu.Unlock()

	defer func() {
		k.mu.Lock()
		delete(k.pendingRequests, reqUUID)
		delete(k.requestIDMap, reqUUID)
		delete(k.pendingDevice, reqUUID)
		delete(k.pendingPayload, reqUUID)
		k.mu.Unlock()
	}()

	if conn != nil {
		_ = k.writeConn(conn, websocket.TextMessage, rewrittenBody)
	}

	select {
	case resp := <-ch:
		resp.ID = origID
		outBytes, _ := json.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(outBytes)

	case <-time.After(recWin):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGatewayTimeout)
		errResp, _ := NewJSONRPCErrorResponse(origID, -32000, "Request timeout during session recovery window", nil)
		outBytes, _ := json.Marshal(errResp)
		_, _ = w.Write(outBytes)
	}
}

// GetActiveConn returns active WebSocket connection for deviceID.
func (k *MockKing) GetActiveConn(deviceID string) (*websocket.Conn, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	conn, ok := k.activeConns[deviceID]
	return conn, ok
}

// GetRegisteredHeaders returns headers captured during registration for deviceID.
func (k *MockKing) GetRegisteredHeaders(deviceID string) (http.Header, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	h, ok := k.registeredHeaders[deviceID]
	return h, ok
}

// GetPendingCount returns current pending request count.
func (k *MockKing) GetPendingCount() int {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return len(k.pendingRequests)
}

// SetRecoveryWindow updates the session recovery window duration.
func (k *MockKing) SetRecoveryWindow(d time.Duration) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.recoveryWindow = d
}

// Close shuts down the MockKing server.
func (k *MockKing) Close() {
	k.mu.Lock()
	for _, conn := range k.activeConns {
		_ = conn.Close()
	}
	k.mu.Unlock()
	k.Server.Close()
}

// ============================================================================
// 4. MockWorker Client
// ============================================================================

// ToolHandler represents a function handling a tool execution call.
type ToolHandler func(params json.RawMessage) (interface{}, error)

// MockWorker represents a programmatic mock of the Worker Daemon client.
type MockWorker struct {
	NodeID         string
	PairToken      string
	PairCode       string
	KingWSSURL     string
	Outbox         *OutboxQueue
	wsConn         *websocket.Conn
	connMu         sync.Mutex
	toolHandlers   map[string]ToolHandler
	isDisconnected bool
	mu             sync.RWMutex
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
}

// NewMockWorker constructs a MockWorker instance.
func NewMockWorker(nodeID, kingWSSURL, pairToken string) *MockWorker {
	ctx, cancel := context.WithCancel(context.Background())
	return &MockWorker{
		NodeID:       nodeID,
		KingWSSURL:   kingWSSURL,
		PairToken:    pairToken,
		PairCode:     GeneratePairingCode(),
		Outbox:       NewOutboxQueue(),
		toolHandlers: make(map[string]ToolHandler),
		ctx:          ctx,
		cancel:       cancel,
	}
}

// RegisterTool registers a tool execution handler for a specific method name.
func (w *MockWorker) RegisterTool(method string, handler ToolHandler) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.toolHandlers[method] = handler
}

// Connect connects outbound to King WSS server with registration headers.
func (w *MockWorker) Connect() error {
	headers := http.Header{}
	headers.Set("X-Node-ID", w.NodeID)
	headers.Set("Authorization", "Bearer "+w.PairToken)

	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(w.KingWSSURL, headers)
	if err != nil {
		return fmt.Errorf("worker dial failed: %w", err)
	}

	w.connMu.Lock()
	if w.wsConn != nil {
		_ = w.wsConn.Close()
	}
	w.wsConn = conn
	w.isDisconnected = false
	w.connMu.Unlock()

	w.wg.Add(1)
	go w.readLoop(conn)

	_ = w.FlushOutbox()

	return nil
}

// Disconnect simulates a network dropout by closing the WSS connection.
func (w *MockWorker) Disconnect() {
	w.connMu.Lock()
	defer w.connMu.Unlock()

	w.isDisconnected = true
	if w.wsConn != nil {
		_ = w.wsConn.Close()
		w.wsConn = nil
	}
}

// AbruptDrop closes the underlying TCP connection directly without sending a WebSocket Close frame.
func (w *MockWorker) AbruptDrop() {
	w.connMu.Lock()
	defer w.connMu.Unlock()

	w.isDisconnected = true
	if w.wsConn != nil {
		_ = w.wsConn.UnderlyingConn().Close()
		w.wsConn = nil
	}
}

// Reconnect re-establishes WSS connection and flushes queued outbox items.
func (w *MockWorker) Reconnect() error {
	return w.Connect()
}

// EnqueueOutbox buffers a response in the local outbox.
func (w *MockWorker) EnqueueOutbox(resp *JSONRPCResponse) {
	w.Outbox.Enqueue(resp)
}

// FlushOutbox sends queued outbox items across active WSS connection.
func (w *MockWorker) FlushOutbox() error {
	w.connMu.Lock()
	defer w.connMu.Unlock()

	if w.isDisconnected || w.wsConn == nil {
		return errors.New("cannot flush outbox: worker is disconnected")
	}

	items := w.Outbox.PopAll()
	if len(items) == 0 {
		return nil
	}

	for _, resp := range items {
		data, err := json.Marshal(resp)
		if err != nil {
			continue
		}
		if err := w.wsConn.WriteMessage(websocket.TextMessage, data); err != nil {
			w.Outbox.Enqueue(resp)
			return fmt.Errorf("outbox flush failed mid-stream: %w", err)
		}
	}
	return nil
}

// readLoop listens for incoming request frames from King.
func (w *MockWorker) readLoop(conn *websocket.Conn) {
	defer w.wg.Done()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}

		_, isResp, req, _, parseErr := ParseJSONRPCMessage(msg)
		if parseErr != nil || isResp || req == nil {
			continue
		}

		w.handleRequest(req)
	}
}

// handleRequest dispatches incoming request to tool handlers and responds or enqueues to Outbox.
func (w *MockWorker) handleRequest(req *JSONRPCRequest) {
	w.mu.RLock()
	handler, exists := w.toolHandlers[req.Method]
	w.mu.RUnlock()

	var resp *JSONRPCResponse
	if !exists {
		resp, _ = NewJSONRPCErrorResponse(req.ID, -32601, fmt.Sprintf("Method %s not found", req.Method), nil)
	} else {
		res, err := handler(req.Params)
		if err != nil {
			resp, _ = NewJSONRPCErrorResponse(req.ID, -32000, err.Error(), nil)
		} else {
			resp, _ = NewJSONRPCResponse(req.ID, res)
		}
	}

	w.connMu.Lock()
	defer w.connMu.Unlock()

	if !w.isDisconnected && w.wsConn != nil {
		data, _ := json.Marshal(resp)
		if err := w.wsConn.WriteMessage(websocket.TextMessage, data); err != nil {
			w.Outbox.Enqueue(resp)
		}
	} else {
		w.Outbox.Enqueue(resp)
	}
}

// IsConnected returns whether worker is currently connected over WSS.
func (w *MockWorker) IsConnected() bool {
	w.connMu.Lock()
	defer w.connMu.Unlock()
	return !w.isDisconnected && w.wsConn != nil
}

// Stop shuts down MockWorker instance.
func (w *MockWorker) Stop() {
	w.cancel()
	w.Disconnect()
	w.wg.Wait()
}

// ============================================================================
// 5. CLIRunner Utility
// ============================================================================

// CLIRunner provides helper methods for launching and testing CLI commands.
type CLIRunner struct {
	BinaryPath string
	WorkingDir string
	Env        []string
}

// NewCLIRunner constructs a new CLIRunner instance.
func NewCLIRunner(binaryPath string) *CLIRunner {
	return &CLIRunner{
		BinaryPath: binaryPath,
		Env:        os.Environ(),
	}
}

// Run executes a CLI command synchronously.
func (c *CLIRunner) Run(args ...string) (stdout string, stderr string, exitCode int, err error) {
	return c.RunWithContext(context.Background(), args...)
}

// RunWithContext executes a CLI command with context cancellation.
func (c *CLIRunner) RunWithContext(ctx context.Context, args ...string) (stdout string, stderr string, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, c.BinaryPath, args...)
	if c.WorkingDir != "" {
		cmd.Dir = c.WorkingDir
	}
	if len(c.Env) > 0 {
		cmd.Env = c.Env
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()
	exitCode = 0

	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
		err = runErr
	}

	return stdout, stderr, exitCode, err
}
