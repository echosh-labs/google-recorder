package recorder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComputeSAPISIDHash(t *testing.T) {
	session := &Session{
		config: AuthConfig{
			SAPISID: "test_sapisid_12345",
		},
	}

	hash, ts := session.ComputeSAPISIDHash("https://recorder.google.com")
	if !strings.HasPrefix(hash, "SAPISIDHASH ") {
		t.Fatalf("unexpected prefix in hash: %s", hash)
	}

	parts := strings.Split(strings.TrimPrefix(hash, "SAPISIDHASH "), "_")
	if len(parts) != 2 {
		t.Fatalf("expected timestamp_hash format, got: %s", hash)
	}

	if ts == 0 {
		t.Fatalf("timestamp should not be 0")
	}
}

func TestSessionSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	authFile := filepath.Join(tmpDir, "auth.json")

	initial := &Session{
		config: AuthConfig{
			SAPISID:  "sapisid_test_value",
			Cookies:  "SAPISID=sapisid_test_value; foo=bar",
			AuthUser: 1,
			APIKey:   DefaultAPIKey,
		},
		authPath: authFile,
	}

	if err := initial.Save(); err != nil {
		t.Fatalf("failed to save session: %v", err)
	}

	loaded, err := LoadAuth(authFile)
	if err != nil {
		t.Fatalf("failed to load saved auth file: %v", err)
	}

	cfg := loaded.GetConfig()
	if cfg.SAPISID != "sapisid_test_value" {
		t.Errorf("expected SAPISID 'sapisid_test_value', got '%s'", cfg.SAPISID)
	}
	if cfg.AuthUser != 1 {
		t.Errorf("expected AuthUser 1, got %d", cfg.AuthUser)
	}
	if cfg.APIKey != DefaultAPIKey {
		t.Errorf("expected APIKey '%s', got '%s'", DefaultAPIKey, cfg.APIKey)
	}
	if loaded.GetAuthPath() != authFile {
		t.Errorf("expected authPath '%s', got '%s'", authFile, loaded.GetAuthPath())
	}
}

func TestCookieParsingFallback(t *testing.T) {
	tmpDir := t.TempDir()
	authFile := filepath.Join(tmpDir, "auth.json")

	// Missing explicit SAPISID, but present in cookies string
	rawJSON := `{"cookies": "SID=abc; SAPISID=parsed_from_cookie; HSID=xyz", "authUser": 0}`
	if err := os.WriteFile(authFile, []byte(rawJSON), 0o600); err != nil {
		t.Fatalf("failed writing test file: %v", err)
	}

	loaded, err := LoadAuth(authFile)
	if err != nil {
		t.Fatalf("LoadAuth failed: %v", err)
	}

	cfg := loaded.GetConfig()
	if cfg.SAPISID != "parsed_from_cookie" {
		t.Errorf("expected SAPISID 'parsed_from_cookie', got '%s'", cfg.SAPISID)
	}
	if cfg.APIKey != DefaultAPIKey {
		t.Errorf("expected default APIKey fallback, got '%s'", cfg.APIKey)
	}
}
