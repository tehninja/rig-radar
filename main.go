package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

//go:embed index.html
var indexHTML []byte

type Config struct {
	Filters         Filters      `json:"filters"`
	Server          ServerConfig `json:"server"`
	RefreshInterval int          `json:"refreshInterval"`
}

type Filters struct {
	HideSystemBeads      bool `json:"hideSystemBeads"`
	HideEvents           bool `json:"hideEvents"`
	HideRigIdentity      bool `json:"hideRigIdentity"`
	HideMaintenanceWisps bool `json:"hideMaintenanceWisps"`
}

type ServerConfig struct {
	Port int    `json:"port"`
	Host string `json:"host"`
}

var (
	configPath string
	townRoot   string
	prefixMap  map[string]string
	configMu   sync.RWMutex
)

func init() {
	exeDir, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	configPath = filepath.Join(exeDir, "config.json")

	// Walk up to find town root (parent of rig dir).
	// The Node.js version does: path.resolve(crewDir, '..', '..')  then '..'
	// We approximate: look for a directory containing .beads/ or multiple rig dirs.
	// For simplicity, use the same heuristic: go up from cwd looking for the town root.
	townRoot = findTownRoot(exeDir)
	prefixMap = buildPrefixMap()
}

func findTownRoot(start string) string {
	// Walk up looking for a directory that has subdirectories with .beads/ in them,
	// or that has a .gastown marker.
	dir := start
	for i := 0; i < 10; i++ {
		// Check for .gastown marker or mayor/ directory
		if _, err := os.Stat(filepath.Join(dir, "mayor")); err == nil {
			return dir
		}
		if _, err := os.Stat(filepath.Join(dir, ".gastown")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// Fallback: assume 3 levels up from a polecat dir, or just use start
	// Typical: /town/rig/polecats/name/project -> town is 4 up
	candidate := filepath.Join(start, "..", "..", "..", "..")
	if abs, err := filepath.Abs(candidate); err == nil {
		if _, err := os.Stat(filepath.Join(abs, "mayor")); err == nil {
			return abs
		}
	}
	return start
}

// buildRigPrefixNameMap reads routes.jsonl and returns a prefix -> rig name mapping
// (for frontend display, e.g. "ri" -> "rigradar")
func buildRigPrefixNameMap() map[string]string {
	routesPath := filepath.Join(townRoot, ".beads", "routes.jsonl")
	m := make(map[string]string)

	data, err := os.ReadFile(routesPath)
	if err != nil {
		return m
	}

	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var route struct {
			Prefix string `json:"prefix"`
			Path   string `json:"path"`
		}
		if json.Unmarshal([]byte(line), &route) != nil {
			continue
		}
		prefix := strings.TrimSuffix(route.Prefix, "-")
		key := prefix
		if idx := strings.Index(prefix, "-"); idx > 0 {
			key = prefix[:idx]
		}
		rigName := route.Path
		if rigName == "." {
			rigName = "town"
		}
		if _, exists := m[key]; !exists {
			m[key] = rigName
		}
	}

	return m
}

func buildPrefixMap() map[string]string {
	townBeadsDir := filepath.Join(townRoot, ".beads")
	m := map[string]string{"hq": townBeadsDir}

	// Use routes.jsonl for prefix resolution
	routesPath := filepath.Join(townBeadsDir, "routes.jsonl")
	data, err := os.ReadFile(routesPath)
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var route struct {
				Prefix string `json:"prefix"`
				Path   string `json:"path"`
			}
			if json.Unmarshal([]byte(line), &route) != nil {
				continue
			}
			prefix := strings.TrimSuffix(route.Prefix, "-")
			rigPath := route.Path
			beadsDir := townBeadsDir
			if rigPath != "." {
				beadsDir = filepath.Join(townRoot, rigPath, ".beads")
			}
			m[prefix] = beadsDir
			// Also map first segment for multi-segment prefixes
			if idx := strings.Index(prefix, "-"); idx > 0 {
				firstSeg := prefix[:idx]
				if _, exists := m[firstSeg]; !exists {
					m[firstSeg] = beadsDir
				}
			}
			if rigPath != "." {
				m[rigPath] = beadsDir
			}
		}
	}

	// Also scan for rig directories (fallback)
	entries, err := os.ReadDir(townRoot)
	if err != nil {
		return m
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		beadsDir := filepath.Join(townRoot, e.Name(), ".beads")
		dbPath := filepath.Join(beadsDir, "beads.db")
		if _, err := os.Stat(dbPath); err != nil {
			continue
		}
		if _, exists := m[e.Name()]; !exists {
			m[e.Name()] = beadsDir
		}
	}

	return m
}

func beadsDirForID(beadID string) string {
	dash := strings.Index(beadID, "-")
	if dash > 0 {
		prefix := beadID[:dash]
		if dir, ok := prefixMap[prefix]; ok {
			return dir
		}
	}
	return filepath.Join(townRoot, ".beads")
}

func loadConfig() Config {
	cfg := Config{
		Filters: Filters{
			HideSystemBeads:      true,
			HideEvents:           true,
			HideRigIdentity:      true,
			HideMaintenanceWisps: true,
		},
		Server:          ServerConfig{Port: 9292, Host: "localhost"},
		RefreshInterval: 30000,
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return cfg
	}
	json.Unmarshal(data, &cfg)
	return cfg
}

func saveConfig(cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, append(data, '\n'), 0644)
}

