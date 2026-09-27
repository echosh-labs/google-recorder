package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"

	"github.com/justin/google-recorder-api/internal/mcp"
	"github.com/justin/google-recorder-api/pkg/recorder"
)

type Server struct {
	client      *recorder.Client
	syncManager *recorder.SyncManager
	mcpServer   *mcp.Server
}

func NewServer(client *recorder.Client, syncManager *recorder.SyncManager, mcpServer *mcp.Server) *Server {
	if syncManager == nil {
		syncManager = recorder.NewSyncManager(client)
	}
	if mcpServer == nil {
		mcpHandler := mcp.NewHandler(client, syncManager)
		mcpServer = mcp.NewServer(mcpHandler, "")
	}
	return &Server{
		client:      client,
		syncManager: syncManager,
		mcpServer:   mcpServer,
	}
}

// RegisterRoutes sets up all REST API routes and MCP endpoints on the provided mux.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Health & Authentication
	mux.HandleFunc("/api/v1/health", s.handleHealth)
	mux.HandleFunc("/api/v1/auth/status", s.handleAuthStatus)
	mux.HandleFunc("/api/v1/auth/refresh", s.handleAuthRefresh)
	mux.HandleFunc("/api/v1/auth/login", s.handleAuthLogin)
	mux.HandleFunc("/api/v1/auth/credentials", s.handleAuthCredentials)

	// Recordings
	mux.HandleFunc("/api/v1/recordings", s.handleRecordings)
	mux.HandleFunc("/api/v1/recordings/", s.handleRecordingSubresources)

	// Library Synchronization & Download Management
	mux.HandleFunc("/api/v1/sync", s.handleSync)
	mux.HandleFunc("/api/v1/sync/status", s.handleSyncStatus)
	mux.HandleFunc("/api/v1/sync/cancel", s.handleSyncCancel)

	// MCP Endpoint (Streamable HTTP JSON-RPC 2.0)
	s.mcpServer.RegisterRoutes(mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	session := s.client.Session()
	hasAuth := session != nil && session.IsValid()

	respondJSON(w, http.StatusOK, map[string]any{
		"status":     "healthy",
		"service":    "google-recorder-api",
		"auth_valid": hasAuth,
		"features": []string{
			"rest_api",
			"streaming_audio",
			"transcripts",
			"library_sync",
			"mcp_server",
		},
	})
}

// handleAuthStatus checks and returns current session status.
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session := s.client.Session()
	if session == nil {
		respondJSON(w, http.StatusOK, map[string]any{
			"authenticated": false,
			"error":         "no session initialized",
		})
		return
	}

	cfg := session.GetConfig()
	hasSAPISID := cfg.SAPISID != ""

	// Quick check against RPC if SAPISID is present
	testErr := session.TestAuth(r.Context())
	testOK := testErr == nil
	testErrMsg := ""
	if testErr != nil {
		testErrMsg = testErr.Error()
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"authenticated": hasSAPISID && testOK,
		"has_sapisid":   hasSAPISID,
		"test_ok":       testOK,
		"test_error":    testErrMsg,
		"auth_user":     cfg.AuthUser,
		"saved_at":      cfg.SavedAt,
		"auth_path":     session.GetAuthPath(),
	})
}

// handleAuthRefresh triggers automatic browser cookie extraction and test.
func (s *Server) handleAuthRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session := s.client.Session()
	if session == nil {
		respondError(w, http.StatusInternalServerError, "Session not initialized")
		return
	}

	refreshed, err := session.Refresh()
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	testErr := session.TestAuth(r.Context())
	respondJSON(w, http.StatusOK, map[string]any{
		"status":        "refreshed",
		"authenticated": testErr == nil,
		"auth_user":     refreshed.AuthUser,
		"saved_at":      refreshed.SavedAt,
		"test_ok":       testErr == nil,
	})
}

// handleAuthLogin launches the system browser to the Google Recorder login page.
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session := s.client.Session()
	authUser := 0
	if session != nil {
		authUser = session.GetConfig().AuthUser
	}

	targetURL := "https://recorder.google.com/?authuser=" + strconv.Itoa(authUser)

	go func() {
		for _, b := range []string{"snap run firefox", "firefox", "xdg-open", "google-chrome"} {
			parts := strings.Fields(b)
			parts = append(parts, targetURL)
			cmd := exec.Command(parts[0], parts[1:]...)
			if err := cmd.Start(); err == nil {
				return
			}
		}
	}()

	respondJSON(w, http.StatusOK, map[string]any{
		"status": "browser_launched",
		"url":    targetURL,
	})
}

