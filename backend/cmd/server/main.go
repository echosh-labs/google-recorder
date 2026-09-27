package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/justin/google-recorder-api/internal/api"
	"github.com/justin/google-recorder-api/internal/mcp"
	"github.com/justin/google-recorder-api/pkg/recorder"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "auth":
			runAuthCLI(os.Args[2:])
			return
		case "mcp":
			runMCPCLI(os.Args[2:])
			return
		case "sync":
			runSyncCLI(os.Args[2:])
			return
		case "list":
			runListCLI(os.Args[2:])
			return
		case "download":
			runDownloadCLI(os.Args[2:])
			return
		case "server":
			runServer(os.Args[2:])
			return
		case "help", "--help", "-h":
			printHelp()
			return
		}
	}

	runServer(os.Args[1:])
}

func printHelp() {
	fmt.Println("Google Recorder Studio & MCP Service")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  recorder-server [flags]                  Start HTTP REST API & MCP server")
	fmt.Println("  recorder-server server [flags]           Start HTTP REST API & MCP server")
	fmt.Println("  recorder-server mcp [flags]              Run Stdio MCP server (Claude/Antigravity integration)")
	fmt.Println("  recorder-server sync [flags]             Synchronize recordings to local directory")
	fmt.Println("  recorder-server download <id> [flags]    Download individual recording files")
	fmt.Println("  recorder-server list [flags]             List recordings from Google Recorder")
	fmt.Println("  recorder-server auth [flags]             Inspect or refresh Google authentication")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  --port <port>       Port to run HTTP server on (default: 8080)")
	fmt.Println("  --auth <path>       Path to auth.json credentials (default: ~/.config/google-recorder/auth.json)")
	fmt.Println()
}

func initSession(authPath string, silent bool) (*recorder.Session, error) {
	session, err := recorder.LoadAuth(authPath)
	if err != nil {
		if !silent {
			log.Printf("⚠️  Authentication notice: %v", err)
			log.Printf("Server starting in setup mode. Authenticate via POST /api/v1/auth/refresh or the CLI.")
		}
		targetPath := authPath
		if targetPath == "" {
			targetPath, _ = recorder.DefaultAuthPath()
		}
		session, _ = recorder.LoadAuth(targetPath)
		if session == nil {
			_ = os.WriteFile(targetPath, []byte(`{"apiKey":"`+recorder.DefaultAPIKey+`"}`), 0o600)
			session, _ = recorder.LoadAuth(targetPath)
		}
		return session, err
	}
	return session, nil
}

