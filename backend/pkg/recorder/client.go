package recorder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	RPCEndpointBase   = "https://pixelrecorder-pa.clients6.google.com/$rpc/java.com.google.wireless.android.pixel.recorder.protos.PlaybackService"
	Origin            = "https://recorder.google.com"
	DefaultUserAgent  = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	DefaultGRPCUserAg = "grpc-web-javascript/0.1"
)

// Client is a high-performance client for Google Recorder's internal RPC and media APIs.
type Client struct {
	session    *Session
	httpClient *http.Client
}

// NewClient initializes a Client with HTTP/2 transport and persistent connection reuse.
func NewClient(session *Session) *Client {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
	}

	return &Client{
		session: session,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   60 * time.Second,
		},
	}
}

// Session returns the underlying Session instance.
func (c *Client) Session() *Session {
	return c.session
}

// doRPC executes a POST request against Google Recorder's internal PlaybackService RPC,
// with automatic 401 silent re-authentication retry.
func (c *Client) doRPC(ctx context.Context, method string, reqPayload any) ([]byte, error) {
	respBody, statusCode, err := c.doRPCOnce(ctx, method, reqPayload)
	if statusCode == http.StatusUnauthorized {
		// Attempt silent auto-refresh from browser
		if _, refErr := c.session.Refresh(); refErr == nil {
			respBody, statusCode, err = c.doRPCOnce(ctx, method, reqPayload)
		}
	}
	if err != nil {
		return nil, err
	}

	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("rpc %s failed with HTTP %d: %s", method, statusCode, string(respBody))
	}

	return respBody, nil
}

func (c *Client) doRPCOnce(ctx context.Context, method string, reqPayload any) ([]byte, int, error) {
	bodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to encode request: %w", err)
	}

	reqURL := fmt.Sprintf("%s/%s", RPCEndpointBase, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	cfg := c.session.GetConfig()
	authHeader, _ := c.session.ComputeSAPISIDHash(Origin)

	req.Header.Set("Content-Type", "application/json+protobuf")
	req.Header.Set("X-User-Agent", DefaultGRPCUserAg)
	req.Header.Set("X-Goog-Api-Key", cfg.APIKey)
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("X-Goog-AuthUser", strconv.Itoa(cfg.AuthUser))
	req.Header.Set("Origin", Origin)
	req.Header.Set("Referer", Origin+"/")
	req.Header.Set("User-Agent", DefaultUserAgent)
	if cfg.Cookies != "" {
		req.Header.Set("Cookie", cfg.Cookies)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request error: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed reading response body: %w", err)
	}

	return respBody, resp.StatusCode, nil
}

