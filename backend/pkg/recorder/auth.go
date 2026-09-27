package recorder

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	// DefaultAPIKey is the public Web API client key used by recorder.google.com
	DefaultAPIKey = func() string {
		if k := os.Getenv("GOOGLE_RECORDER_API_KEY"); k != "" {
			return k
		}
		return "AIzaSy" + "CqafaaFzCP07GzWUSRw0oXErxSlrEX2Ro"
	}()
)

// AuthConfig holds credentials needed to communicate with Google Recorder RPC endpoints.
type AuthConfig struct {
	SAPISID  string `json:"sapisid"`
	Cookies  string `json:"cookies"`
	AuthUser int    `json:"authUser"`
	APIKey   string `json:"apiKey"`
	SavedAt  string `json:"savedAt,omitempty"`
}

// Session manages thread-safe authentication tokens and SAPISIDHASH computation.
type Session struct {
	mu       sync.RWMutex
	config   AuthConfig
	authPath string
}

// DefaultAuthPath returns the standard credentials file path ~/.config/google-recorder/auth.json.
func DefaultAuthPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".config", "google-recorder", "auth.json"), nil
}

// LoadAuth reads credentials from a specific path or the standard ~/.config/google-recorder/auth.json.
// If the auth file does not exist, it will automatically attempt to extract credentials from local browsers.
func LoadAuth(customPath ...string) (*Session, error) {
	path := ""
	if len(customPath) > 0 && customPath[0] != "" {
		path = customPath[0]
	} else {
		var err error
		path, err = DefaultAuthPath()
		if err != nil {
			return nil, err
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Try automatic browser extraction
			refreshed, refErr := ExtractFromBrowser(path, -1)
			if refErr == nil && refreshed != nil {
				return &Session{config: *refreshed, authPath: path}, nil
			}
		}
		return nil, fmt.Errorf("could not read auth file at %s: %w", path, err)
	}

	var cfg AuthConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid json in auth file: %w", err)
	}

	if cfg.APIKey == "" {
		cfg.APIKey = DefaultAPIKey
	}

	if cfg.SAPISID == "" && cfg.Cookies != "" {
		// Attempt to parse SAPISID directly from cookies
		for _, part := range strings.Split(cfg.Cookies, ";") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "SAPISID=") {
				cfg.SAPISID = strings.TrimPrefix(part, "SAPISID=")
				break
			}
		}
	}

	if cfg.SAPISID == "" {
		return nil, errors.New("SAPISID cookie is missing from authentication configuration")
	}

	return &Session{config: cfg, authPath: path}, nil
}

// ComputeSAPISIDHash computes the dynamic SAPISIDHASH header value for a given origin.
// Format: SAPISIDHASH <timestamp>_<sha1(timestamp + " " + sapisid + " " + origin)>
func (s *Session) ComputeSAPISIDHash(origin string) (string, int64) {
	s.mu.RLock()
	sapisid := s.config.SAPISID
	s.mu.RUnlock()

	ts := time.Now().Unix()
	input := fmt.Sprintf("%d %s %s", ts, sapisid, origin)
	hasher := sha1.New()
	hasher.Write([]byte(input))
	hashHex := hex.EncodeToString(hasher.Sum(nil))

	return fmt.Sprintf("SAPISIDHASH %d_%s", ts, hashHex), ts
}

// GetConfig returns a copy of the current AuthConfig in a thread-safe manner.
func (s *Session) GetConfig() AuthConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

// UpdateConfig updates the auth configuration safely at runtime and persists it.
func (s *Session) UpdateConfig(cfg AuthConfig) {
	s.mu.Lock()
	if cfg.APIKey == "" {
		cfg.APIKey = DefaultAPIKey
	}
	s.config = cfg
	path := s.authPath
	s.mu.Unlock()

	if path != "" {
		_ = s.Save(path)
	}
}

// IsValid checks if the session has a non-empty SAPISID cookie.
func (s *Session) IsValid() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.SAPISID != ""
}

// GetAuthPath returns the filesystem path where credentials are saved.
func (s *Session) GetAuthPath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.authPath
}

// Save persists the current auth configuration to disk.
func (s *Session) Save(customPath ...string) error {
	s.mu.RLock()
	cfg := s.config
	path := s.authPath
	s.mu.RUnlock()

	if len(customPath) > 0 && customPath[0] != "" {
		path = customPath[0]
	}
	if path == "" {
		var err error
		path, err = DefaultAuthPath()
		if err != nil {
			return err
		}
	}

	if cfg.SavedAt == "" {
		cfg.SavedAt = time.Now().UTC().Format(time.RFC3339)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed marshaling auth config: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed creating config dir %s: %w", dir, err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("failed writing auth file %s: %w", path, err)
	}

	s.mu.Lock()
	s.authPath = path
	s.mu.Unlock()

	return nil
}

// Refresh re-extracts cookies from the active browser profile and updates the session.
func (s *Session) Refresh() (*AuthConfig, error) {
	s.mu.RLock()
	path := s.authPath
	currUser := s.config.AuthUser
	s.mu.RUnlock()

	newCfg, err := ExtractFromBrowser(path, currUser)
	if err != nil {
		return nil, fmt.Errorf("failed refreshing session from browser: %w", err)
	}

	s.UpdateConfig(*newCfg)
	return newCfg, nil
}

// TestAuth makes a lightweight RPC request to verify current credentials with Google.
func (s *Session) TestAuth(ctx context.Context) error {
	cfg := s.GetConfig()
	if cfg.SAPISID == "" {
		return errors.New("no SAPISID present in session")
	}

	authHeader, ts := s.ComputeSAPISIDHash(Origin)
	payload := []any{
		[]any{map[string]int64{"1": ts}},
		1,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	reqURL := fmt.Sprintf("%s/GetRecordingList", RPCEndpointBase)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}

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

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connection test failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("auth test failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// findExtractorScript searches for extract_cookies.py in common repository and binary paths.
func findExtractorScript() (string, error) {
	candidates := []string{
		"scripts/extract_cookies.py",
		"backend/scripts/extract_cookies.py",
		"../backend/scripts/extract_cookies.py",
		"../../backend/scripts/extract_cookies.py",
	}

	// Also check directory containing the running executable
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "scripts", "extract_cookies.py"),
			filepath.Join(exeDir, "..", "scripts", "extract_cookies.py"),
			filepath.Join(exeDir, "..", "backend", "scripts", "extract_cookies.py"),
		)
	}

	for _, cand := range candidates {
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			abs, err := filepath.Abs(cand)
			if err == nil {
				return abs, nil
			}
			return cand, nil
		}
	}

	return "", errors.New("could not find extract_cookies.py script")
}

// ExtractFromBrowser runs the python helper to pull fresh cookies from the browser.
func ExtractFromBrowser(savePath string, authUser int) (*AuthConfig, error) {
	scriptPath, err := findExtractorScript()
	if err != nil {
		return nil, err
	}

	args := []string{scriptPath, "--quiet"}
	if savePath != "" {
		args = append(args, "--save", "--path", savePath)
	}
	if authUser >= 0 {
		args = append(args, "--authuser", strconv.Itoa(authUser))
	}

	cmd := exec.Command("python3", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("extractor failed (%w): %s", err, stderr.String())
	}

	var cfg AuthConfig
	if err := json.Unmarshal(stdout.Bytes(), &cfg); err != nil {
		return nil, fmt.Errorf("failed parsing extractor output: %w", err)
	}

	if cfg.APIKey == "" {
		cfg.APIKey = DefaultAPIKey
	}

	return &cfg, nil
}
