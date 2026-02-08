package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestServer creates a full rigradar server with all routes for E2E testing.
func newTestServer() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", handleIndex)
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /api/ready", handleReady)
	mux.HandleFunc("GET /api/status", handleStatus)
	mux.HandleFunc("GET /api/beads", handleBeads)
	mux.HandleFunc("GET /api/bead/", handleBeadDetail)
	mux.HandleFunc("GET /api/config", handleGetConfig)
	mux.HandleFunc("POST /api/config", handlePostConfig)
	return httptest.NewServer(corsMiddleware(mux))
}

func get(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("reading body from %s: %v", url, err)
	}
	return resp, body
}

func postJSON(t *testing.T, url string, payload string) (*http.Response, []byte) {
	t.Helper()
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(url, "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("reading body from %s: %v", url, err)
	}
	return resp, body
}

// --- E2E: Health endpoint ---

func TestE2E_HealthEndpoint(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, body := get(t, ts.URL+"/health")

	if resp.StatusCode != 200 {
		t.Errorf("health status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("health content-type = %q, want application/json", ct)
	}
	if cors := resp.Header.Get("Access-Control-Allow-Origin"); cors != "*" {
		t.Errorf("health CORS = %q, want *", cors)
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("health body not valid JSON: %v\nbody: %s", err, body)
	}
	if result["status"] != "ok" {
		t.Errorf("health status field = %v, want ok", result["status"])
	}
	if result["engine"] != "go" {
		t.Errorf("health engine field = %v, want go", result["engine"])
	}
	if result["town"] == nil || result["town"] == "" {
		t.Error("health town field should not be empty")
	}
}

// --- E2E: Index page ---

func TestE2E_IndexPage(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, body := get(t, ts.URL+"/")

	if resp.StatusCode != 200 {
		t.Errorf("index status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("index content-type = %q, want text/html", ct)
	}

	html := string(body)

	// Core UI elements
	checks := []struct {
		name    string
		content string
	}{
		{"page title", "<title>Rigradar"},
		{"app name", "Rigradar"},
		{"sidebar", `class="sidebar"`},
		{"main panel", `class="main-panel"`},
		{"detail panel", `class="detail-panel"`},
		{"town overview", `id="townOverview"`},
		{"filter toggles", `id="filterToggles"`},
		{"rig list", `id="rigList"`},
		{"priority stats", `id="priorityStats"`},
		{"refresh button", `id="refreshBtn"`},
	}
	for _, c := range checks {
		if !strings.Contains(html, c.content) {
			t.Errorf("index missing %s: expected %q in HTML", c.name, c.content)
		}
	}

	// JavaScript state and API calls
	jsChecks := []struct {
		name    string
		content string
	}{
		{"state object", "let state ="},
		{"API fetch helper", "async function api("},
		{"filter logic", "function shouldHide("},
		{"bead rig resolver", "function beadRig("},
		{"group by rig", "function groupByRig("},
		{"render overview", "function renderOverview("},
		{"render filters", "function renderFilters("},
		{"render rig list", "function renderRigList("},
		{"render main", "function renderMain("},
		{"render detail", "function renderDetail("},
		{"select bead", "async function selectBead("},
		{"close detail", "function closeDetail("},
		{"copy command", "async function copyCmd("},
		{"load config", "async function loadConfig("},
		{"load status", "async function loadStatus("},
		{"load beads", "async function loadBeads("},
		{"refresh all", "async function refreshAll("},
		{"auto-refresh", "function startAutoRefresh("},
		{"config API call", "'/api/config'"},
		{"status API call", "'/api/status'"},
		{"beads API call", "'/api/beads"},
		{"bead detail API call", "/api/bead/"},
	}
	for _, c := range jsChecks {
		if !strings.Contains(html, c.content) {
			t.Errorf("index JS missing %s: expected %q", c.name, c.content)
		}
	}

	// CSS styling classes
	cssChecks := []string{
		".bead-card", ".bead-id", ".bead-title", ".bead-priority",
		".rig-item", ".rig-group", ".p-badge",
		".status-dot", ".detail-field", ".cmd-copy",
		"--bg-dark", "--accent",
	}
	for _, c := range cssChecks {
		if !strings.Contains(html, c) {
			t.Errorf("index CSS missing class/var: %q", c)
		}
	}
}

// --- E2E: Config endpoints ---

func TestE2E_ConfigGetAndPost(t *testing.T) {
	origPath := configPath
	defer func() { configPath = origPath }()
	configPath = t.TempDir() + "/test-config.json"

	// Write initial config
	saveConfig(Config{
		Filters: Filters{
			HideSystemBeads:      true,
			HideEvents:           true,
			HideRigIdentity:      true,
			HideMaintenanceWisps: true,
			HideHQBeads:          true,
		},
		Server:          ServerConfig{Port: 9292, Host: "localhost"},
		RefreshInterval: 30000,
	})

	ts := newTestServer()
	defer ts.Close()

	// GET config
	resp, body := get(t, ts.URL+"/api/config")
	if resp.StatusCode != 200 {
		t.Fatalf("GET config status = %d", resp.StatusCode)
	}

	var cfg Config
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("config not valid JSON: %v", err)
	}
	if cfg.Server.Port != 9292 {
		t.Errorf("config port = %d, want 9292", cfg.Server.Port)
	}
	if !cfg.Filters.HideSystemBeads {
		t.Error("config HideSystemBeads should be true")
	}
	if !cfg.Filters.HideHQBeads {
		t.Error("config HideHQBeads should be true")
	}
	if cfg.RefreshInterval != 30000 {
		t.Errorf("config refreshInterval = %d, want 30000", cfg.RefreshInterval)
	}

	// POST config - update filters and port
	payload := `{
		"server": {"port": 7777, "host": "0.0.0.0"},
		"filters": {
			"hideSystemBeads": false,
			"hideEvents": false,
			"hideRigIdentity": false,
			"hideMaintenanceWisps": false,
			"hideHQBeads": false
		},
		"refreshInterval": 5000
	}`
	resp, body = postJSON(t, ts.URL+"/api/config", payload)
	if resp.StatusCode != 200 {
		t.Fatalf("POST config status = %d, body: %s", resp.StatusCode, body)
	}

	var updated Config
	json.Unmarshal(body, &updated)
	if updated.Server.Port != 7777 {
		t.Errorf("updated port = %d, want 7777", updated.Server.Port)
	}
	if updated.Server.Host != "0.0.0.0" {
		t.Errorf("updated host = %q, want 0.0.0.0", updated.Server.Host)
	}
	if updated.Filters.HideSystemBeads {
		t.Error("updated HideSystemBeads should be false")
	}
	if updated.Filters.HideHQBeads {
		t.Error("updated HideHQBeads should be false")
	}
	if updated.RefreshInterval != 5000 {
		t.Errorf("updated refreshInterval = %d, want 5000", updated.RefreshInterval)
	}

	// Verify persistence: GET again
	resp, body = get(t, ts.URL+"/api/config")
	var persisted Config
	json.Unmarshal(body, &persisted)
	if persisted.Server.Port != 7777 {
		t.Errorf("persisted port = %d, want 7777", persisted.Server.Port)
	}
	if persisted.Filters.HideSystemBeads {
		t.Error("persisted HideSystemBeads should be false")
	}
}

func TestE2E_ConfigPostBadJSON(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, _ := postJSON(t, ts.URL+"/api/config", "not valid json")
	if resp.StatusCode != 400 {
		t.Errorf("bad config POST status = %d, want 400", resp.StatusCode)
	}
}

func TestE2E_ConfigPostPartialUpdate(t *testing.T) {
	origPath := configPath
	defer func() { configPath = origPath }()
	configPath = t.TempDir() + "/test-config.json"

	saveConfig(Config{
		Filters:         Filters{HideSystemBeads: true, HideEvents: true, HideHQBeads: true},
		Server:          ServerConfig{Port: 9292, Host: "localhost"},
		RefreshInterval: 30000,
	})

	ts := newTestServer()
	defer ts.Close()

	// Only update port, keep other settings
	resp, body := postJSON(t, ts.URL+"/api/config", `{"server":{"port":8888}}`)
	if resp.StatusCode != 200 {
		t.Fatalf("partial config POST status = %d", resp.StatusCode)
	}

	var cfg Config
	json.Unmarshal(body, &cfg)
	if cfg.Server.Port != 8888 {
		t.Errorf("partial update port = %d, want 8888", cfg.Server.Port)
	}
	// Host should be preserved from original
	if cfg.Server.Host != "localhost" {
		t.Errorf("partial update host = %q, want localhost (preserved)", cfg.Server.Host)
	}
}

// --- E2E: CORS on all endpoints ---

func TestE2E_CORSPreflight(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	endpoints := []string{"/api/beads", "/api/config", "/api/status", "/api/ready", "/health"}
	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			client := &http.Client{Timeout: 10 * time.Second}
			req, _ := http.NewRequest("OPTIONS", ts.URL+ep, nil)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("OPTIONS %s: %v", ep, err)
			}
			resp.Body.Close()

			if resp.StatusCode != 204 {
				t.Errorf("OPTIONS %s status = %d, want 204", ep, resp.StatusCode)
			}
			if cors := resp.Header.Get("Access-Control-Allow-Origin"); cors != "*" {
				t.Errorf("OPTIONS %s CORS = %q, want *", ep, cors)
			}
			if methods := resp.Header.Get("Access-Control-Allow-Methods"); methods == "" {
				t.Errorf("OPTIONS %s missing Allow-Methods", ep)
			}
		})
	}
}