// ListRecordings fetches recordings from the server with optional pagination.
func (c *Client) ListRecordings(ctx context.Context, opts ListOptions) ([]Recording, error) {
	requestedLimit := opts.Limit
	if requestedLimit <= 0 && !opts.All {
		requestedLimit = 50
	}
	if opts.All && opts.Limit <= 0 {
		requestedLimit = 10000 // effectively no limit
	}

	seenIDs := make(map[string]bool)
	var allRecordings []Recording
	cursorSec := time.Now().Unix()

	for {
		// Google's max batch size per RPC call is 100
		batchLimit := 100
		remaining := requestedLimit - len(allRecordings)
		if remaining < batchLimit {
			batchLimit = remaining
		}
		if batchLimit <= 0 {
			break
		}

		payload := []any{
			[]any{map[string]int64{"1": cursorSec}},
			batchLimit,
		}

		raw, err := c.doRPC(ctx, "GetRecordingList", payload)
		if err != nil {
			return nil, err
		}

		var respData []any
		if err := json.Unmarshal(raw, &respData); err != nil {
			return nil, fmt.Errorf("failed parsing recording list response: %w", err)
		}

		if len(respData) == 0 {
			break
		}

		itemsArray, ok := respData[0].([]any)
		if !ok || len(itemsArray) == 0 {
			break
		}

		batchAdded := 0
		var oldestRecordedSec int64 = 0

		for _, itemRaw := range itemsArray {
			item, ok := itemRaw.([]any)
			if !ok || len(item) < 4 {
				continue
			}

			rec := parseRecordingItem(item)
			if rec.ID == "" || seenIDs[rec.ID] {
				continue
			}

			seenIDs[rec.ID] = true
			recSec := rec.RecordedAt.Unix()
			if oldestRecordedSec == 0 || recSec < oldestRecordedSec {
				oldestRecordedSec = recSec
			}

			if opts.Query != "" {
				if !strings.Contains(strings.ToLower(rec.Title), strings.ToLower(opts.Query)) {
					continue
				}
			}

			allRecordings = append(allRecordings, rec)
			batchAdded++

			if len(allRecordings) >= requestedLimit {
				break
			}
		}

		// If no new recordings were added in this batch, or fewer than requested returned, we've reached the end
		if batchAdded == 0 || len(itemsArray) < batchLimit || oldestRecordedSec == 0 {
			break
		}

		// Page backwards in time: query starting 1 second before the oldest recording in the current batch
		if oldestRecordedSec <= 1 {
			break
		}
		cursorSec = oldestRecordedSec - 1
	}

	if opts.OldestFirst {
		// Reverse to present oldest first
		for i, j := 0, len(allRecordings)-1; i < j; i, j = i+1, j-1 {
			allRecordings[i], allRecordings[j] = allRecordings[j], allRecordings[i]
		}
	}

	return allRecordings, nil
}

