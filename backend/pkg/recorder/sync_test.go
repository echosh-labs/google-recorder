package recorder

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Hello World", "Hello_World"},
		{"Meeting / Planning : 2026", "Meeting_Planning_2026"},
		{"Illegal * ? < > | chars", "Illegal_chars"},
		{"", "recording"},
		{"   Multiple    spaces   ", "Multiple_spaces"},
		{"...dots and dashes---", "dots_and_dashes"},
	}

	for _, tt := range tests {
		got := SanitizeFilename(tt.input)
		if got != tt.expected {
			t.Errorf("SanitizeFilename(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFormatRecordingBaseName(t *testing.T) {
	recTime := time.Date(2026, 9, 7, 10, 1, 23, 0, time.UTC)
	rec := Recording{
		ID:         "96b5fc02-f8e5-4369-a16f-7e669e139e22",
		Title:      "Sep 7 at 10:01 AM",
		RecordedAt: recTime,
	}

	baseName := FormatRecordingBaseName(rec)
	if baseName == "" {
		t.Fatalf("baseName should not be empty")
	}
	if !filepath.IsLocal(baseName) {
		t.Errorf("expected local path name, got: %s", baseName)
	}
}

func TestManifestSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()

	manifest, err := LoadManifest(tmpDir)
	if err != nil {
		t.Fatalf("LoadManifest on new dir failed: %v", err)
	}

	entry := ManifestEntry{
		RecordingID:        "rec-123",
		Title:              "Test Title",
		RecordedAt:         time.Now().UTC(),
		Duration:           "02:30",
		AudioFile:          "test.m4a",
		AudioSizeBytes:     1024,
		TranscriptTextFile: "test.txt",
		TranscriptTextSize: 50,
		SyncedAt:           time.Now().UTC(),
	}

	manifest.PutEntry(entry)
	if err := manifest.Save(tmpDir); err != nil {
		t.Fatalf("Save manifest failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(filepath.Join(tmpDir, "manifest.json")); err != nil {
		t.Fatalf("manifest.json file not created: %v", err)
	}

	// Reload
	reloaded, err := LoadManifest(tmpDir)
	if err != nil {
		t.Fatalf("reloading manifest failed: %v", err)
	}

	got, ok := reloaded.GetEntry("rec-123")
	if !ok {
		t.Fatalf("expected entry rec-123 not found in reloaded manifest")
	}

	if got.Title != "Test Title" {
		t.Errorf("expected Title 'Test Title', got %q", got.Title)
	}
	if got.AudioFile != "test.m4a" {
		t.Errorf("expected AudioFile 'test.m4a', got %q", got.AudioFile)
	}
}