func runServer(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	port := fs.Int("port", 8080, "Port to run the HTTP API server on")
	authPath := fs.String("auth", "", "Path to auth.json credentials (default: ~/.config/google-recorder/auth.json)")
	checkAuth := fs.Bool("check-auth", false, "Test authentication and exit")
	refreshAuth := fs.Bool("refresh-auth", false, "Refresh credentials from browser and exit")
	_ = fs.Parse(args)

	if *refreshAuth {
		runAuthCLI([]string{"--refresh"})
		return
	}
	if *checkAuth {
		runAuthCLI([]string{"--check"})
		return
	}

	log.Printf("Starting Google Recorder API & MCP service...")

	session, err := initSession(*authPath, false)
	if err == nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if testErr := session.TestAuth(ctx); testErr != nil {
				log.Printf("⚠️  Session test warning: %v. Auto-refresh will attempt to recover on next request.", testErr)
			} else {
				cfg := session.GetConfig()
				log.Printf("✅ Authentication active (Google Account index: %d)", cfg.AuthUser)
			}
		}()
	}

	client := recorder.NewClient(session)
	syncManager := recorder.NewSyncManager(client)
	mcpHandler := mcp.NewHandler(client, syncManager)
	mcpServer := mcp.NewServer(mcpHandler, "")

	server := api.NewServer(client, syncManager, mcpServer)

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	// Auto port selection
	var listener net.Listener
	currentPort := *port
	const maxPortAttempts = 100

	for i := 0; i < maxPortAttempts; i++ {
		addr := fmt.Sprintf("0.0.0.0:%d", currentPort)
		l, err := net.Listen("tcp", addr)
		if err == nil {
			listener = l
			break
		}
		log.Printf("Port %d is already in use, trying port %d...", currentPort, currentPort+1)
		currentPort++
	}

	if listener == nil {
		log.Fatalf("Failed to bind to an available port after trying %d consecutive ports starting from %d", maxPortAttempts, *port)
	}

	httpServer := &http.Server{
		Handler:      loggingMiddleware(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 300 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("Google Recorder API listening on http://localhost:%d", currentPort)
		log.Printf("REST Endpoints:")
		log.Printf("  GET  http://localhost:%d/api/v1/health", currentPort)
		log.Printf("  GET  http://localhost:%d/api/v1/auth/status", currentPort)
		log.Printf("  POST http://localhost:%d/api/v1/auth/refresh", currentPort)
		log.Printf("  GET  http://localhost:%d/api/v1/recordings?all=true", currentPort)
		log.Printf("  GET  http://localhost:%d/api/v1/recordings/{id}", currentPort)
		log.Printf("  GET  http://localhost:%d/api/v1/recordings/{id}/transcript?format=text|json", currentPort)
		log.Printf("  GET  http://localhost:%d/api/v1/recordings/{id}/audio", currentPort)
		log.Printf("  POST http://localhost:%d/api/v1/recordings/{id}/download", currentPort)
		log.Printf("  GET  http://localhost:%d/api/v1/recordings/{id}/export/audio", currentPort)
		log.Printf("  GET  http://localhost:%d/api/v1/recordings/{id}/export/transcript", currentPort)
		log.Printf("  POST http://localhost:%d/api/v1/sync", currentPort)
		log.Printf("  GET  http://localhost:%d/api/v1/sync/status", currentPort)
		log.Printf("MCP Endpoints:")
		log.Printf("  POST http://localhost:%d/mcp (Streamable HTTP JSON-RPC 2.0)", currentPort)

		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server listen error: %v", err)
		}
	}()

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, os.Interrupt, syscall.SIGTERM)
	<-stopCh

	log.Println("Shutting down API server gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalf("Server shutdown failed: %v", err)
	}
	log.Println("Server gracefully stopped.")
}

func runMCPCLI(args []string) {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	authPath := fs.String("auth", "", "Path to auth.json credentials")
	_ = fs.Parse(args)

	// All diagnostic logging in MCP Stdio mode MUST go to stderr
	logger := log.New(os.Stderr, "[Google Recorder MCP] ", log.LstdFlags)

	session, err := initSession(*authPath, true)
	if err != nil {
		logger.Printf("Warning loading auth: %v", err)
	}

	client := recorder.NewClient(session)
	syncManager := recorder.NewSyncManager(client)
	mcpHandler := mcp.NewHandler(client, syncManager)
	mcpServer := mcp.NewServer(mcpHandler, "")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := mcpServer.RunStdio(ctx); err != nil {
		logger.Fatalf("MCP stdio loop exited: %v", err)
	}
}

