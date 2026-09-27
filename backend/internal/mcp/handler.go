package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/justin/google-recorder-api/pkg/recorder"
)

// Handler processes MCP JSON-RPC 2.0 requests.
type Handler struct {
	client      *recorder.Client
	syncManager *recorder.SyncManager
}

// NewHandler creates a new MCP request handler.
func NewHandler(client *recorder.Client, syncManager *recorder.SyncManager) *Handler {
	return &Handler{
		client:      client,
		syncManager: syncManager,
	}
}

// HandleRequest processes an incoming JSON-RPC 2.0 request.
func (h *Handler) HandleRequest(ctx context.Context, raw []byte) *Response {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   &Error{Code: ErrCodeParse, Message: "Parse error: invalid JSON"},
		}
	}

	resp := &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
	}

	switch req.Method {
	case "initialize":
		resp.Result = h.handleInitialize(req.Params)

	case "notifications/initialized":
		return nil

	case "ping":
		resp.Result = map[string]any{}

	case "tools/list":
		resp.Result = h.handleToolsList()

	case "tools/call":
		result, err := h.handleToolsCall(ctx, req.Params)
		if err != nil {
			resp.Result = &ToolResult{
				Content: []ToolContent{{Type: "text", Text: err.Error()}},
				IsError: true,
			}
		} else {
			resp.Result = result
		}

	case "resources/list":
		resList, err := h.handleResourcesList(ctx)
		if err != nil {
			resp.Error = &Error{Code: ErrCodeInternal, Message: err.Error()}
		} else {
			resp.Result = resList
		}

	case "resources/read":
		resContent, err := h.handleResourcesRead(ctx, req.Params)
		if err != nil {
			resp.Error = &Error{Code: ErrCodeInternal, Message: err.Error()}
		} else {
			resp.Result = resContent
		}

	default:
		resp.Error = &Error{
			Code:    ErrCodeNoMethod,
			Message: fmt.Sprintf("Method not found: %s", req.Method),
		}
	}

	return resp
}

func (h *Handler) handleInitialize(_ any) map[string]any {
	return map[string]any{
		"protocolVersion": ProtocolVersion,
		"serverInfo": ServerInfo{
			Name:    "google-recorder",
			Version: "1.0.0",
		},
		"capabilities": Capabilities{
			Tools:     &ToolCapability{ListChanged: false},
			Resources: &ResourceCapability{ListChanged: false},
		},
	}
}