func TestE2E_CORSOnGetResponses(t *testing.T) {
	origPath := configPath
	defer func() { configPath = origPath }()
	configPath = t.TempDir() + "/test-config.json"
	saveConfig(loadConfig())

	ts := newTestServer()
	defer ts.Close()

	// Test CORS headers on normal GET requests
	endpoints := []string{"/health", "/api/config"}
	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			resp, _ := get(t, ts.URL+ep)
			if cors := resp.Header.Get("Access-Control-Allow-Origin"); cors != "*" {
				t.Errorf("GET %s CORS = %q, want *", ep, cors)
			}
		})
	}
}

// --- E2E: Bead detail edge cases ---

func TestE2E_BeadDetailMissingID(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, body := get(t, ts.URL+"/api/bead/")
	if resp.StatusCode != 400 {
		t.Errorf("bead detail empty id status = %d, want 400", resp.StatusCode)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["error"] != "missing bead id" {
		t.Errorf("bead detail error = %q, want 'missing bead id'", result["error"])
	}
}

// --- E2E: Live data tests (require Gas Town environment) ---

func TestE2E_LiveBeadsList(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	// Test beads endpoint returns valid JSON array
	resp, body := get(t, ts.URL+"/api/beads")
	if resp.StatusCode != 200 {
		t.Fatalf("beads status = %d, body: %s", resp.StatusCode, body)
	}

	var beads []map[string]any
	if err := json.Unmarshal(body, &beads); err != nil {
		t.Fatalf("beads not valid JSON array: %v\nbody: %s", err, string(body))
	}

	t.Logf("Found %d beads total", len(beads))
}

func TestE2E_LiveBeadsWithStatusFilter(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	statuses := []string{"open", "in_progress", "closed"}
	totalFromFiltered := 0

	for _, s := range statuses {
		t.Run("status="+s, func(t *testing.T) {
			resp, body := get(t, ts.URL+"/api/beads?status="+s)
			if resp.StatusCode != 200 {
				t.Fatalf("beads status=%s returned %d", s, resp.StatusCode)
			}

			var beads []map[string]any
			if err := json.Unmarshal(body, &beads); err != nil {
				t.Fatalf("beads status=%s not valid JSON: %v", s, err)
			}

			t.Logf("status=%s: %d beads", s, len(beads))
			totalFromFiltered += len(beads)

			// Verify all returned beads have the requested status
			for _, b := range beads {
				if bs, ok := b["status"].(string); ok && bs != s {
					t.Errorf("bead %v has status %q, expected %q", b["id"], bs, s)
				}
			}
		})
	}
}

func TestE2E_LiveBeadDataShape(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, body := get(t, ts.URL+"/api/beads?status=open")
	if resp.StatusCode != 200 {
		t.Skipf("beads endpoint returned %d, skipping shape check", resp.StatusCode)
	}

	var beads []map[string]any
	json.Unmarshal(body, &beads)

	if len(beads) == 0 {
		t.Skip("no open beads to verify shape")
	}

	// Check first bead has expected fields
	bead := beads[0]
	requiredFields := []string{"id", "title", "status"}
	for _, f := range requiredFields {
		if _, ok := bead[f]; !ok {
			t.Errorf("bead missing required field %q: %v", f, bead)
		}
	}

	// ID should have a prefix-dash pattern
	if id, ok := bead["id"].(string); ok {
		if !strings.Contains(id, "-") {
			t.Errorf("bead id %q doesn't match expected prefix-id pattern", id)
		}
	}

	t.Logf("Sample bead: id=%v title=%v status=%v", bead["id"], bead["title"], bead["status"])
}

func TestE2E_LiveStatus(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, body := get(t, ts.URL+"/api/status")

	// gt status may timeout in some environments (15s cmd timeout)
	if resp.StatusCode == 500 {
		var errResp map[string]any
		json.Unmarshal(body, &errResp)
		t.Skipf("status endpoint returned 500 (gt status may be unavailable): %v", errResp["error"])
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status endpoint returned %d: %s", resp.StatusCode, body)
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("status not valid JSON: %v", err)
	}

	// Should have rigPrefixes (enrichment from Go server)
	if _, ok := result["rigPrefixes"]; !ok {
		t.Error("status response missing rigPrefixes enrichment")
	}

	// rigPrefixes should be an object
	if prefixes, ok := result["rigPrefixes"].(map[string]any); ok {
		t.Logf("rigPrefixes has %d entries", len(prefixes))
		for k, v := range prefixes {
			t.Logf("  prefix %q -> %v", k, v)
		}
	}

	// Should have rigs array
	if rigs, ok := result["rigs"].([]any); ok {
		t.Logf("Found %d rigs", len(rigs))
		for _, r := range rigs {
			if rig, ok := r.(map[string]any); ok {
				t.Logf("  rig: %v", rig["name"])
			}
		}
	}
}

func TestE2E_LiveReady(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, body := get(t, ts.URL+"/api/ready")
	if resp.StatusCode != 200 {
		// ready endpoint might fail if gt ready isn't available, that's ok
		t.Logf("ready endpoint returned %d (may not have gt ready): %s", resp.StatusCode, body)
		return
	}

	// Should be valid JSON
	if !json.Valid(body) {
		t.Errorf("ready response not valid JSON: %s", body)
	}
	t.Logf("ready response: %s", truncate(string(body), 200))
}

func TestE2E_LiveBeadDetail(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	// Get beads and find one that bd show can resolve
	// Prefer non-HQ beads since HQ prefix resolution depends on town root discovery
	resp, body := get(t, ts.URL+"/api/beads?status=open")
	if resp.StatusCode != 200 {
		t.Skip("can't get beads list")
	}

	var beads []map[string]any
	json.Unmarshal(body, &beads)
	if len(beads) == 0 {
		resp, body = get(t, ts.URL+"/api/beads?status=in_progress")
		json.Unmarshal(body, &beads)
	}
	if len(beads) == 0 {
		t.Skip("no beads available to test detail")
	}

	// Pick a non-HQ bead if possible (better prefix resolution in test env)
	var beadID string
	for _, b := range beads {
		id, ok := b["id"].(string)
		if !ok {
			continue
		}
		if !strings.HasPrefix(id, "hq-") {
			beadID = id
			break
		}
	}
	if beadID == "" {
		beadID = beads[0]["id"].(string)
	}

	t.Logf("Testing bead detail for: %s", beadID)

	resp, body = get(t, ts.URL+"/api/bead/"+beadID)

	// bd show may fail for some beads depending on BEADS_DIR resolution
	if resp.StatusCode == 500 {
		var errResp map[string]any
		json.Unmarshal(body, &errResp)
		t.Logf("bead detail returned 500 (BEADS_DIR resolution): %v", errResp["error"])
		// Verify error response is well-formed JSON with error field
		if _, ok := errResp["error"]; !ok {
			t.Error("error response missing 'error' field")
		}
		return
	}
	if resp.StatusCode != 200 {
		t.Fatalf("bead detail for %s returned %d: %s", beadID, resp.StatusCode, body)
	}

	// Parse detail - could be object or array
	var detail any
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("bead detail not valid JSON: %v", err)
	}

	// Extract the bead object
	var beadObj map[string]any
	switch v := detail.(type) {
	case map[string]any:
		beadObj = v
	case []any:
		if len(v) > 0 {
			if m, ok := v[0].(map[string]any); ok {
				beadObj = m
			}
		}
	}

	if beadObj == nil {
		t.Fatalf("couldn't parse bead detail as object: %s", truncate(string(body), 200))
	}

	// Verify detail has the same ID
	if detailID, ok := beadObj["id"].(string); ok {
		if detailID != beadID {
			t.Errorf("detail id = %q, want %q", detailID, beadID)
		}
	}

	// Detail should have title
	if _, ok := beadObj["title"]; !ok {
		t.Error("bead detail missing title")
	}

	t.Logf("Bead detail: id=%v title=%v status=%v", beadObj["id"], beadObj["title"], beadObj["status"])
}

