package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// PendingRequest represents an in-flight JSON-RPC request waiting for a worker response.
type PendingRequest struct {
	UUID       string
	OriginalID interface{}
	DeviceID   string
	RespChan   chan *JSONRPCResponse
	CreatedAt  time.Time
}

// KingIngress provides HTTP route-based ingress at /<device_id>/mcp and handles transport-agnostic pending request demuxing.
type KingIngress struct {
	ActiveConns     *sync.Map     // map[string]*WorkerConn
	PendingRequests sync.Map      // map[string]*PendingRequest
	Timeout         time.Duration // Session recovery window timeout (default 30s)
	MaxPayloadSize  int64         // Maximum allowed request body size in bytes (0 for default 10MB)
}

// NewKingIngress initializes a KingIngress router instance.
func NewKingIngress(activeConns *sync.Map) *KingIngress {
	if activeConns == nil {
		activeConns = &sync.Map{}
	}
	return &KingIngress{
		ActiveConns: activeConns,
		Timeout:     30 * time.Second,
	}
}

// ServeHTTP handles incoming HTTP requests at /<device_id>/mcp.
func (ing *KingIngress) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. Verify HTTP Method
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// 2. Parse Path Pattern: /<device_id>/mcp
	path := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[1] != "mcp" || parts[0] == "" {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	deviceID := parts[0]

	// 3. Read and Parse JSON-RPC Payload
	limit := ing.MaxPayloadSize
	if limit <= 0 {
		limit = 10 * 1024 * 1024 // Default 10MB
	}
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, limit))
	if err != nil {
		writeJSONRPCError(w, http.StatusBadRequest, nil, -32700, "Parse error reading request body")
		return
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		writeJSONRPCError(w, http.StatusBadRequest, nil, -32700, "Invalid JSON payload: "+err.Error())
		return
	}

	if req.JSONRPC != "2.0" || req.Method == "" {
		writeJSONRPCError(w, http.StatusBadRequest, req.ID, -32600, "Invalid Request: missing jsonrpc='2.0' or method")
		return
	}

	// 4. Verify Active Worker Connection
	val, ok := ing.ActiveConns.Load(deviceID)
	if !ok || val == nil {
		writeJSONRPCError(w, http.StatusServiceUnavailable, req.ID, -32001, fmt.Sprintf("Device '%s' not connected or offline", deviceID))
		return
	}
	workerConn, ok := val.(*WorkerConn)
	if !ok || workerConn == nil {
		writeJSONRPCError(w, http.StatusServiceUnavailable, req.ID, -32001, fmt.Sprintf("Device '%s' not connected or offline", deviceID))
		return
	}

	// 5. Rewrite ID to UUID and Register PendingRequest
	originalID := req.ID
	rewrittenUUID := uuid.New().String()
	req.ID = rewrittenUUID

	pending := &PendingRequest{
		UUID:       rewrittenUUID,
		OriginalID: originalID,
		DeviceID:   deviceID,
		RespChan:   make(chan *JSONRPCResponse, 1),
		CreatedAt:  time.Now(),
	}

	ing.PendingRequests.Store(rewrittenUUID, pending)

	// 6. Forward Request down Worker WebSocket Tunnel (NO TOOL RENAMING / PREFIXING)
	reqBytes, err := json.Marshal(req)
	if err != nil {
		ing.PendingRequests.Delete(rewrittenUUID)
		writeJSONRPCError(w, http.StatusInternalServerError, originalID, -32603, "Internal error marshaling request")
		return
	}

	if err := workerConn.WriteTextMessage(reqBytes); err != nil {
		ing.PendingRequests.Delete(rewrittenUUID)
		writeJSONRPCError(w, http.StatusBadGateway, originalID, -32002, "Failed transmitting request to worker WebSocket tunnel")
		return
	}

	// 7. Session Waiter with 30-Second Recovery Window
	ctx, cancel := context.WithTimeout(r.Context(), ing.Timeout)
	defer cancel()

	select {
	case resp := <-pending.RespChan:
		// Worker response received and demuxed cleanly
		resp.ID = pending.OriginalID // Restore original agent request ID
		respBytes, err := json.Marshal(resp)
		if err != nil {
			writeJSONRPCError(w, http.StatusInternalServerError, originalID, -32603, "Failed marshaling worker response")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(respBytes)

	case <-ctx.Done():
		// 30s session recovery timeout expired or client cancelled
		ing.PendingRequests.Delete(rewrittenUUID)
		writeJSONRPCError(w, http.StatusGatewayTimeout, originalID, -32000, "Gateway Timeout: worker response timed out after 30 seconds")
	}
}

// DemuxResponse receives raw worker WebSocket response bytes, matches rewritten UUID, and forwards to waiting channel.
func (ing *KingIngress) DemuxResponse(data []byte) bool {
	var resp JSONRPCResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return false
	}
	return ing.DemuxResponseStruct(&resp)
}

// DemuxResponseStruct processes an unmarshaled JSONRPCResponse struct.
func (ing *KingIngress) DemuxResponseStruct(resp *JSONRPCResponse) bool {
	if resp.ID == nil {
		return false
	}
	uuidStr := fmt.Sprintf("%v", resp.ID)

	val, ok := ing.PendingRequests.LoadAndDelete(uuidStr)
	if !ok || val == nil {
		// No pending request found (already timed out or invalid ID)
		return false
	}

	pending, ok := val.(*PendingRequest)
	if !ok || pending == nil {
		return false
	}

	select {
	case pending.RespChan <- resp:
		return true
	default:
		return false
	}
}

// writeJSONRPCError formats and writes a JSON-RPC 2.0 error HTTP response.
func writeJSONRPCError(w http.ResponseWriter, httpStatus int, id interface{}, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	errResp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:          id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
	_ = json.NewEncoder(w).Encode(errResp)
}
