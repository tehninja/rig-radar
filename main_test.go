package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindTownRoot(t *testing.T) {
	// Create a temp directory tree: town/mayor/
	tmp := t.TempDir()
	mayorDir := filepath.Join(tmp, "mayor")
	if err := os.Mkdir(mayorDir, 0755); err != nil {
		t.Fatal(err)
	}

	// findTownRoot from town dir itself should find it
	got := findTownRoot(tmp)
	if got != tmp {
		t.Errorf("findTownRoot(%q) = %q, want %q", tmp, got, tmp)
	}

	// findTownRoot from a child dir should walk up
	child := filepath.Join(tmp, "somedir")
	os.Mkdir(child, 0755)
	got = findTownRoot(child)
	if got != tmp {
		t.Errorf("findTownRoot(%q) = %q, want %q", child, got, tmp)
	}

	// findTownRoot from a deeply nested child
	deep := filepath.Join(tmp, "a", "b", "c")
	os.MkdirAll(deep, 0755)
	got = findTownRoot(deep)
	if got != tmp {
		t.Errorf("findTownRoot(%q) = %q, want %q", deep, got, tmp)
	}
}

func TestFindTownRootWithGastown(t *testing.T) {
	tmp := t.TempDir()
	// Use .gastown marker instead of mayor/
	f, err := os.Create(filepath.Join(tmp, ".gastown"))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	child := filepath.Join(tmp, "rig", "polecats")
	os.MkdirAll(child, 0755)

	got := findTownRoot(child)
	if got != tmp {
		t.Errorf("findTownRoot(%q) = %q, want %q", child, got, tmp)
	}
}

func TestFindTownRootNoMarker(t *testing.T) {
	tmp := t.TempDir()
	// No mayor/ or .gastown marker - should fallback
	got := findTownRoot(tmp)
	// It won't find the marker, but shouldn't panic
	if got == "" {
		t.Error("findTownRoot should not return empty string")
	}
}