// --- E2E: Frontend filter logic verification ---

func TestE2E_FilterLogicInHTML(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	_, body := get(t, ts.URL+"/")
	html := string(body)

	// Verify filter toggle definitions
	filterKeys := []string{
		"hideHQBeads",
		"hideSystemBeads",
		"hideEvents",
		"hideRigIdentity",
		"hideMaintenanceWisps",
	}
	for _, f := range filterKeys {
		if !strings.Contains(html, f) {
			t.Errorf("HTML missing filter key %q", f)
		}
	}

	// Verify filter labels in UI
	filterLabels := []string{
		"Hide HQ beads",
		"Hide system beads",
		"Hide events",
		"Hide rig identity beads",
		"Hide maintenance wisps",
	}
	for _, l := range filterLabels {
		if !strings.Contains(html, l) {
			t.Errorf("HTML missing filter label %q", l)
		}
	}
}

// --- E2E: Navigation and routing ---

func TestE2E_AllRoutesAccessible(t *testing.T) {
	origPath := configPath
	defer func() { configPath = origPath }()
	configPath = t.TempDir() + "/test-config.json"
	saveConfig(loadConfig())

	ts := newTestServer()
	defer ts.Close()

	routes := []struct {
		method     string
		path       string
		wantStatus int
		wantCT     string
	}{
		{"GET", "/", 200, "text/html"},
		{"GET", "/health", 200, "application/json"},
		{"GET", "/api/config", 200, "application/json"},
	}

	client := &http.Client{Timeout: 30 * time.Second}
	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			req, _ := http.NewRequest(r.method, ts.URL+r.path, nil)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", r.method, r.path, err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode != r.wantStatus {
				t.Errorf("status = %d, want %d (body: %s)", resp.StatusCode, r.wantStatus, truncate(string(body), 100))
			}
			if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, r.wantCT) {
				t.Errorf("content-type = %q, want %q", ct, r.wantCT)
			}
		})
	}
}