func (h *Handler) handleToolsList() map[string]any {
	tools := []Tool{
		{
			Name:        "list_recordings",
			Description: "List Google recordings with metadata, duration, timestamps, and IDs.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of recordings to return (default: 20)",
					},
					"query": map[string]any{
						"type":        "string",
						"description": "Optional search term to filter recording titles",
					},
					"all": map[string]any{
						"type":        "boolean",
						"description": "If true, page through complete recording history",
					},
					"oldest_first": map[string]any{
						"type":        "boolean",
						"description": "If true, return in chronological order (oldest first)",
					},
				},
			},
		},
		{
			Name:        "get_recording",
			Description: "Get detailed metadata for a specific Google recording ID.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"recording_id": map[string]any{
						"type":        "string",
						"description": "The unique UUID of the recording",
					},
				},
				"required": []string{"recording_id"},
			},
		},
		{
			Name:        "get_transcript",
			Description: "Get the full text or speaker-diarized JSON transcript of a recording.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"recording_id": map[string]any{
						"type":        "string",
						"description": "The unique UUID of the recording",
					},
					"format": map[string]any{
						"type":        "string",
						"enum":        []string{"text", "json"},
						"description": "Transcript format: 'text' (human-readable with speakers) or 'json' (raw segments with ms timestamps)",
					},
				},
				"required": []string{"recording_id"},
			},
		},
		{
			Name:        "download_recording",
			Description: "Download an individual Google recording's M4A audio and/or transcripts (.txt and .json) side-by-side into a local directory.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"recording_id": map[string]any{
						"type":        "string",
						"description": "The unique UUID of the recording to download",
					},
					"output_dir": map[string]any{
						"type":        "string",
						"description": "Destination directory path on the local filesystem (default: ./recordings)",
					},
					"download_audio": map[string]any{
						"type":        "boolean",
						"description": "Whether to download the .m4a audio file (default: true)",
					},
					"download_transcript": map[string]any{
						"type":        "boolean",
						"description": "Whether to download the transcript file (default: true)",
					},
					"transcript_format": map[string]any{
						"type":        "string",
						"enum":        []string{"text", "json", "both"},
						"description": "Transcript file format to save (default: 'both')",
					},
					"download_metadata": map[string]any{
						"type":        "boolean",
						"description": "Whether to download full JSON metadata and word segments (default: true)",
					},
					"force": map[string]any{
						"type":        "boolean",
						"description": "Whether to overwrite and re-download existing files",
					},
				},
				"required": []string{"recording_id"},
			},
		},
		{
			Name:        "sync_recordings",
			Description: "Synchronize recordings from Google Recorder to a local directory. Downloads M4A audio and transcripts side-by-side (not a zip file), skipping already downloaded recordings using an incremental manifest.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"output_dir": map[string]any{
						"type":        "string",
						"description": "Target local directory where files will be stored",
					},
					"download_audio": map[string]any{
						"type":        "boolean",
						"description": "Download .m4a audio files (default: true)",
					},
					"download_transcript": map[string]any{
						"type":        "boolean",
						"description": "Download formatted .txt transcripts (default: true)",
					},
					"transcript_format": map[string]any{
						"type":        "string",
						"enum":        []string{"text", "json", "both"},
						"description": "Transcript format to export (default: 'both')",
					},
					"download_metadata": map[string]any{
						"type":        "boolean",
						"description": "Export complete JSON metadata and word timings (default: true)",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of recordings to sync (0 for unlimited)",
					},
					"all": map[string]any{
						"type":        "boolean",
						"description": "Sync entire historical library back to 2020 (default: true)",
					},
					"query": map[string]any{
						"type":        "string",
						"description": "Optional keyword filter to match against titles",
					},
					"force": map[string]any{
						"type":        "boolean",
						"description": "Re-download already synced items",
					},
				},
				"required": []string{"output_dir"},
			},
		},
		{
			Name:        "get_sync_status",
			Description: "Check current/previous synchronization status and inspect the local manifest.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"output_dir": map[string]any{
						"type":        "string",
						"description": "Optional output directory to inspect manifest file counts",
					},
				},
			},
		},
		{
			Name:        "auth_status",
			Description: "Check current Google Recorder authentication status, cookie validity, and active Google account index.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "auth_refresh",
			Description: "Trigger automatic re-extraction of fresh Google Recorder cookies from local browser profiles (Firefox / Chrome).",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}

	return map[string]any{
		"tools": tools,
	}
}