func TestBeadsDirForID(t *testing.T) {
	// Save and restore global state
	origMap := prefixMap
	origRoot := townRoot
	defer func() {
		prefixMap = origMap
		townRoot = origRoot
	}()

	townRoot = "/fake/town"
	prefixMap = map[string]string{
		"ri":  "/fake/town/rigradar/.beads",
		"hq":  "/fake/town/.beads",
		"gt":  "/fake/town/gastown/.beads",
	}

	tests := []struct {
		id   string
		want string
	}{
		{"ri-abc-123", "/fake/town/rigradar/.beads"},
		{"hq-xyz-456", "/fake/town/.beads"},
		{"gt-rig-abc", "/fake/town/gastown/.beads"},
		{"unknown-xyz", "/fake/town/.beads"},       // fallback
		{"noprefixhere", "/fake/town/.beads"},       // no dash
	}

	for _, tt := range tests {
		got := beadsDirForID(tt.id)
		if got != tt.want {
			t.Errorf("beadsDirForID(%q) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	origPath := configPath
	defer func() { configPath = origPath }()

	// Point to nonexistent file to get defaults
	configPath = filepath.Join(t.TempDir(), "nonexistent.json")

	cfg := loadConfig()

	if !cfg.Filters.HideSystemBeads {
		t.Error("expected HideSystemBeads default true")
	}
	if !cfg.Filters.HideEvents {
		t.Error("expected HideEvents default true")
	}
	if !cfg.Filters.HideRigIdentity {
		t.Error("expected HideRigIdentity default true")
	}
	if !cfg.Filters.HideMaintenanceWisps {
		t.Error("expected HideMaintenanceWisps default true")
	}
	if cfg.Server.Port != 9292 {
		t.Errorf("expected default port 9292, got %d", cfg.Server.Port)
	}
	if cfg.Server.Host != "localhost" {
		t.Errorf("expected default host localhost, got %q", cfg.Server.Host)
	}
	if cfg.RefreshInterval != 30000 {
		t.Errorf("expected default refreshInterval 30000, got %d", cfg.RefreshInterval)
	}
}

func TestLoadConfigFromFile(t *testing.T) {
	origPath := configPath
	defer func() { configPath = origPath }()

	tmp := t.TempDir()
	configPath = filepath.Join(tmp, "config.json")
	data := `{
		"filters": {"hideSystemBeads": false, "hideEvents": false, "hideRigIdentity": false, "hideMaintenanceWisps": false},
		"server": {"port": 3000, "host": "0.0.0.0"},
		"refreshInterval": 5000
	}`
	os.WriteFile(configPath, []byte(data), 0644)

	cfg := loadConfig()

	if cfg.Filters.HideSystemBeads {
		t.Error("expected HideSystemBeads false from file")
	}
	if cfg.Server.Port != 3000 {
		t.Errorf("expected port 3000, got %d", cfg.Server.Port)
	}
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("expected host 0.0.0.0, got %q", cfg.Server.Host)
	}
	if cfg.RefreshInterval != 5000 {
		t.Errorf("expected refreshInterval 5000, got %d", cfg.RefreshInterval)
	}
}

func TestSaveAndLoadConfig(t *testing.T) {
	origPath := configPath
	defer func() { configPath = origPath }()

	configPath = filepath.Join(t.TempDir(), "config.json")

	cfg := Config{
		Filters: Filters{
			HideSystemBeads:      true,
			HideEvents:           false,
			HideRigIdentity:      true,
			HideMaintenanceWisps: false,
		},
		Server:          ServerConfig{Port: 4444, Host: "127.0.0.1"},
		RefreshInterval: 10000,
	}

	if err := saveConfig(cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	loaded := loadConfig()
	if loaded.Server.Port != 4444 {
		t.Errorf("round-trip port: got %d want 4444", loaded.Server.Port)
	}
	if loaded.Server.Host != "127.0.0.1" {
		t.Errorf("round-trip host: got %q want 127.0.0.1", loaded.Server.Host)
	}
	if loaded.RefreshInterval != 10000 {
		t.Errorf("round-trip refreshInterval: got %d want 10000", loaded.RefreshInterval)
	}
	if !loaded.Filters.HideSystemBeads {
		t.Error("round-trip HideSystemBeads should be true")
	}
	if loaded.Filters.HideEvents {
		t.Error("round-trip HideEvents should be false")
	}
}

// HTTP handler tests

func TestHandleHealth(t *testing.T) {
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	handleHealth(w, req)

	resp := w.Result()
	if resp.StatusCode != 200 {
		t.Errorf("health status = %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("health content-type = %q, want application/json", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("health response not valid JSON: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("health status field = %v, want ok", result["status"])
	}
	if result["engine"] != "go" {
		t.Errorf("health engine field = %v, want go", result["engine"])
	}
}

func TestHandleIndex(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	handleIndex(w, req)

	resp := w.Result()
	if resp.StatusCode != 200 {
		t.Errorf("index status = %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("index content-type = %q, want text/html", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Rigradar") {
		t.Error("index response should contain 'Rigradar'")
	}
}

func TestHandleGetConfig(t *testing.T) {
	origPath := configPath
	defer func() { configPath = origPath }()

	configPath = filepath.Join(t.TempDir(), "config.json")
	saveConfig(Config{
		Filters:         Filters{HideSystemBeads: true},
		Server:          ServerConfig{Port: 9292, Host: "localhost"},
		RefreshInterval: 30000,
	})

	req := httptest.NewRequest("GET", "/api/config", nil)
	w := httptest.NewRecorder()

	handleGetConfig(w, req)

	resp := w.Result()
	if resp.StatusCode != 200 {
		t.Errorf("get config status = %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var cfg Config
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("config response not valid JSON: %v", err)
	}
	if cfg.Server.Port != 9292 {
		t.Errorf("config port = %d, want 9292", cfg.Server.Port)
	}
}

func TestHandlePostConfig(t *testing.T) {
	origPath := configPath
	defer func() { configPath = origPath }()

	configPath = filepath.Join(t.TempDir(), "config.json")
	saveConfig(Config{
		Filters:         Filters{HideSystemBeads: true, HideEvents: true},
		Server:          ServerConfig{Port: 9292, Host: "localhost"},
		RefreshInterval: 30000,
	})

	// POST updated config
	payload := `{"server":{"port":5555,"host":"0.0.0.0"},"filters":{"hideSystemBeads":false,"hideEvents":false,"hideRigIdentity":false,"hideMaintenanceWisps":false}}`
	req := httptest.NewRequest("POST", "/api/config", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handlePostConfig(w, req)

	resp := w.Result()
	if resp.StatusCode != 200 {
		t.Errorf("post config status = %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var cfg Config
	json.Unmarshal(body, &cfg)
	if cfg.Server.Port != 5555 {
		t.Errorf("updated port = %d, want 5555", cfg.Server.Port)
	}
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("updated host = %q, want 0.0.0.0", cfg.Server.Host)
	}
	if cfg.Filters.HideSystemBeads {
		t.Error("updated HideSystemBeads should be false")
	}

	// Verify persistence
	loaded := loadConfig()
	if loaded.Server.Port != 5555 {
		t.Errorf("persisted port = %d, want 5555", loaded.Server.Port)
	}
}

func TestHandlePostConfigBadJSON(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/config", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handlePostConfig(w, req)

	resp := w.Result()
	if resp.StatusCode != 400 {
		t.Errorf("bad json status = %d, want 400", resp.StatusCode)
	}
}

func TestCORSMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	handler := corsMiddleware(inner)

	// Test OPTIONS preflight
	req := httptest.NewRequest("OPTIONS", "/api/beads", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != 204 {
		t.Errorf("CORS OPTIONS status = %d, want 204", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Error("CORS OPTIONS missing Access-Control-Allow-Origin: *")
	}
	if resp.Header.Get("Access-Control-Allow-Methods") == "" {
		t.Error("CORS OPTIONS missing Access-Control-Allow-Methods")
	}

	// Test normal request passes through
	req = httptest.NewRequest("GET", "/api/beads", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp = w.Result()
	if resp.StatusCode != 200 {
		t.Errorf("CORS GET status = %d, want 200", resp.StatusCode)
	}
}

func TestSendJSON(t *testing.T) {
	w := httptest.NewRecorder()
	sendJSON(w, map[string]string{"key": "val"}, 200)

	resp := w.Result()
	if resp.StatusCode != 200 {
		t.Errorf("sendJSON status = %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("sendJSON content-type = %q, want application/json", ct)
	}
	cors := resp.Header.Get("Access-Control-Allow-Origin")
	if cors != "*" {
		t.Errorf("sendJSON CORS = %q, want *", cors)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("sendJSON not valid JSON: %v", err)
	}
	if result["key"] != "val" {
		t.Errorf("sendJSON body key = %q, want val", result["key"])
	}
}

func TestSendError(t *testing.T) {
	w := httptest.NewRecorder()
	sendError(w, "something broke", 500)

	resp := w.Result()
	if resp.StatusCode != 500 {
		t.Errorf("sendError status = %d, want 500", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]string
	json.Unmarshal(body, &result)
	if result["error"] != "something broke" {
		t.Errorf("sendError body = %q, want 'something broke'", result["error"])
	}
}

// Routing integration test using the full mux
func TestRouting(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", handleIndex)
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /api/config", handleGetConfig)

	handler := corsMiddleware(mux)

	tests := []struct {
		method string
		path   string
		status int
		check  func(t *testing.T, body []byte)
	}{
		{
			method: "GET",
			path:   "/",
			status: 200,
			check: func(t *testing.T, body []byte) {
				if !strings.Contains(string(body), "Rigradar") {
					t.Error("index should contain Rigradar")
				}
			},
		},
		{
			method: "GET",
			path:   "/health",
			status: 200,
			check: func(t *testing.T, body []byte) {
				var m map[string]any
				json.Unmarshal(body, &m)
				if m["status"] != "ok" {
					t.Error("health should return status ok")
				}
			},
		},
		{
			method: "OPTIONS",
			path:   "/api/beads",
			status: 204,
			check:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			resp := w.Result()
			if resp.StatusCode != tt.status {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.status)
			}

			if tt.check != nil {
				body, _ := io.ReadAll(resp.Body)
				tt.check(t, body)
			}
		})
	}
}

func TestHandleBeadDetailMissingID(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/bead/", nil)
	w := httptest.NewRecorder()

	handleBeadDetail(w, req)

	resp := w.Result()
	if resp.StatusCode != 400 {
		t.Errorf("bead detail missing id status = %d, want 400", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]string
	json.Unmarshal(body, &result)
	if result["error"] != "missing bead id" {
		t.Errorf("expected 'missing bead id', got %q", result["error"])
	}
}

func TestBuildPrefixMapEmpty(t *testing.T) {
	// Save/restore globals
	origRoot := townRoot
	defer func() { townRoot = origRoot }()

	townRoot = t.TempDir()

	m := buildPrefixMap()
	// Should always have "hq" key
	if _, ok := m["hq"]; !ok {
		t.Error("buildPrefixMap should always include 'hq' key")
	}
}

func TestBuildPrefixMapWithRig(t *testing.T) {
	origRoot := townRoot
	defer func() { townRoot = origRoot }()

	townRoot = t.TempDir()

	// Create a rig with beads
	rigBeads := filepath.Join(townRoot, "myrig", ".beads")
	os.MkdirAll(rigBeads, 0755)
	// Create beads.db file (required for detection)
	os.WriteFile(filepath.Join(rigBeads, "beads.db"), []byte(""), 0644)
	// Create config.json with prefix
	os.WriteFile(filepath.Join(rigBeads, "config.json"), []byte(`{"prefix":"mr"}`), 0644)

	m := buildPrefixMap()

	// Should have hq, myrig, and mr prefix
	if _, ok := m["hq"]; !ok {
		t.Error("should have hq key")
	}
	if _, ok := m["myrig"]; !ok {
		t.Error("should have myrig key from directory name")
	}
	if _, ok := m["mr"]; !ok {
		t.Error("should have mr key from config prefix")
	}
	if m["mr"] != rigBeads {
		t.Errorf("mr prefix should point to %q, got %q", rigBeads, m["mr"])
	}
}