// --- E2E: Bead detail with nonexistent ID ---

func TestE2E_BeadDetailNonexistent(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, body := get(t, ts.URL+"/api/bead/nonexistent-99999")
	// Should return 500 (bd show fails) or error JSON
	if resp.StatusCode == 200 {
		// If it returns 200, the body should still be valid JSON
		if !json.Valid(body) {
			t.Errorf("nonexistent bead returned 200 but invalid JSON")
		}
	} else if resp.StatusCode == 500 {
		var result map[string]any
		if err := json.Unmarshal(body, &result); err == nil {
			if _, ok := result["error"]; !ok {
				t.Error("error response missing 'error' field")
			}
		}
	}
	// Any other status is acceptable too - just shouldn't panic
}

// --- E2E: Beads endpoint with type filter ---

func TestE2E_LiveBeadsWithTypeFilter(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	types := []string{"task", "epic", "event"}
	for _, bt := range types {
		t.Run("type="+bt, func(t *testing.T) {
			resp, body := get(t, ts.URL+"/api/beads?type="+bt)
			if resp.StatusCode != 200 {
				t.Fatalf("beads type=%s returned %d", bt, resp.StatusCode)
			}

			var beads []map[string]any
			if err := json.Unmarshal(body, &beads); err != nil {
				t.Fatalf("beads type=%s not valid JSON: %v", bt, err)
			}
			t.Logf("type=%s: %d beads", bt, len(beads))
		})
	}
}

