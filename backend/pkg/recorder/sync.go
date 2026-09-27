package recorder

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	sanitizerRegex  = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1F]`)
	multiSpaceRegex = regexp.MustCompile(`[\s_]+`)
)

// SanitizeFilename cleans an arbitrary string for safe usage across Linux, macOS, and Windows.
func SanitizeFilename(name string) string {
	cleaned := sanitizerRegex.ReplaceAllString(name, "_")
	cleaned = multiSpaceRegex.ReplaceAllString(cleaned, "_")
	cleaned = strings.Trim(cleaned, "._- ")
	if cleaned == "" {
		cleaned = "recording"
	}
	if len(cleaned) > 80 {
		cleaned = cleaned[:80]
		cleaned = strings.TrimRight(cleaned, "._- ")
	}
	return cleaned
}

// FormatRecordingBaseName generates a deterministic, human-readable prefix:
// Format: YYYY-MM-DD_HHMMSS_<CleanTitle>_<ShortID>
func FormatRecordingBaseName(rec Recording) string {
	dateStr := "unknown_date"
	if !rec.RecordedAt.IsZero() {
		dateStr = rec.RecordedAt.Local().Format("2006-01-02_150405")
	}

	cleanTitle := SanitizeFilename(rec.Title)
	shortID := rec.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	return fmt.Sprintf("%s_%s_%s", dateStr, cleanTitle, shortID)
}

// ManifestEntry represents a synced recording in the local destination directory.
type ManifestEntry struct {
	RecordingID        string    `json:"recording_id"`
	DeviceID           string    `json:"device_id,omitempty"`
	Title              string    `json:"title"`
	RecordedAt         time.Time `json:"recorded_at"`
	Duration           string    `json:"duration"`
	DurationMs         int64     `json:"duration_ms"`
	Location           string    `json:"location,omitempty"`
	AudioFile          string    `json:"audio_file,omitempty"`
	AudioSizeBytes     int64     `json:"audio_size_bytes,omitempty"`
	TranscriptTextFile string    `json:"transcript_text_file,omitempty"`
	TranscriptTextSize int64     `json:"transcript_text_size_bytes,omitempty"`
	TranscriptJSONFile string    `json:"transcript_json_file,omitempty"`
	TranscriptJSONSize int64     `json:"transcript_json_size_bytes,omitempty"`
	SyncedAt           time.Time `json:"synced_at"`
	HasTranscript      bool      `json:"has_transcript"`
}

// SyncManifest tracks synchronization state across executions.
type SyncManifest struct {
	Version    string                   `json:"version"`
	LastSync   time.Time                `json:"last_sync"`
	Recordings map[string]ManifestEntry `json:"recordings"`
	mu         sync.RWMutex             `json:"-"`
}

// LoadManifest reads or initializes the sync manifest from a target directory.
func LoadManifest(dir string) (*SyncManifest, error) {
	manifestPath := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &SyncManifest{
				Version:    "1.0",
				LastSync:   time.Time{},
				Recordings: make(map[string]ManifestEntry),
			}, nil
		}
		return nil, fmt.Errorf("failed reading manifest: %w", err)
	}

	var m SyncManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed parsing manifest.json: %w", err)
	}
	if m.Recordings == nil {
		m.Recordings = make(map[string]ManifestEntry)
	}
	return &m, nil
}

// Save atomically writes the manifest to disk.
func (m *SyncManifest) Save(dir string) error {
	m.mu.Lock()
	m.LastSync = time.Now().UTC()
	data, err := json.MarshalIndent(m, "", "  ")
	m.mu.Unlock()

	if err != nil {
		return fmt.Errorf("failed serializing manifest: %w", err)
	}

	manifestPath := filepath.Join(dir, "manifest.json")
	tmpPath := manifestPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("failed writing tmp manifest: %w", err)
	}
	if err := os.Rename(tmpPath, manifestPath); err != nil {
		return fmt.Errorf("failed replacing manifest: %w", err)
	}
	return nil
}

// PutEntry safely updates a recording entry in memory.
func (m *SyncManifest) PutEntry(entry ManifestEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Recordings[entry.RecordingID] = entry
}

// GetEntry safely looks up an entry.
func (m *SyncManifest) GetEntry(id string) (ManifestEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.Recordings[id]
	return entry, ok
}

// DownloadOptions configures an individual recording download.
type DownloadOptions struct {
	OutputDir          string `json:"output_dir"`
	DownloadAudio      bool   `json:"download_audio"`
	DownloadTranscript bool   `json:"download_transcript"`
	TranscriptFormat   string `json:"transcript_format"` // "text", "json", "both"
	DownloadMetadata   bool   `json:"download_metadata"`
	Force              bool   `json:"force"`
}

// SyncOptions configures library synchronization.
type SyncOptions struct {
	OutputDir          string `json:"output_dir"`
	DownloadAudio      bool   `json:"download_audio"`
	DownloadTranscript bool   `json:"download_transcript"`
	TranscriptFormat   string `json:"transcript_format"` // "text", "json", "both" (default: "both")
	DownloadMetadata   bool   `json:"download_metadata"`
	Concurrency        int    `json:"concurrency"`
	Limit              int    `json:"limit"`
	All                bool   `json:"all"`
	Query              string `json:"query"`
	OldestFirst        bool   `json:"oldest_first"`
	Force              bool   `json:"force"`
}

// SyncItem represents the result of syncing an individual recording.
type SyncItem struct {
	RecordingID        string    `json:"recording_id"`
	Title              string    `json:"title"`
	RecordedAt         time.Time `json:"recorded_at"`
	AudioPath          string    `json:"audio_path,omitempty"`
	AudioSizeBytes     int64     `json:"audio_size_bytes,omitempty"`
	TranscriptTextPath string    `json:"transcript_text_path,omitempty"`
	TranscriptTextSize int64     `json:"transcript_text_size,omitempty"`
	TranscriptJSONPath string    `json:"transcript_json_path,omitempty"`
	TranscriptJSONSize int64     `json:"transcript_json_size,omitempty"`
	Skipped            bool      `json:"skipped"`
	Status             string    `json:"status"` // "downloaded", "skipped", "error"
	Error              string    `json:"error,omitempty"`
}

// SyncReport contains summary statistics for a completed sync job.
type SyncReport struct {
	OutputDir       string        `json:"output_dir"`
	TotalFound      int           `json:"total_found"`
	Downloaded      int           `json:"downloaded"`
	Skipped         int           `json:"skipped"`
	Failed          int           `json:"failed"`
	StartTime       time.Time     `json:"start_time"`
	EndTime         time.Time     `json:"end_time"`
	DurationSeconds float64       `json:"duration_seconds"`
	Items           []SyncItem    `json:"items"`
	Errors          []string      `json:"errors,omitempty"`
}

// DownloadRecording downloads audio, transcript, and metadata for a single recording.
func (c *Client) DownloadRecording(ctx context.Context, rec Recording, opts DownloadOptions) (*SyncItem, error) {
	return c.downloadRecording(ctx, rec, opts, nil)
}

func (c *Client) downloadRecording(ctx context.Context, rec Recording, opts DownloadOptions, sharedManifest *SyncManifest) (*SyncItem, error) {
	if opts.OutputDir == "" {
		opts.OutputDir = "./recordings"
	}
	if opts.TranscriptFormat == "" {
		opts.TranscriptFormat = "both"
	}

	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed creating output dir %s: %w", opts.OutputDir, err)
	}

	manifest := sharedManifest
	if manifest == nil {
		var err error
		manifest, err = LoadManifest(opts.OutputDir)
		if err != nil {
			return nil, err
		}
	}

	baseName := FormatRecordingBaseName(rec)
	item := &SyncItem{
		RecordingID: rec.ID,
		Title:       rec.Title,
		RecordedAt:  rec.RecordedAt,
		Status:      "downloaded",
	}

	existing, hasExisting := manifest.GetEntry(rec.ID)
	shouldSkip := hasExisting && !opts.Force

	// Determine required files
	targetAudio := ""
	if opts.DownloadAudio {
		targetAudio = filepath.Join(opts.OutputDir, baseName+".m4a")
		if shouldSkip && existing.AudioFile != "" {
			if fi, err := os.Stat(filepath.Join(opts.OutputDir, existing.AudioFile)); err == nil && fi.Size() > 0 {
				item.AudioPath = filepath.Join(opts.OutputDir, existing.AudioFile)
				item.AudioSizeBytes = fi.Size()
			} else {
				shouldSkip = false
			}
		} else if !hasExisting {
			shouldSkip = false
		}
	}

	targetText := ""
	if opts.DownloadTranscript && (opts.TranscriptFormat == "text" || opts.TranscriptFormat == "both") {
		targetText = filepath.Join(opts.OutputDir, baseName+".txt")
		if shouldSkip && existing.TranscriptTextFile != "" {
			if fi, err := os.Stat(filepath.Join(opts.OutputDir, existing.TranscriptTextFile)); err == nil && fi.Size() > 0 {
				item.TranscriptTextPath = filepath.Join(opts.OutputDir, existing.TranscriptTextFile)
				item.TranscriptTextSize = fi.Size()
			} else {
				shouldSkip = false
			}
		} else if !hasExisting {
			shouldSkip = false
		}
	}

	targetJSON := ""
	if opts.DownloadMetadata || (opts.DownloadTranscript && (opts.TranscriptFormat == "json" || opts.TranscriptFormat == "both")) {
		targetJSON = filepath.Join(opts.OutputDir, baseName+".json")
		if shouldSkip && existing.TranscriptJSONFile != "" {
			if fi, err := os.Stat(filepath.Join(opts.OutputDir, existing.TranscriptJSONFile)); err == nil && fi.Size() > 0 {
				item.TranscriptJSONPath = filepath.Join(opts.OutputDir, existing.TranscriptJSONFile)
				item.TranscriptJSONSize = fi.Size()
			} else {
				shouldSkip = false
			}
		} else if !hasExisting {
			shouldSkip = false
		}
	}

	if shouldSkip {
		item.Skipped = true
		item.Status = "skipped"
		return item, nil
	}

	manifestEntry := ManifestEntry{
		RecordingID:   rec.ID,
		DeviceID:      rec.DeviceID,
		Title:         rec.Title,
		RecordedAt:    rec.RecordedAt,
		Duration:      rec.Duration,
		DurationMs:    rec.DurationMs,
		Location:      rec.Location,
		HasTranscript: rec.HasTranscript,
		SyncedAt:      time.Now().UTC(),
	}

	// 1. Download audio
	if opts.DownloadAudio {
		f, err := os.Create(targetAudio)
		if err != nil {
			return nil, fmt.Errorf("failed creating audio destination: %w", err)
		}
		streamInfo, err := c.StreamAudio(ctx, rec.ID, "", f)
		_ = f.Close()
		if err != nil {
			_ = os.Remove(targetAudio)
			return nil, fmt.Errorf("audio download failed: %w", err)
		}
		fi, _ := os.Stat(targetAudio)
		item.AudioPath = targetAudio
		item.AudioSizeBytes = fi.Size()
		if streamInfo != nil && streamInfo.ContentLength > 0 {
			manifestEntry.AudioSizeBytes = streamInfo.ContentLength
		} else {
			manifestEntry.AudioSizeBytes = fi.Size()
		}
		manifestEntry.AudioFile = filepath.Base(targetAudio)
	}

	// 2. Fetch transcript if requested
	var transcript *Transcript
	if targetText != "" || targetJSON != "" {
		t, err := c.GetTranscript(ctx, rec.ID)
		if err == nil && t != nil {
			transcript = t
		}
	}

	// 3. Write text transcript (.txt)
	if targetText != "" {
		textData := ""
		if transcript != nil {
			textData = transcript.FullText
		}
		if err := os.WriteFile(targetText, []byte(textData), 0o644); err != nil {
			return nil, fmt.Errorf("failed writing text transcript: %w", err)
		}
		item.TranscriptTextPath = targetText
		item.TranscriptTextSize = int64(len(textData))
		manifestEntry.TranscriptTextFile = filepath.Base(targetText)
		manifestEntry.TranscriptTextSize = int64(len(textData))
	}

	// 4. Write full JSON metadata & segments (.json)
	if targetJSON != "" {
		metaPayload := map[string]any{
			"recording":   rec,
			"transcript":  transcript,
			"exported_at": time.Now().UTC().Format(time.RFC3339),
		}
		jsonData, err := json.MarshalIndent(metaPayload, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed encoding metadata json: %w", err)
		}
		if err := os.WriteFile(targetJSON, jsonData, 0o644); err != nil {
			return nil, fmt.Errorf("failed writing metadata json: %w", err)
		}
		item.TranscriptJSONPath = targetJSON
		item.TranscriptJSONSize = int64(len(jsonData))
		manifestEntry.TranscriptJSONFile = filepath.Base(targetJSON)
		manifestEntry.TranscriptJSONSize = int64(len(jsonData))
	}

	// Update manifest
	manifest.PutEntry(manifestEntry)
	_ = manifest.Save(opts.OutputDir)

	return item, nil
}

// Sync downloads all matching recordings to the output directory using worker pooling.
func (c *Client) Sync(ctx context.Context, opts SyncOptions, progressCb func(current, total int, item *SyncItem)) (*SyncReport, error) {
	if opts.OutputDir == "" {
		opts.OutputDir = "./recordings"
	}
	if opts.TranscriptFormat == "" {
		opts.TranscriptFormat = "both"
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 3
	}

	startTime := time.Now()
	report := &SyncReport{
		OutputDir: opts.OutputDir,
		StartTime: startTime,
		Items:     make([]SyncItem, 0),
	}

	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed creating output dir: %w", err)
	}

	manifest, err := LoadManifest(opts.OutputDir)
	if err != nil {
		return nil, err
	}

	// Fetch recording list
	listOpts := ListOptions{
		Limit:       opts.Limit,
		All:         opts.All,
		Query:       opts.Query,
		OldestFirst: opts.OldestFirst,
	}
	recordings, err := c.ListRecordings(ctx, listOpts)
	if err != nil {
		return nil, fmt.Errorf("failed listing recordings: %w", err)
	}

	report.TotalFound = len(recordings)
	if len(recordings) == 0 {
		report.EndTime = time.Now()
		report.DurationSeconds = report.EndTime.Sub(startTime).Seconds()
		return report, nil
	}

	// Worker pool
	type task struct {
		idx int
		rec Recording
	}

	taskCh := make(chan task, len(recordings))
	for i, r := range recordings {
		taskCh <- task{idx: i, rec: r}
	}
	close(taskCh)

	var (
		mu           sync.Mutex
		results      = make([]SyncItem, len(recordings))
		processedCnt int32
		wg           sync.WaitGroup
	)

	dlOpts := DownloadOptions{
		OutputDir:          opts.OutputDir,
		DownloadAudio:      opts.DownloadAudio,
		DownloadTranscript: opts.DownloadTranscript,
		TranscriptFormat:   opts.TranscriptFormat,
		DownloadMetadata:   opts.DownloadMetadata,
		Force:              opts.Force,
	}

	for i := 0; i < opts.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case t, ok := <-taskCh:
					if !ok {
						return
					}

					item, dlErr := c.downloadRecording(ctx, t.rec, dlOpts, manifest)
					if dlErr != nil {
						item = &SyncItem{
							RecordingID: t.rec.ID,
							Title:       t.rec.Title,
							RecordedAt:  t.rec.RecordedAt,
							Status:      "error",
							Error:       dlErr.Error(),
						}
					}

					currProcessed := int(atomic.AddInt32(&processedCnt, 1))

					mu.Lock()
					results[t.idx] = *item
					if item.Status == "downloaded" {
						report.Downloaded++
					} else if item.Status == "skipped" {
						report.Skipped++
					} else {
						report.Failed++
						report.Errors = append(report.Errors, fmt.Sprintf("[%s] %s: %s", item.RecordingID, item.Title, item.Error))
					}
					mu.Unlock()

					if progressCb != nil {
						progressCb(currProcessed, len(recordings), item)
					}
				}
			}
		}()
	}

	wg.Wait()

	// Persist finalized manifest
	_ = manifest.Save(opts.OutputDir)

	report.EndTime = time.Now()
	report.DurationSeconds = report.EndTime.Sub(startTime).Seconds()
	report.Items = results

	return report, nil
}
