package utils

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// // JSONRPCRequest represents a JSON-RPC 2.0 request payload.
// type JSONRPCRequest struct {
// 	JSONVersion string          `json:"jsonrpc"`
// 	ID          interface{}     `json:"id,omitempty"`
// 	Method      string          `json:"method"`
// 	Params      json.RawMessage `json:"params,omitempty"`
// }

// // JSONRPCResponse represents a JSON-RPC 2.0 response payload.
// type JSONRPCResponse struct {
// 	JSONVersion string          `json:"jsonrpc"`
// 	ID          interface{}     `json:"id"`
// 	Result      json.RawMessage `json:"result,omitempty"`
// 	Error       *JSONRPCError   `json:"error,omitempty"`
// }

// // JSONRPCError represents a standard JSON-RPC 2.0 error object.
// type JSONRPCError struct {
// 	Code    int             `json:"code"`
// 	Message string          `json:"message"`
// 	Data    json.RawMessage `json:"data,omitempty"`
// }

// WorkerConn encapsulates an active WebSocket connection from a Worker daemon,
// along with session metadata and write synchronization.
type WorkerConn struct {
	NodeID            string
	Conn              *websocket.Conn
	mu                sync.Mutex // Synchronizes writes to Conn (gorilla/websocket write lock)
	RegisteredHeaders http.Header
	ConnectedAt       time.Time
	Context           context.Context
	Cancel            context.CancelFunc
	Initialized       bool
	Tools             []map[string]interface{}
	LastPing          time.Time
}

// WriteJSONRPC sends a JSON-RPC payload down the WebSocket tunnel safely.
func (wc *WorkerConn) WriteJSONRPC(v interface{}) error {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	if wc.Conn == nil {
		return fmt.Errorf("connection is nil")
	}
	return wc.Conn.WriteJSON(v)
}

// WriteTextMessage sends a raw text frame down the WebSocket tunnel safely.
func (wc *WorkerConn) WriteTextMessage(msg []byte) error {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	if wc.Conn == nil {
		return fmt.Errorf("connection is nil")
	}
	return wc.Conn.WriteMessage(websocket.TextMessage, msg)
}

// Close cleanly closes the WebSocket connection and cancels context.
func (wc *WorkerConn) Close() error {
	if wc.Cancel != nil {
		wc.Cancel()
	}
	if wc.Conn != nil {
		_ = wc.Conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "connection closed"),
			time.Now().Add(1*time.Second),
		)
		return wc.Conn.Close()
	}
	return nil
}

// TokenValidatorFunc validates an activation token against a target node ID.
type TokenValidatorFunc func(nodeID, token string) bool

// KingGateway manages worker registrations over /register, connection registry ActiveConns,
// protocol inversion handshakes, and frame routing.
type KingGateway struct {
	ActiveConns    sync.Map // map[string]*WorkerConn (NodeID -> *WorkerConn)
	Upgrader       websocket.Upgrader
	TokenValidator TokenValidatorFunc
	PairingMgr     *PairingManager
	Ingress        *KingIngress
	mu             sync.RWMutex
}

// NewKingGateway initializes a new KingGateway server instance.
func NewKingGateway(validator TokenValidatorFunc) *KingGateway {
	return &KingGateway{
		TokenValidator: validator,
		Upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// ExtractHeader retrieves a header value from http.Header case-insensitively with whitespace trimming.
func ExtractHeader(h http.Header, name string) string {
	val := strings.TrimSpace(h.Get(name))
	if val != "" {
		return val
	}
	for k, vals := range h {
		if strings.EqualFold(k, name) && len(vals) > 0 {
			return strings.TrimSpace(vals[0])
		}
	}
	return ""
}

// HandleRegister manages worker WebSocket registrations over /register.
func (kg *KingGateway) HandleRegister(w http.ResponseWriter, r *http.Request) {
	nodeID := ExtractHeader(r.Header, "X-Node-ID")
	if nodeID == "" {
		http.Error(w, "Missing X-Node-ID header", http.StatusBadRequest)
		return
	}

	authHeader := ExtractHeader(r.Header, "Authorization")
	if authHeader == "" || !strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		http.Error(w, "Missing or invalid Authorization header", http.StatusUnauthorized)
		return
	}

	token := strings.TrimSpace(authHeader[7:])
	if token == "" {
		http.Error(w, "Missing or invalid Authorization header", http.StatusUnauthorized)
		return
	}

	authorized := false
	if kg.TokenValidator != nil {
		authorized = kg.TokenValidator(nodeID, token)
	} else if kg.PairingMgr != nil {
		devID, valid := kg.PairingMgr.ValidatePairToken(token)
		authorized = valid && devID == nodeID
	} else {
		// Fallback: accept token if no validator or pairing manager configured
		authorized = true
	}

	if !authorized {
		http.Error(w, "Unauthorized pair token", http.StatusForbidden)
		return
	}

	conn, err := kg.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	workerConn := &WorkerConn{
		NodeID:            nodeID,
		Conn:              conn,
		RegisteredHeaders: r.Header.Clone(),
		ConnectedAt:       time.Now(),
		Context:           ctx,
		Cancel:            cancel,
	}

	// Preempt existing connection for nodeID if present
	if oldVal, loaded := kg.ActiveConns.Swap(nodeID, workerConn); loaded {
		if oldConn, ok := oldVal.(*WorkerConn); ok && oldConn != nil {
			_ = oldConn.Close()
		}
	}

	// Perform Protocol Inversion Handshake & Start Read Loop
	go kg.performProtocolInversionHandshake(workerConn)
	go kg.readLoop(workerConn)
}

// performProtocolInversionHandshake issues initialize and tools/list requests from King (MCP Client) to Worker.
func (kg *KingGateway) performProtocolInversionHandshake(wc *WorkerConn) {
	// Step 1: Send initialize request
	initReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "king-init-1",
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo": map[string]string{
				"name":    "dca-king-gateway",
				"version": "1.0.0",
			},
		},
	}
	_ = wc.WriteJSONRPC(initReq)

	// Step 2: Send tools/list request
	toolsReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "king-init-2",
		"method":  "tools/list",
		"params":  map[string]interface{}{},
	}
	_ = wc.WriteJSONRPC(toolsReq)

	wc.Initialized = true
}

// readLoop reads frames from worker WebSocket, demuxes responses, and handles disconnection cleanup safely.
func (kg *KingGateway) readLoop(wc *WorkerConn) {
	defer func() {
		// Race-free cleanup: only delete from ActiveConns if current entry is wc
		if currVal, ok := kg.ActiveConns.Load(wc.NodeID); ok {
			if currConn, ok := currVal.(*WorkerConn); ok && currConn == wc {
				kg.ActiveConns.Delete(wc.NodeID)
			}
		}
		_ = wc.Close()
	}()

	for {
		_, message, err := wc.Conn.ReadMessage()
		if err != nil {
			break
		}
		if kg.Ingress != nil {
			kg.Ingress.DemuxResponse(message)
		}
	}
}