// --- E2E: Combined status + type filter ---

func TestE2E_LiveBeadsCombinedFilter(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, body := get(t, ts.URL+"/api/beads?status=open&type=task")
	if resp.StatusCode != 200 {
		t.Fatalf("combined filter returned %d", resp.StatusCode)
	}

	var beads []map[string]any
	if err := json.Unmarshal(body, &beads); err != nil {
		t.Fatalf("combined filter not valid JSON: %v", err)
	}
	t.Logf("open tasks: %d beads", len(beads))
}

// --- E2E: Config filter defaults ---

func TestE2E_ConfigDefaultFilters(t *testing.T) {
	origPath := configPath
	defer func() { configPath = origPath }()
	configPath = t.TempDir() + "/nonexistent-config.json"

	ts := newTestServer()
	defer ts.Close()

	resp, body := get(t, ts.URL+"/api/config")
	if resp.StatusCode != 200 {
		t.Fatalf("config status = %d", resp.StatusCode)
	}

	var cfg Config
	json.Unmarshal(body, &cfg)

	// All filter defaults should be true
	if !cfg.Filters.HideSystemBeads {
		t.Error("default HideSystemBeads should be true")
	}
	if !cfg.Filters.HideEvents {
		t.Error("default HideEvents should be true")
	}
	if !cfg.Filters.HideRigIdentity {
		t.Error("default HideRigIdentity should be true")
	}
	if !cfg.Filters.HideMaintenanceWisps {
		t.Error("default HideMaintenanceWisps should be true")
	}
	if !cfg.Filters.HideHQBeads {
		t.Error("default HideHQBeads should be true")
	}
	if cfg.Server.Port != 9292 {
		t.Errorf("default port = %d, want 9292", cfg.Server.Port)
	}
	if cfg.Server.Host != "localhost" {
		t.Errorf("default host = %q, want localhost", cfg.Server.Host)
	}
}