func (h *Handler) handleToolsCall(ctx context.Context, params any) (*ToolResult, error) {
	var call struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}

	paramBytes, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed encoding tool call params: %w", err)
	}
	if err := json.Unmarshal(paramBytes, &call); err != nil {
		return nil, fmt.Errorf("invalid tool call structure: %w", err)
	}

	args := call.Arguments
	if args == nil {
		args = make(map[string]any)
	}

	switch call.Name {
	case "list_recordings":
		limit := 20
		if l, ok := args["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}
		query, _ := args["query"].(string)
		all, _ := args["all"].(bool)
		oldestFirst, _ := args["oldest_first"].(bool)

		opts := recorder.ListOptions{
			Limit:       limit,
			Query:       query,
			All:         all,
			OldestFirst: oldestFirst,
		}

		recs, err := h.client.ListRecordings(ctx, opts)
		if err != nil {
			return nil, err
		}

		data, _ := json.MarshalIndent(recs, "", "  ")
		return &ToolResult{
			Content: []ToolContent{{
				Type: "text",
				Text: fmt.Sprintf("Found %d recordings:\n\n%s", len(recs), string(data)),
			}},
		}, nil

	case "get_recording":
		id, _ := args["recording_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("recording_id is required")
		}

		rec, err := h.client.GetRecordingInfo(ctx, id)
		if err != nil {
			return nil, err
		}

		data, _ := json.MarshalIndent(rec, "", "  ")
		return &ToolResult{
			Content: []ToolContent{{Type: "text", Text: string(data)}},
		}, nil

	case "get_transcript":
		id, _ := args["recording_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("recording_id is required")
		}
		format, _ := args["format"].(string)
		if format == "" {
			format = "text"
		}

		transcript, err := h.client.GetTranscript(ctx, id)
		if err != nil {
			return nil, err
		}

		if format == "json" {
			data, _ := json.MarshalIndent(transcript, "", "  ")
			return &ToolResult{
				Content: []ToolContent{{Type: "text", Text: string(data)}},
			}, nil
		}

		return &ToolResult{
			Content: []ToolContent{{Type: "text", Text: transcript.FullText}},
		}, nil

	case "download_recording":
		id, _ := args["recording_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("recording_id is required")
		}

		outputDir, _ := args["output_dir"].(string)
		if outputDir == "" {
			outputDir = "./recordings"
		}

		dlAudio := true
		if val, ok := args["download_audio"].(bool); ok {
			dlAudio = val
		}

		dlTranscript := true
		if val, ok := args["download_transcript"].(bool); ok {
			dlTranscript = val
		}

		transFormat := "both"
		if val, ok := args["transcript_format"].(string); ok && val != "" {
			transFormat = val
		}

		dlMeta := true
		if val, ok := args["download_metadata"].(bool); ok {
			dlMeta = val
		}

		force, _ := args["force"].(bool)

		rec, err := h.client.GetRecordingInfo(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("could not fetch metadata for %s: %w", id, err)
		}

		opts := recorder.DownloadOptions{
			OutputDir:          outputDir,
			DownloadAudio:      dlAudio,
			DownloadTranscript: dlTranscript,
			TranscriptFormat:   transFormat,
			DownloadMetadata:   dlMeta,
			Force:              force,
		}

		item, err := h.client.DownloadRecording(ctx, *rec, opts)
		if err != nil {
			return nil, err
		}

		resData, _ := json.MarshalIndent(item, "", "  ")
		return &ToolResult{
			Content: []ToolContent{{
				Type: "text",
				Text: fmt.Sprintf("Download result for %s (%s):\n\n%s", rec.Title, rec.ID, string(resData)),
			}},
		}, nil

	case "sync_recordings":
		outputDir, _ := args["output_dir"].(string)
		if outputDir == "" {
			return nil, fmt.Errorf("output_dir is required")
		}

		dlAudio := true
		if val, ok := args["download_audio"].(bool); ok {
			dlAudio = val
		}

		dlTranscript := true
		if val, ok := args["download_transcript"].(bool); ok {
			dlTranscript = val
		}

		transFormat := "both"
		if val, ok := args["transcript_format"].(string); ok && val != "" {
			transFormat = val
		}

		dlMeta := true
		if val, ok := args["download_metadata"].(bool); ok {
			dlMeta = val
		}

		all := true
		if val, ok := args["all"].(bool); ok {
			all = val
		}

		limit := 0
		if l, ok := args["limit"].(float64); ok {
			limit = int(l)
		}

		query, _ := args["query"].(string)
		force, _ := args["force"].(bool)

		opts := recorder.SyncOptions{
			OutputDir:          outputDir,
			DownloadAudio:      dlAudio,
			DownloadTranscript: dlTranscript,
			TranscriptFormat:   transFormat,
			DownloadMetadata:   dlMeta,
			Limit:              limit,
			All:                all,
			Query:              query,
			Force:              force,
		}

		report, err := h.syncManager.RunSync(ctx, opts)
		if err != nil {
			return nil, err
		}

		reportBytes, _ := json.MarshalIndent(report, "", "  ")
		return &ToolResult{
			Content: []ToolContent{{
				Type: "text",
				Text: fmt.Sprintf("Sync completed successfully:\n- Output Dir: %s\n- Total: %d\n- Downloaded: %d\n- Skipped: %d\n- Failed: %d\n- Duration: %.2fs\n\nFull Report:\n%s",
					report.OutputDir, report.TotalFound, report.Downloaded, report.Skipped, report.Failed, report.DurationSeconds, string(reportBytes)),
			}},
		}, nil

	case "get_sync_status":
		status := h.syncManager.GetStatus()
		dir, _ := args["output_dir"].(string)

		res := map[string]any{
			"job_status": status,
		}

		if dir != "" {
			if manifest, err := recorder.LoadManifest(dir); err == nil {
				res["manifest_counts"] = map[string]any{
					"total_entries": len(manifest.Recordings),
					"last_sync":     manifest.LastSync,
					"output_dir":    dir,
				}
			}
		}

		data, _ := json.MarshalIndent(res, "", "  ")
		return &ToolResult{
			Content: []ToolContent{{Type: "text", Text: string(data)}},
		}, nil

	case "auth_status":
		session := h.client.Session()
		if session == nil {
			return &ToolResult{
				Content: []ToolContent{{Type: "text", Text: "No session initialized"}},
				IsError: true,
			}, nil
		}

		cfg := session.GetConfig()
		testErr := session.TestAuth(ctx)
		testOK := testErr == nil
		testErrMsg := ""
		if testErr != nil {
			testErrMsg = testErr.Error()
		}

		res := map[string]any{
			"authenticated": cfg.SAPISID != "" && testOK,
			"has_sapisid":   cfg.SAPISID != "",
			"test_ok":       testOK,
			"test_error":    testErrMsg,
			"auth_user":     cfg.AuthUser,
			"saved_at":      cfg.SavedAt,
			"auth_path":     session.GetAuthPath(),
		}
		data, _ := json.MarshalIndent(res, "", "  ")
		return &ToolResult{
			Content: []ToolContent{{Type: "text", Text: string(data)}},
		}, nil

	case "auth_refresh":
		session := h.client.Session()
		if session == nil {
			return nil, fmt.Errorf("no session initialized")
		}

		refreshed, err := session.Refresh()
		if err != nil {
			return nil, err
		}

		testErr := session.TestAuth(ctx)
		res := map[string]any{
			"status":        "refreshed",
			"authenticated": testErr == nil,
			"auth_user":     refreshed.AuthUser,
			"saved_at":      refreshed.SavedAt,
			"test_ok":       testErr == nil,
		}
		data, _ := json.MarshalIndent(res, "", "  ")
		return &ToolResult{
			Content: []ToolContent{{Type: "text", Text: string(data)}},
		}, nil

	default:
		return nil, fmt.Errorf("unknown tool: %s", call.Name)
	}
}

