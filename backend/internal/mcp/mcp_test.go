package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMCPInitializeAndPing(t *testing.T) {
	handler := NewHandler(nil, nil)

	// 1. Initialize
	initReq := []byte(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "initialize",
		"params": {
			"protocolVersion": "2024-11-05",
			"clientInfo": {"name": "test-client", "version": "1.0.0"}
		}
	}`)

	resp := handler.HandleRequest(context.Background(), initReq)
	if resp.Error != nil {
		t.Fatalf("unexpected error on initialize: %v", resp.Error)
	}

	resMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got: %T", resp.Result)
	}
	if resMap["protocolVersion"] != ProtocolVersion {
		t.Errorf("expected protocolVersion %s, got %v", ProtocolVersion, resMap["protocolVersion"])
	}

	// 2. Ping
	pingReq := []byte(`{"jsonrpc": "2.0", "id": 2, "method": "ping"}`)
	pingResp := handler.HandleRequest(context.Background(), pingReq)
	if pingResp.Error != nil {
		t.Fatalf("unexpected error on ping: %v", pingResp.Error)
	}

	// 3. Tools List
	toolsReq := []byte(`{"jsonrpc": "2.0", "id": 3, "method": "tools/list"}`)
	toolsResp := handler.HandleRequest(context.Background(), toolsReq)
	if toolsResp.Error != nil {
		t.Fatalf("unexpected error on tools/list: %v", toolsResp.Error)
	}

	toolsMap, ok := toolsResp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected tools result map")
	}
	toolsList, ok := toolsMap["tools"].([]Tool)
	if !ok || len(toolsList) == 0 {
		t.Fatalf("expected non-empty tools list, got %v", toolsMap["tools"])
	}

	// Verify required tools are present
	expectedTools := map[string]bool{
		"list_recordings":    false,
		"get_recording":      false,
		"get_transcript":     false,
		"download_recording": false,
		"sync_recordings":    false,
		"get_sync_status":    false,
		"auth_status":        false,
		"auth_refresh":       false,
	}
	for _, tool := range toolsList {
		if _, exists := expectedTools[tool.Name]; exists {
			expectedTools[tool.Name] = true
		}
	}
	for name, found := range expectedTools {
		if !found {
			t.Errorf("expected tool %s not found in tools/list", name)
		}
	}
}

func TestMCPHTTPTransport(t *testing.T) {
	handler := NewHandler(nil, nil)
	server := NewServer(handler, "secret-key")

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	// 1. GET /mcp
	getReq := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Errorf("expected GET /mcp status 200, got %d", getRec.Code)
	}

	// 2. Unauthorized POST
	postPayload := []byte(`{"jsonrpc": "2.0", "id": 1, "method": "ping"}`)
	postReq := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(postPayload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)

	if postRec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", postRec.Code)
	}

	// 3. Authorized POST
	postReqAuth := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(postPayload))
	postReqAuth.Header.Set("Authorization", "Bearer secret-key")
	postRecAuth := httptest.NewRecorder()
	mux.ServeHTTP(postRecAuth, postReqAuth)

	if postRecAuth.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", postRecAuth.Code)
	}

	var jsonResp Response
	if err := json.Unmarshal(postRecAuth.Body.Bytes(), &jsonResp); err != nil {
		t.Fatalf("failed unmarshaling json response: %v", err)
	}
	if jsonResp.Error != nil {
		t.Errorf("unexpected error in ping response: %v", jsonResp.Error)
	}
}