func runSyncCLI(args []string) {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	dir := fs.String("dir", "./recordings", "Output directory for audio and transcript files")
	audio := fs.Bool("audio", true, "Download .m4a audio files")
	transcript := fs.Bool("transcript", true, "Download transcripts")
	format := fs.String("format", "both", "Transcript format: text, json, or both")
	meta := fs.Bool("meta", true, "Download JSON metadata and segments")
	all := fs.Bool("all", true, "Sync all historical recordings")
	limit := fs.Int("limit", 0, "Maximum number of recordings to sync (0 = unlimited)")
	query := fs.String("query", "", "Filter recordings by title keyword")
	force := fs.Bool("force", false, "Force re-download of already synced recordings")
	concurrency := fs.Int("concurrency", 3, "Number of concurrent downloads")
	authPath := fs.String("auth", "", "Path to auth.json credentials")
	_ = fs.Parse(args)

	session, err := recorder.LoadAuth(*authPath)
	if err != nil {
		log.Fatalf("Authentication required for sync: %v", err)
	}

	client := recorder.NewClient(session)

	fmt.Printf("=== Google Recorder Sync ===\n")
	fmt.Printf("Destination: %s\n", *dir)
	fmt.Printf("Audio: %v | Transcript: %v (%s) | Metadata: %v\n", *audio, *transcript, *format, *meta)
	fmt.Printf("Query: %q | Limit: %d | All: %v | Force: %v\n\n", *query, *limit, *all, *force)

	fetchAll := *all
	if *limit > 0 {
		fetchAll = false
	}

	opts := recorder.SyncOptions{
		OutputDir:          *dir,
		DownloadAudio:      *audio,
		DownloadTranscript: *transcript,
		TranscriptFormat:   *format,
		DownloadMetadata:   *meta,
		Concurrency:        *concurrency,
		Limit:              *limit,
		All:                fetchAll,
		Query:              *query,
		Force:              *force,
	}

	report, err := client.Sync(context.Background(), opts, func(curr, total int, item *recorder.SyncItem) {
		statusIcon := "✅"
		if item.Status == "skipped" {
			statusIcon = "⏭️"
		} else if item.Status == "error" {
			statusIcon = "❌"
		}
		fmt.Printf("[%d/%d] %s %s (%s) [%s]\n", curr, total, statusIcon, item.Title, item.RecordingID[:8], item.Status)
	})

	if err != nil {
		log.Fatalf("Sync failed: %v", err)
	}

	fmt.Println()
	fmt.Println("=== Sync Complete ===")
	fmt.Printf("Total Found: %d\n", report.TotalFound)
	fmt.Printf("Downloaded:  %d\n", report.Downloaded)
	fmt.Printf("Skipped:     %d\n", report.Skipped)
	fmt.Printf("Failed:      %d\n", report.Failed)
	fmt.Printf("Duration:    %.2fs\n", report.DurationSeconds)
	fmt.Printf("Manifest:    %s/manifest.json\n", report.OutputDir)
}

func runDownloadCLI(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: recorder-server download <recording-id> [--dir ./recordings] [--audio] [--transcript]")
		os.Exit(1)
	}

	recordingID := args[0]
	fs := flag.NewFlagSet("download", flag.ExitOnError)
	dir := fs.String("dir", "./recordings", "Output directory for downloaded files")
	audio := fs.Bool("audio", true, "Download audio")
	transcript := fs.Bool("transcript", true, "Download transcript")
	format := fs.String("format", "both", "Transcript format: text, json, both")
	meta := fs.Bool("meta", true, "Download metadata JSON")
	force := fs.Bool("force", false, "Force re-download")
	authPath := fs.String("auth", "", "Path to auth.json credentials")
	_ = fs.Parse(args[1:])

	session, err := recorder.LoadAuth(*authPath)
	if err != nil {
		log.Fatalf("Authentication required: %v", err)
	}

	client := recorder.NewClient(session)
	ctx := context.Background()

	rec, err := client.GetRecordingInfo(ctx, recordingID)
	if err != nil {
		log.Fatalf("Failed getting recording info: %v", err)
	}

	opts := recorder.DownloadOptions{
		OutputDir:          *dir,
		DownloadAudio:      *audio,
		DownloadTranscript: *transcript,
		TranscriptFormat:   *format,
		DownloadMetadata:   *meta,
		Force:              *force,
	}

	item, err := client.DownloadRecording(ctx, *rec, opts)
	if err != nil {
		log.Fatalf("Download failed: %v", err)
	}

	fmt.Printf("Recording downloaded successfully:\n")
	fmt.Printf("  ID:         %s\n", item.RecordingID)
	fmt.Printf("  Title:      %s\n", item.Title)
	if item.AudioPath != "" {
		fmt.Printf("  Audio:      %s (%d bytes)\n", item.AudioPath, item.AudioSizeBytes)
	}
	if item.TranscriptTextPath != "" {
		fmt.Printf("  Transcript: %s (%d bytes)\n", item.TranscriptTextPath, item.TranscriptTextSize)
	}
	if item.TranscriptJSONPath != "" {
		fmt.Printf("  Metadata:   %s (%d bytes)\n", item.TranscriptJSONPath, item.TranscriptJSONSize)
	}
}