func (h *Handler) handleResourcesList(ctx context.Context) (map[string]any, error) {
	recs, err := h.client.ListRecordings(ctx, recorder.ListOptions{Limit: 25})
	if err != nil {
		return nil, err
	}

	var resources []Resource
	for _, r := range recs {
		resources = append(resources, Resource{
			URI:         fmt.Sprintf("recorder://recordings/%s", r.ID),
			Name:        fmt.Sprintf("%s (%s)", r.Title, r.Duration),
			Description: fmt.Sprintf("Recorded at %s", r.RecordedAt.Format("2006-01-02 15:04:05")),
			MimeType:    "application/json",
		})
		resources = append(resources, Resource{
			URI:         fmt.Sprintf("recorder://transcripts/%s", r.ID),
			Name:        fmt.Sprintf("Transcript: %s", r.Title),
			Description: fmt.Sprintf("Full text transcript for %s", r.ID),
			MimeType:    "text/plain",
		})
	}

	return map[string]any{
		"resources": resources,
	}, nil
}

func (h *Handler) handleResourcesRead(ctx context.Context, params any) (map[string]any, error) {
	var req struct {
		URI string `json:"uri"`
	}
	pBytes, _ := json.Marshal(params)
	_ = json.Unmarshal(pBytes, &req)

	if req.URI == "" {
		return nil, fmt.Errorf("uri is required")
	}

	if strings.HasPrefix(req.URI, "recorder://recordings/") {
		id := strings.TrimPrefix(req.URI, "recorder://recordings/")
		rec, err := h.client.GetRecordingInfo(ctx, id)
		if err != nil {
			return nil, err
		}
		data, _ := json.MarshalIndent(rec, "", "  ")
		return map[string]any{
			"contents": []ResourceContent{{
				URI:      req.URI,
				MimeType: "application/json",
				Text:     string(data),
			}},
		}, nil
	}

	if strings.HasPrefix(req.URI, "recorder://transcripts/") {
		id := strings.TrimPrefix(req.URI, "recorder://transcripts/")
		t, err := h.client.GetTranscript(ctx, id)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"contents": []ResourceContent{{
				URI:      req.URI,
				MimeType: "text/plain",
				Text:     t.FullText,
			}},
		}, nil
	}

	return nil, fmt.Errorf("unsupported resource URI: %s", req.URI)
}