func execCmd(name string, args []string, env map[string]string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = townRoot

	if len(env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}

	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s %s exited %d: %s", name, strings.Join(args, " "), exitErr.ExitCode(), string(exitErr.Stderr))
		}
		return nil, err
	}

	// Try to parse as JSON; if it fails, wrap as a JSON string
	out = []byte(strings.TrimSpace(string(out)))
	if json.Valid(out) {
		return json.RawMessage(out), nil
	}
	quoted, _ := json.Marshal(string(out))
	return json.RawMessage(quoted), nil
}

func sendJSON(w http.ResponseWriter, data any, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func sendError(w http.ResponseWriter, msg string, status int) {
	sendJSON(w, map[string]string{"error": msg}, status)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, map[string]any{
		"status": "ok",
		"town":   townRoot,
		"engine": "go",
	}, http.StatusOK)
}

func handleReady(w http.ResponseWriter, r *http.Request) {
	data, err := execCmd("gt", []string{"ready", "--json"}, nil)
	if err != nil {
		sendError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write(data)
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	data, err := execCmd("gt", []string{"status", "--json"}, nil)
	if err != nil {
		sendError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Enrich with rig prefix mapping for frontend display
	var statusObj map[string]json.RawMessage
	if json.Unmarshal(data, &statusObj) == nil {
		prefixes := buildRigPrefixNameMap()
		prefixJSON, _ := json.Marshal(prefixes)
		statusObj["rigPrefixes"] = json.RawMessage(prefixJSON)
		enriched, _ := json.Marshal(statusObj)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write(enriched)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write(data)
}

func handleBeads(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	btype := r.URL.Query().Get("type")

	// Collect unique bead dirs
	dirs := make(map[string]bool)
	for _, d := range prefixMap {
		dirs[d] = true
	}

	type result struct {
		data json.RawMessage
		err  error
	}

	ch := make(chan result, len(dirs))
	for dir := range dirs {
		go func(d string) {
			args := []string{"list", "--json"}
			if status != "" {
				args = append(args, "--status="+status)
			}
			if btype != "" {
				args = append(args, "--type="+btype)
			}
			data, err := execCmd("bd", args, map[string]string{"BEADS_DIR": d})
			ch <- result{data, err}
		}(dir)
	}

	var allBeads []json.RawMessage
	for range dirs {
		res := <-ch
		if res.err != nil {
			continue
		}
		var arr []json.RawMessage
		if json.Unmarshal(res.data, &arr) == nil {
			allBeads = append(allBeads, arr...)
		}
	}

	if allBeads == nil {
		allBeads = []json.RawMessage{}
	}
	sendJSON(w, allBeads, http.StatusOK)
}

func handleBeadDetail(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/bead/")
	if id == "" {
		sendError(w, "missing bead id", http.StatusBadRequest)
		return
	}

	dir := beadsDirForID(id)
	data, err := execCmd("bd", []string{"show", id, "--json"}, map[string]string{"BEADS_DIR": dir})
	if err != nil {
		sendError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write(data)
}

func handleGetConfig(w http.ResponseWriter, r *http.Request) {
	configMu.RLock()
	cfg := loadConfig()
	configMu.RUnlock()
	sendJSON(w, cfg, http.StatusOK)
}

func handlePostConfig(w http.ResponseWriter, r *http.Request) {
	var body Config
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		sendError(w, err.Error(), http.StatusBadRequest)
		return
	}

	configMu.Lock()
	current := loadConfig()
	// Merge: body overwrites current where set
	if body.Server.Port != 0 {
		current.Server.Port = body.Server.Port
	}
	if body.Server.Host != "" {
		current.Server.Host = body.Server.Host
	}
	if body.RefreshInterval != 0 {
		current.RefreshInterval = body.RefreshInterval
	}
	// Filters: always overwrite from body since bools default to false
	current.Filters = body.Filters
	saveConfig(current)
	configMu.Unlock()

	sendJSON(w, current, http.StatusOK)
}

func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler"}
	default:
		cmd = "xdg-open"
	}
	args = append(args, url)
	exec.Command(cmd, args...).Start()
}

func main() {
	port := flag.Int("port", 0, "Server port (overrides config.json)")
	open := flag.Bool("open", false, "Open browser on start")
	flag.Parse()

	cfg := loadConfig()

	listenPort := cfg.Server.Port
	if *port != 0 {
		listenPort = *port
	}
	if listenPort == 0 {
		listenPort = 9292
	}

	host := cfg.Server.Host
	if host == "" {
		host = "localhost"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", handleIndex)
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /api/ready", handleReady)
	mux.HandleFunc("GET /api/status", handleStatus)
	mux.HandleFunc("GET /api/beads", handleBeads)
	mux.HandleFunc("GET /api/bead/", handleBeadDetail)
	mux.HandleFunc("GET /api/config", handleGetConfig)
	mux.HandleFunc("POST /api/config", handlePostConfig)

	addr := fmt.Sprintf("%s:%d", host, listenPort)
	server := &http.Server{
		Addr:         addr,
		Handler:      corsMiddleware(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	// Graceful shutdown on interrupt
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	fmt.Printf("Beadboard running at http://%s\n", addr)
	fmt.Printf("Town root: %s\n", townRoot)
	fmt.Printf("Engine: Go\n")

	if *open {
		openBrowser(fmt.Sprintf("http://%s", addr))
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
