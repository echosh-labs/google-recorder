package recorder

import (
	"time"
)

// Recording represents a Google Recorder audio recording item.
type Recording struct {
	ID            string    `json:"id"`             // Web UUID used for API endpoints
	DeviceID      string    `json:"device_id"`      // Unique ID assigned by device
	Title         string    `json:"title"`          // User or auto-generated title
	RecordedAt    time.Time `json:"recorded_at"`    // Creation timestamp
	Duration      string    `json:"duration"`       // Human-friendly duration string (e.g. "5:23")
	DurationMs    int64     `json:"duration_ms"`    // Duration in milliseconds
	Location      string    `json:"location,omitempty"`
	AudioURL      string    `json:"audio_url,omitempty"`
	HasTranscript bool      `json:"has_transcript"`
}

// Transcript represents the full transcription of a recording.
type Transcript struct {
	RecordingID string              `json:"recording_id"`
	Segments    []TranscriptSegment `json:"segments"`
	FullText    string              `json:"full_text"`
}

// TranscriptSegment represents a continuous utterance spoken by a single speaker.
type TranscriptSegment struct {
	Speaker   string `json:"speaker"`
	StartTime string `json:"start_time"`
	StartMs   int64  `json:"start_ms"`
	EndTime   string `json:"end_time"`
	EndMs     int64  `json:"end_ms"`
	Text      string `json:"text"`
}

// ListOptions specifies parameters for listing recordings.
type ListOptions struct {
	Limit       int    `json:"limit"`
	OldestFirst bool   `json:"oldest_first"`
	All         bool   `json:"all"` // If true, pages through all historical recordings
	Query       string `json:"query,omitempty"`
}