// GetRecordingInfo fetches detailed metadata for a single recording ID.
func (c *Client) GetRecordingInfo(ctx context.Context, recordingID string) (*Recording, error) {
	if recordingID == "" {
		return nil, errors.New("recording ID cannot be empty")
	}

	payload := []string{recordingID}
	raw, err := c.doRPC(ctx, "GetRecordingInfo", payload)
	if err != nil {
		return nil, err
	}

	var respData []any
	if err := json.Unmarshal(raw, &respData); err != nil {
		return nil, fmt.Errorf("failed parsing GetRecordingInfo response: %w", err)
	}

	if len(respData) == 0 {
		return nil, fmt.Errorf("recording %s not found", recordingID)
	}

	item, ok := respData[0].([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected recording metadata structure")
	}

	rec := parseRecordingItem(item)
	if len(respData) > 3 {
		if audioURL, ok := respData[3].(string); ok && audioURL != "" {
			rec.AudioURL = audioURL
		}
	}

	return &rec, nil
}

// GetTranscript fetches words and speaker segments for a recording.
func (c *Client) GetTranscript(ctx context.Context, recordingID string) (*Transcript, error) {
	if recordingID == "" {
		return nil, errors.New("recording ID cannot be empty")
	}

	payload := []string{recordingID}
	raw, err := c.doRPC(ctx, "GetTranscription", payload)
	if err != nil {
		return nil, err
	}

	var respData []any
	if err := json.Unmarshal(raw, &respData); err != nil {
		return nil, fmt.Errorf("failed parsing transcription response: %w", err)
	}

	if len(respData) == 0 {
		return nil, fmt.Errorf("transcript for %s not found", recordingID)
	}

	segmentsArray, ok := respData[0].([]any)
	if !ok {
		return &Transcript{RecordingID: recordingID}, nil
	}

	t := &Transcript{
		RecordingID: recordingID,
		Segments:    make([]TranscriptSegment, 0),
	}

	var fullTextBuilder strings.Builder

	for _, segRaw := range segmentsArray {
		segTuple, ok := segRaw.([]any)
		if !ok || len(segTuple) < 2 {
			continue
		}

		rawWords, ok := segTuple[0].([]any)
		if !ok || len(rawWords) == 0 {
			continue
		}

		speakerID := 0
		if spkNum, ok := segTuple[1].(float64); ok {
			speakerID = int(spkNum)
		}

		var (
			segTextBuilder strings.Builder
			startMs        int64
			endMs          int64
		)

		for idx, wRaw := range rawWords {
			wordTuple, ok := wRaw.([]any)
			if !ok || len(wordTuple) < 4 {
				continue
			}

			// wordTuple: [rawWord, formattedWord, startMsStr, endMsStr, ...]
			wordText, _ := wordTuple[0].(string)
			if formatted, ok := wordTuple[1].(string); ok && formatted != "" {
				wordText = formatted
			}

			if idx == 0 {
				if sStr, ok := wordTuple[2].(string); ok {
					startMs, _ = strconv.ParseInt(sStr, 10, 64)
				}
			}
			if eStr, ok := wordTuple[3].(string); ok {
				endMs, _ = strconv.ParseInt(eStr, 10, 64)
			}

			if segTextBuilder.Len() > 0 && !strings.HasPrefix(wordText, "\n") {
				segTextBuilder.WriteString(" ")
			}
			segTextBuilder.WriteString(wordText)
		}

		segment := TranscriptSegment{
			Speaker:   fmt.Sprintf("Speaker %d", speakerID+1),
			StartTime: formatMilliseconds(startMs),
			StartMs:   startMs,
			EndTime:   formatMilliseconds(endMs),
			EndMs:     endMs,
			Text:      strings.TrimSpace(segTextBuilder.String()),
		}

		t.Segments = append(t.Segments, segment)
		if fullTextBuilder.Len() > 0 {
			fullTextBuilder.WriteString("\n\n")
		}
		fullTextBuilder.WriteString(fmt.Sprintf("[%s] (%s)\n%s", segment.Speaker, segment.StartTime, segment.Text))
	}

	t.FullText = fullTextBuilder.String()
	return t, nil
}

// parseRecordingItem maps Google's raw jspb array to a Recording struct.
func parseRecordingItem(item []any) Recording {
	var r Recording

	// [0] Device UUID
	if devID, ok := item[0].(string); ok {
		r.DeviceID = devID
		r.ID = devID
	}

	// [1] Title
	if title, ok := item[1].(string); ok {
		r.Title = title
	}

	// [2] Created timestamp: [seconds_str, nanoseconds]
	if len(item) > 2 {
		if tsTuple, ok := item[2].([]any); ok && len(tsTuple) >= 2 {
			if secStr, ok := tsTuple[0].(string); ok {
				sec, _ := strconv.ParseInt(secStr, 10, 64)
				var nano int64
				if nanoF, ok := tsTuple[1].(float64); ok {
					nano = int64(nanoF)
				}
				r.RecordedAt = time.Unix(sec, nano).UTC()
			}
		}
	}

	// [3] Duration: [seconds_str, nanoseconds]
	if len(item) > 3 {
		if durTuple, ok := item[3].([]any); ok && len(durTuple) >= 2 {
			if secStr, ok := durTuple[0].(string); ok {
				sec, _ := strconv.ParseInt(secStr, 10, 64)
				var nano int64
				if nanoF, ok := durTuple[1].(float64); ok {
					nano = int64(nanoF)
				}
				r.DurationMs = (sec * 1000) + (nano / 1_000_000)
				r.Duration = formatMilliseconds(r.DurationMs)
			}
		}
	}

	// [6] Location
	if len(item) > 6 {
		if loc, ok := item[6].(string); ok {
			r.Location = loc
		}
	}

	// [13] Web UUID (preferred over device UUID for API requests)
	if len(item) > 13 {
		if webID, ok := item[13].(string); ok && webID != "" {
			r.ID = webID
		}
	}

	r.HasTranscript = true
	return r
}

func formatMilliseconds(ms int64) string {
	totalSec := ms / 1000
	h := totalSec / 3600
	m := (totalSec % 3600) / 60
	s := totalSec % 60

	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}
