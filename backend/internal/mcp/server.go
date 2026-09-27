package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

const maxRequestSize = 10 << 20 // 10 MB

// Server provides HTTP and Stdio transports for the Google Recorder MCP server.
type Server struct {
	handler *Handler
	apiKey  string
}

// NewServer creates an MCP server with the given handler and optional API key.
func NewServer(handler *Handler, apiKey string) *Server {
	return &Server{
		handler: handler,
		apiKey:  apiKey,
	}
}

// RegisterRoutes registers the Streamable HTTP MCP endpoint on the provided mux.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/mcp", s.handleMCP)
}

// handleMCP serves the Streamable HTTP transport endpoint for MCP JSON-RPC.
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	// Enable CORS for web-based MCP inspectors/clients
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method == http.MethodGet {
		// Provide health / discovery info for GET /mcp
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"service":          "google-recorder-mcp",
			"protocol_version": ProtocolVersion,
			"transport":        "streamable-http",
			"endpoint":         "/mcp",
		})
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, GET, OPTIONS")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !s.authenticate(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestSize))
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	if len(body) == 0 {
		http.Error(w, "Empty request body", http.StatusBadRequest)
		return
	}

	resp := s.handler.HandleRequest(r.Context(), body)
	if resp == nil {
		// Notification without response
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("MCP HTTP transport error encoding response: %v", err)
	}
}

func (s *Server) authenticate(r *http.Request) bool {
	if s.apiKey == "" {
		return true
	}
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return false
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	return strings.TrimPrefix(auth, prefix) == s.apiKey
}

// RunStdio starts a standard I/O JSON-RPC loop for command-line agent integration.
// All responses are sent to stdout; all logs are strictly directed to stderr.
func (s *Server) RunStdio(ctx context.Context) error {
	logger := log.New(os.Stderr, "[MCP Stdio] ", log.LstdFlags)
	logger.Printf("Starting Google Recorder MCP server in Stdio mode...")

	scanner := bufio.NewScanner(os.Stdin)
	// Allow large tokens (e.g. large transcripts or payloads up to 10MB)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, maxRequestSize)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		resp := s.handler.HandleRequest(ctx, line)
		if resp == nil {
			continue // Notification
		}

		respBytes, err := json.Marshal(resp)
		if err != nil {
			logger.Printf("Error serializing response: %v", err)
			continue
		}

		// Write to Stdout with newline
		fmt.Printf("%s\n", string(respBytes))
	}

	if err := scanner.Err(); err != nil {
		logger.Printf("Scanner terminated with error: %v", err)
		return err
	}

	logger.Printf("Stdio loop ended.")
	return nil
}