// handleAuthCredentials allows manually setting cookies or auth parameters.
func (s *Server) handleAuthCredentials(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Cookies  string `json:"cookies"`
		AuthUser *int   `json:"authUser"`
		APIKey   string `json:"apiKey"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	session := s.client.Session()
	if session == nil {
		respondError(w, http.StatusInternalServerError, "Session not initialized")
		return
	}

	curr := session.GetConfig()
	if req.Cookies != "" {
		curr.Cookies = req.Cookies
		for _, part := range strings.Split(req.Cookies, ";") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "SAPISID=") {
				curr.SAPISID = strings.TrimPrefix(part, "SAPISID=")
				break
			}
		}
	}
	if req.AuthUser != nil {
		curr.AuthUser = *req.AuthUser
	}
	if req.APIKey != "" {
		curr.APIKey = req.APIKey
	}

	session.UpdateConfig(curr)

	testErr := session.TestAuth(r.Context())
	respondJSON(w, http.StatusOK, map[string]any{
		"status":        "updated",
		"authenticated": testErr == nil,
		"auth_user":     curr.AuthUser,
		"test_ok":       testErr == nil,
	})
}

// handleRecordings handles GET /api/v1/recordings?limit=50&oldest=true&query=meeting&all=true
func (s *Server) handleRecordings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if val, err := strconv.Atoi(lStr); err == nil && val > 0 {
			limit = val
		}
	}

	oldestFirst := r.URL.Query().Get("oldest") == "true"
	fetchAll := r.URL.Query().Get("all") == "true"
	query := r.URL.Query().Get("query")

	opts := recorder.ListOptions{
		Limit:       limit,
		OldestFirst: oldestFirst,
		All:         fetchAll,
		Query:       query,
	}

	recordings, err := s.client.ListRecordings(r.Context(), opts)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"count":      len(recordings),
		"recordings": recordings,
	})
}

// handleRecordingSubresources handles:
// GET  /api/v1/recordings/{id}
// GET  /api/v1/recordings/{id}/transcript
// GET  /api/v1/recordings/{id}/audio
// POST /api/v1/recordings/{id}/download
// GET  /api/v1/recordings/{id}/export/audio
// GET  /api/v1/recordings/{id}/export/transcript
func (s *Server) handleRecordingSubresources(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/recordings/")
	parts := strings.Split(path, "/")

	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}

	recordingID := parts[0]

	// 1. Single recording metadata: GET /api/v1/recordings/{id}
	if len(parts) == 1 {
		if r.Method == http.MethodGet {
			rec, err := s.client.GetRecordingInfo(r.Context(), recordingID)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err.Error())
				return
			}
			respondJSON(w, http.StatusOK, rec)
			return
		}
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sub := parts[1]
	switch sub {
	case "transcript":
		transcript, err := s.client.GetTranscript(r.Context(), recordingID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err.Error())
			return
		}

		format := r.URL.Query().Get("format")
		if format == "text" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write([]byte(transcript.FullText))
			return
		}

		respondJSON(w, http.StatusOK, transcript)

	case "audio":
		rangeHeader := r.Header.Get("Range")
		_, _ = s.client.StreamAudio(r.Context(), recordingID, rangeHeader, w)
		return

	case "download":
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleSingleDownload(w, r, recordingID)

	case "export":
		if len(parts) < 3 {
			http.NotFound(w, r)
			return
		}
		exportTarget := parts[2]
		switch exportTarget {
		case "audio":
			s.handleExportAudio(w, r, recordingID)
		case "transcript":
			s.handleExportTranscript(w, r, recordingID)
		default:
			http.NotFound(w, r)
		}

	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleSingleDownload(w http.ResponseWriter, r *http.Request, recordingID string) {
	var req struct {
		OutputDir          string `json:"output_dir"`
		DownloadAudio      *bool  `json:"download_audio"`
		DownloadTranscript *bool  `json:"download_transcript"`
		TranscriptFormat   string `json:"transcript_format"`
		DownloadMetadata   *bool  `json:"download_metadata"`
		Force              bool   `json:"force"`
	}

	_ = json.NewDecoder(r.Body).Decode(&req)

	rec, err := s.client.GetRecordingInfo(r.Context(), recordingID)
	if err != nil {
		respondError(w, http.StatusNotFound, fmt.Sprintf("recording %s not found: %v", recordingID, err))
		return
	}

	outDir := req.OutputDir
	if outDir == "" {
		outDir = "./recordings"
	}
	dlAudio := true
	if req.DownloadAudio != nil {
		dlAudio = *req.DownloadAudio
	}
	dlTranscript := true
	if req.DownloadTranscript != nil {
		dlTranscript = *req.DownloadTranscript
	}
	transFormat := "both"
	if req.TranscriptFormat != "" {
		transFormat = req.TranscriptFormat
	}
	dlMeta := true
	if req.DownloadMetadata != nil {
		dlMeta = *req.DownloadMetadata
	}

	opts := recorder.DownloadOptions{
		OutputDir:          outDir,
		DownloadAudio:      dlAudio,
		DownloadTranscript: dlTranscript,
		TranscriptFormat:   transFormat,
		DownloadMetadata:   dlMeta,
		Force:              req.Force,
	}

	item, err := s.client.DownloadRecording(r.Context(), *rec, opts)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, item)
}

func (s *Server) handleExportAudio(w http.ResponseWriter, r *http.Request, recordingID string) {
	rec, err := s.client.GetRecordingInfo(r.Context(), recordingID)
	baseName := recordingID
	if err == nil && rec != nil {
		baseName = recorder.FormatRecordingBaseName(*rec)
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.m4a\"", baseName))
	_, _ = s.client.StreamAudio(r.Context(), recordingID, "", w)
}

func (s *Server) handleExportTranscript(w http.ResponseWriter, r *http.Request, recordingID string) {
	transcript, err := s.client.GetTranscript(r.Context(), recordingID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	rec, _ := s.client.GetRecordingInfo(r.Context(), recordingID)
	baseName := recordingID
	if rec != nil {
		baseName = recorder.FormatRecordingBaseName(*rec)
	}

	format := r.URL.Query().Get("format")
	if format == "json" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.json\"", baseName))
		_ = json.NewEncoder(w).Encode(transcript)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.txt\"", baseName))
	w.Write([]byte(transcript.FullText))
}

// handleSync handles POST /api/v1/sync
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		OutputDir          string `json:"output_dir"`
		DownloadAudio      *bool  `json:"download_audio"`
		DownloadTranscript *bool  `json:"download_transcript"`
		TranscriptFormat   string `json:"transcript_format"`
		DownloadMetadata   *bool  `json:"download_metadata"`
		Concurrency        int    `json:"concurrency"`
		Limit              int    `json:"limit"`
		All                *bool  `json:"all"`
		Query              string `json:"query"`
		OldestFirst        bool   `json:"oldest_first"`
		Force              bool   `json:"force"`
		Async              bool   `json:"async"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		respondError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	outDir := req.OutputDir
	if outDir == "" {
		outDir = "./recordings"
	}
	dlAudio := true
	if req.DownloadAudio != nil {
		dlAudio = *req.DownloadAudio
	}
	dlTranscript := true
	if req.DownloadTranscript != nil {
		dlTranscript = *req.DownloadTranscript
	}
	transFormat := "both"
	if req.TranscriptFormat != "" {
		transFormat = req.TranscriptFormat
	}
	dlMeta := true
	if req.DownloadMetadata != nil {
		dlMeta = *req.DownloadMetadata
	}
	fetchAll := true
	if req.All != nil {
		fetchAll = *req.All
	}
	concurrency := req.Concurrency
	if concurrency <= 0 {
		concurrency = 3
	}

	opts := recorder.SyncOptions{
		OutputDir:          outDir,
		DownloadAudio:      dlAudio,
		DownloadTranscript: dlTranscript,
		TranscriptFormat:   transFormat,
		DownloadMetadata:   dlMeta,
		Concurrency:        concurrency,
		Limit:              req.Limit,
		All:                fetchAll,
		Query:              req.Query,
		OldestFirst:        req.OldestFirst,
		Force:              req.Force,
	}

	if req.Async {
		jobID, err := s.syncManager.StartAsync(r.Context(), opts)
		if err != nil {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondJSON(w, http.StatusAccepted, map[string]any{
			"status":  "running",
			"job_id":  jobID,
			"message": "Library synchronization started in background",
		})
		return
	}

	report, err := s.syncManager.RunSync(r.Context(), opts)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, report)
}

// handleSyncStatus handles GET /api/v1/sync/status
func (s *Server) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status := s.syncManager.GetStatus()
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir = status.OutputDir
	}

	res := map[string]any{
		"job": status,
	}

	if dir != "" {
		if manifest, err := recorder.LoadManifest(dir); err == nil {
			res["manifest"] = map[string]any{
				"output_dir":       dir,
				"version":          manifest.Version,
				"last_sync":        manifest.LastSync,
				"total_recordings": len(manifest.Recordings),
			}
		}
	}

	respondJSON(w, http.StatusOK, res)
}

// handleSyncCancel handles POST /api/v1/sync/cancel
func (s *Server) handleSyncCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cancelled := s.syncManager.Cancel()
	respondJSON(w, http.StatusOK, map[string]any{
		"cancelled": cancelled,
		"status":    "cancelling",
	})
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{
		"error": message,
	})
}
