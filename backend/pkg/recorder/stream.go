package recorder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// StreamAudio streams the raw .m4a audio directly to an io.Writer without buffering in memory.
// It supports HTTP Range headers for scrubbing/seeking in web players.
func (c *Client) StreamAudio(ctx context.Context, recordingID string, rangeHeader string, dst io.Writer) (*AudioStreamInfo, error) {
	if recordingID == "" {
		return nil, errors.New("recording ID cannot be empty")
	}

	cfg := c.session.GetConfig()
	audioURL := fmt.Sprintf("https://usercontent.recorder.google.com/download/playback/%s?authuser=%d&download=true", recordingID, cfg.AuthUser)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, audioURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create audio request: %w", err)
	}

	req.Header.Set("User-Agent", DefaultUserAgent)
	req.Header.Set("Referer", Origin+"/")
	if cfg.Cookies != "" {
		req.Header.Set("Cookie", cfg.Cookies)
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("audio stream request failed: %w", err)
	}
	defer resp.Body.Close()

	// If unauthenticated (Google returns 401 or 404 for expired cookies), attempt auto-refresh
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusNotFound {
		if _, refErr := c.session.Refresh(); refErr == nil {
			// Retry request with fresh session
			cfg = c.session.GetConfig()
			audioURL = fmt.Sprintf("https://usercontent.recorder.google.com/download/playback/%s?authuser=%d&download=true", recordingID, cfg.AuthUser)
			retryReq, rErr := http.NewRequestWithContext(ctx, http.MethodGet, audioURL, nil)
			if rErr == nil {
				retryReq.Header.Set("User-Agent", DefaultUserAgent)
				retryReq.Header.Set("Referer", Origin+"/")
				if cfg.Cookies != "" {
					retryReq.Header.Set("Cookie", cfg.Cookies)
				}
				if rangeHeader != "" {
					retryReq.Header.Set("Range", rangeHeader)
				}
				if retryResp, doErr := c.httpClient.Do(retryReq); doErr == nil {
					resp = retryResp
					defer resp.Body.Close()
				}
			}
		}
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("audio endpoint returned HTTP %d", resp.StatusCode)
	}

	contentLength, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "audio/mp4"
	}

	// Forward streaming headers if destination is an http.ResponseWriter
	if rw, ok := dst.(http.ResponseWriter); ok {
		rw.Header().Set("Content-Type", contentType)
		rw.Header().Set("Accept-Ranges", "bytes")
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			rw.Header().Set("Content-Range", cr)
		}
		if contentLength > 0 {
			rw.Header().Set("Content-Length", strconv.FormatInt(contentLength, 10))
		}
		rw.WriteHeader(resp.StatusCode)
	}

	info := &AudioStreamInfo{
		StatusCode:    resp.StatusCode,
		ContentType:   contentType,
		ContentLength: contentLength,
		ContentRange:  resp.Header.Get("Content-Range"),
		Filename:      fmt.Sprintf("%s.m4a", recordingID),
	}

	// Zero-memory buffer copy directly to dst
	_, err = io.Copy(dst, resp.Body)
	if err != nil && !errors.Is(err, context.Canceled) {
		return info, fmt.Errorf("error during audio streaming: %w", err)
	}

	return info, nil
}

// AudioStreamInfo contains metadata from Google's audio response.
type AudioStreamInfo struct {
	StatusCode    int
	ContentType   string
	ContentLength int64
	ContentRange  string
	Filename      string
}