func runListCLI(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	limit := fs.Int("limit", 20, "Number of recordings to list")
	query := fs.String("query", "", "Filter title keyword")
	all := fs.Bool("all", false, "Fetch all historical recordings")
	oldest := fs.Bool("oldest", false, "Oldest first")
	authPath := fs.String("auth", "", "Path to auth.json credentials")
	_ = fs.Parse(args)

	session, err := recorder.LoadAuth(*authPath)
	if err != nil {
		log.Fatalf("Authentication required: %v", err)
	}

	client := recorder.NewClient(session)
	recs, err := client.ListRecordings(context.Background(), recorder.ListOptions{
		Limit:       *limit,
		Query:       *query,
		All:         *all,
		OldestFirst: *oldest,
	})
	if err != nil {
		log.Fatalf("List recordings failed: %v", err)
	}

	fmt.Printf("%-36s  %-20s  %-10s  %s\n", "ID", "RECORDED AT", "DURATION", "TITLE")
	fmt.Println(strings.Repeat("-", 90))
	for _, r := range recs {
		dateStr := r.RecordedAt.Local().Format("2006-01-02 15:04")
		fmt.Printf("%-36s  %-20s  %-10s  %s\n", r.ID, dateStr, r.Duration, r.Title)
	}
	fmt.Printf("\nTotal: %d recordings\n", len(recs))
}

func runAuthCLI(args []string) {
	fs := flag.NewFlagSet("auth", flag.ExitOnError)
	check := fs.Bool("check", false, "Test existing saved authentication")
	refresh := fs.Bool("refresh", false, "Re-extract fresh cookies from local browser profiles")
	authUser := fs.Int("authuser", -1, "Google account index (default: auto-detected or 1)")
	path := fs.String("path", "", "Path to auth.json credentials")
	_ = fs.Parse(args)

	targetPath := *path
	if targetPath == "" {
		var err error
		targetPath, err = recorder.DefaultAuthPath()
		if err != nil {
			log.Fatalf("Failed getting default auth path: %v", err)
		}
	}

	fmt.Printf("Google Recorder Authentication Tool\n")
	fmt.Printf("Auth file path: %s\n\n", targetPath)

	if *refresh || !*check {
		fmt.Printf("Extracting cookies from local browser profiles...\n")
		cfg, err := recorder.ExtractFromBrowser(targetPath, *authUser)
		if err != nil {
			fmt.Printf("❌ Browser extraction failed: %v\n", err)
			fmt.Printf("Please ensure you are signed into https://recorder.google.com in your browser.\n")
			os.Exit(1)
		}
		fmt.Printf("✅ Extracted fresh cookies successfully!\n")
		fmt.Printf("   Google Account index: %d\n", cfg.AuthUser)
		fmt.Printf("   Saved to: %s\n\n", targetPath)
	}

	fmt.Printf("Testing connection with Google Recorder RPC API...\n")
	session, err := recorder.LoadAuth(targetPath)
	if err != nil {
		fmt.Printf("❌ Could not load session: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := session.TestAuth(ctx); err != nil {
		fmt.Printf("❌ Authentication test failed: %v\n", err)
		os.Exit(1)
	}

	cfg := session.GetConfig()
	fmt.Printf("✅ Authentication verified! Connected to Google Recorder (Account: %d).\n", cfg.AuthUser)
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Range")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Range, Content-Length, Accept-Ranges")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)

		// Don't clutter logs with audio chunk streaming Range requests
		if !strings.HasSuffix(r.URL.Path, "/audio") || r.Method != http.MethodGet {
			log.Printf("%s %s in %v", r.Method, r.URL.Path, time.Since(start))
		}
	})
}