// --- E2E: Multiple concurrent requests ---

func TestE2E_ConcurrentRequests(t *testing.T) {
	origPath := configPath
	defer func() { configPath = origPath }()
	configPath = t.TempDir() + "/test-config.json"
	saveConfig(loadConfig())

	ts := newTestServer()
	defer ts.Close()

	// Fire multiple requests concurrently to test for race conditions
	type result struct {
		path   string
		status int
		err    error
	}

	paths := []string{"/health", "/api/config", "/", "/health", "/api/config"}
	ch := make(chan result, len(paths))

	for _, p := range paths {
		go func(path string) {
			client := &http.Client{Timeout: 30 * time.Second}
			resp, err := client.Get(ts.URL + path)
			if err != nil {
				ch <- result{path, 0, err}
				return
			}
			io.ReadAll(resp.Body)
			resp.Body.Close()
			ch <- result{path, resp.StatusCode, nil}
		}(p)
	}

	for range paths {
		r := <-ch
		if r.err != nil {
			t.Errorf("concurrent %s: %v", r.path, r.err)
		} else if r.status != 200 {
			t.Errorf("concurrent %s status = %d, want 200", r.path, r.status)
		}
	}
}

// --- E2E: Verify rig prefix mapping integration ---

func TestE2E_RigPrefixMapping(t *testing.T) {
	// Test that buildRigPrefixNameMap and buildPrefixMap work together
	prefixes := buildRigPrefixNameMap()
	t.Logf("Rig prefix name map has %d entries", len(prefixes))
	for k, v := range prefixes {
		t.Logf("  %q -> %q", k, v)
	}

	t.Logf("Prefix map has %d entries", len(prefixMap))
	for k, v := range prefixMap {
		t.Logf("  %q -> %q", k, v)
	}

	// Verify hq is always present in prefixMap
	if _, ok := prefixMap["hq"]; !ok {
		t.Error("prefixMap missing 'hq' key")
	}
}

// --- E2E: Frontend display correctness ---

func TestE2E_FrontendPriorityBadges(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	_, body := get(t, ts.URL+"/")
	html := string(body)

	// Verify priority badge CSS classes
	for i := 0; i <= 4; i++ {
		cls := fmt.Sprintf(".p-badge.p%d", i)
		if !strings.Contains(html, cls) {
			t.Errorf("missing priority badge class %q", cls)
		}
	}

	// Verify priority labels in JS
	for i := 0; i <= 4; i++ {
		label := fmt.Sprintf("'P%d'", i)
		if !strings.Contains(html, label) {
			t.Errorf("missing priority label %s in JS", label)
		}
	}
}

func TestE2E_FrontendStatusDots(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	_, body := get(t, ts.URL+"/")
	html := string(body)

	// Verify status dot CSS classes
	statusClasses := []string{".status-dot.open", ".status-dot.in_progress", ".status-dot.closed"}
	for _, cls := range statusClasses {
		if !strings.Contains(html, cls) {
			t.Errorf("missing status dot class %q", cls)
		}
	}
}

func TestE2E_FrontendEscapeFunction(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	_, body := get(t, ts.URL+"/")
	html := string(body)

	// HTML escape function should handle XSS vectors
	if !strings.Contains(html, "function esc(") {
		t.Error("missing HTML escape function")
	}
	// Should escape &, <, >, "
	for _, entity := range []string{"&amp;", "&lt;", "&gt;", "&quot;"} {
		if !strings.Contains(html, entity) {
			t.Errorf("esc() should handle %s", entity)
		}
	}
}

func TestE2E_FrontendDetailCommands(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	_, body := get(t, ts.URL+"/")
	html := string(body)

	// Verify expected commands in detail view
	commands := []string{"gt cat", "gt sling", "gt unsling", "bd close", "bd update"}
	for _, cmd := range commands {
		if !strings.Contains(html, cmd) {
			t.Errorf("detail view missing command %q", cmd)
		}
	}
}

// Helper
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
