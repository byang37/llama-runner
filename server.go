package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func setupRoutes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/settings",       handleSettings)
	mux.HandleFunc("/api/service/status", handleServiceStatus)
	mux.HandleFunc("/api/service/start",  handleServiceStart)
	mux.HandleFunc("/api/service/stop",   handleServiceStop)
	mux.HandleFunc("/api/models",         handleModels)
	mux.HandleFunc("/api/models/config",  handleModelConfig)
	mux.HandleFunc("/api/models/load",    handleModelLoad)
	mux.HandleFunc("/api/models/unload",  handleModelUnload)
	mux.HandleFunc("/api/status-stream",  handleStatusStream)
	mux.HandleFunc("/api/logs",           handleLogStream)
	mux.HandleFunc("/api/logs/clear",     handleLogClear)
	mux.HandleFunc("/api/io",             handleIOStream)
	mux.HandleFunc("/api/io/clear",       handleIOClear)
	mux.HandleFunc("/api/browse-folder",  handleBrowseFolder)
	mux.HandleFunc("/favicon.ico",         handleFavicon)
	mux.HandleFunc("/api/langs",           handleLangs)
	mux.HandleFunc("/v1/",               handleV1Proxy)

	return mux
}

func cors(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ─── Settings ─────────────────────────────────────────────────────────────────

func handleSettings(w http.ResponseWriter, r *http.Request) {
	cors(w)
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, loadAppSettings())
	case http.MethodPut:
		var s AppSettings
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			writeError(w, err.Error(), 400)
			return
		}
		if err := saveAppSettings(s); err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		// Regenerate presets.ini immediately so it reflects the new models dir.
		if s.ModelsDir != "" {
			if _, err := writePresetsINI(); err != nil {
				globalLogHub.Publish(fmt.Sprintf("[%s] WRN presets.ini write failed: %v", ts(), err))
			}
		}
		writeJSON(w, s)
	default:
		w.WriteHeader(405)
	}
}

// ─── Service ──────────────────────────────────────────────────────────────────

func handleServiceStatus(w http.ResponseWriter, r *http.Request) {
	cors(w)
	writeJSON(w, svc.Status())
}

func handleServiceStart(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	// Clear log and IO history so the new session starts clean.
	globalLogHub.Clear()
	globalIOHub.Clear()
	modelIOHubs.Lock()
	for _, h := range modelIOHubs.m {
		h.Clear()
	}
	modelIOHubs.Unlock()

	if err := svc.Start(loadAppSettings()); err != nil {
		writeError(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]string{"status": "started"})
}

func handleServiceStop(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	if err := svc.Stop(); err != nil {
		writeError(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]string{"status": "stopped"})
}

// ─── Models ───────────────────────────────────────────────────────────────────

func handleModels(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	settings := loadAppSettings()
	loaded := svc.QueryLoadedModels()
	models, err := scanModels(settings.ModelsDir, loaded)
	if err != nil {
		writeError(w, err.Error(), 500)
		return
	}
	if models == nil {
		models = []GGUFModel{}
	}
	writeJSON(w, models)
}

func handleModelConfig(w http.ResponseWriter, r *http.Request) {
	cors(w)
	switch r.Method {
	case http.MethodGet:
		modelID   := r.URL.Query().Get("id")
		modelPath := r.URL.Query().Get("path")
		mmproj    := r.URL.Query().Get("mmproj")
		inSubdir  := r.URL.Query().Get("subdir") == "1"
		if modelID == "" {
			writeError(w, "missing id", 400)
			return
		}
		writeJSON(w, loadModelConfig(modelPath, mmproj, modelID, inSubdir))
	case http.MethodPut, http.MethodPost:
		var cfg ModelConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeError(w, err.Error(), 400)
			return
		}
		if err := saveModelConfig(cfg); err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		if _, err := writePresetsINI(); err != nil {
			globalLogHub.Publish(fmt.Sprintf("[%s] WRN presets.ini update failed: %v", ts(), err))
		}
		writeJSON(w, cfg)
	default:
		w.WriteHeader(405)
	}
}

func handleModelLoad(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var cfg ModelConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, err.Error(), 400)
		return
	}
	_ = saveModelConfig(cfg)

	// Use alias as the model identifier when set; llama-server matches by
	// the preset section name which equals the alias.
	loadID := cfg.Alias
	if loadID == "" {
		loadID = cfg.ModelID
	}

	hub := getModelIOHub(cfg.ModelID)
	globalLogHub.Publish(fmt.Sprintf("[%s] SYS load id=%s", ts(), loadID))

	if err := svc.LoadModel(loadID, hub); err != nil {
		globalLogHub.Publish(fmt.Sprintf("[%s] ERR load failed id=%s: %v", ts(), loadID, err))
		writeError(w, err.Error(), 500)
		return
	}
	globalLogHub.Publish(fmt.Sprintf("[%s] SYS load complete id=%s", ts(), loadID))
	statusBroadcast()
	writeJSON(w, map[string]string{"status": "loaded"})
}

func handleModelUnload(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var body struct {
		ModelID string `json:"model_id"`
		Alias   string `json:"alias"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, err.Error(), 400)
		return
	}

	unloadID := body.Alias
	if unloadID == "" {
		unloadID = body.ModelID
	}

	hub := getModelIOHub(body.ModelID)
	globalLogHub.Publish(fmt.Sprintf("[%s] SYS unload id=%s", ts(), unloadID))

	if err := svc.UnloadModel(unloadID, hub); err != nil {
		globalLogHub.Publish(fmt.Sprintf("[%s] ERR unload failed id=%s: %v", ts(), unloadID, err))
		writeError(w, err.Error(), 500)
		return
	}
	globalLogHub.Publish(fmt.Sprintf("[%s] SYS unload complete id=%s", ts(), unloadID))
	statusBroadcast()
	writeJSON(w, map[string]string{"status": "unloaded"})
}

// ─── Status SSE ───────────────────────────────────────────────────────────────

func handleStatusStream(w http.ResponseWriter, r *http.Request) {
	cors(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(500)
		return
	}
	ch := statusHub.Subscribe()
	defer statusHub.Unsubscribe(ch)

	send := func() {
		data, _ := json.Marshal(svc.Status())
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
	send()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			send()
		case <-ticker.C:
			send()
		}
	}
}

// ─── Log SSE ──────────────────────────────────────────────────────────────────

func handleLogStream(w http.ResponseWriter, r *http.Request) {
	cors(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(500)
		return
	}
	ch := globalLogHub.Subscribe()
	defer globalLogHub.Unsubscribe(ch)
	for {
		select {
		case <-r.Context().Done():
			return
		case line, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(line)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func handleLogClear(w http.ResponseWriter, r *http.Request) {
	cors(w)
	globalLogHub.Clear()
	writeJSON(w, map[string]string{"status": "ok"})
}

// ─── IO SSE ───────────────────────────────────────────────────────────────────

func handleIOStream(w http.ResponseWriter, r *http.Request) {
	cors(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(500)
		return
	}
	modelID := r.URL.Query().Get("model")
	var hub *IOHub
	if modelID != "" {
		hub = getModelIOHub(modelID)
	} else {
		hub = globalIOHub
	}
	ch := hub.Subscribe()
	defer hub.Unsubscribe(ch)
	for {
		select {
		case <-r.Context().Done():
			return
		case entry, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(entry)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func handleIOClear(w http.ResponseWriter, r *http.Request) {
	cors(w)
	modelID := r.URL.Query().Get("model")
	if modelID != "" {
		clearModelIOHub(modelID)
	} else {
		globalIOHub.Clear()
	}
	writeJSON(w, map[string]string{"status": "ok"})
}


// handleFavicon serves the embedded application icon so the browser tab
// and OS taskbar show the correct icon.
func handleFavicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/x-icon")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(appIcon)
}
// ─── Browse ───────────────────────────────────────────────────────────────────

func handleBrowseFolder(w http.ResponseWriter, r *http.Request) {
	cors(w)
	path, err := openFolderDialog("Select Models Directory")
	if err != nil || path == "" {
		writeJSON(w, map[string]string{"path": ""})
		return
	}
	writeJSON(w, map[string]string{"path": path})
}

// handleLangs reads the embedded langs.json and returns it so the UI can build
// the language selector dynamically. langs.json lives alongside the i18n files
// and maps display label -> i18n file base name, e.g. {"EN":"en_us","ZH":"zh_cn"}.
func handleLangs(w http.ResponseWriter, r *http.Request) {
	cors(w)
	// Try app-dir first (allows user override), fall back to embedded.
	userPath := appDir + "/ui/i18n/langs.json"
	if data, err := os.ReadFile(userPath); err == nil {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
		return
	}
	// Serve from embedded FS.
	f, err := uiFiles.Open("ui/i18n/langs.json")
	if err != nil {
		writeError(w, "langs.json not found", 404)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.Copy(w, f)
}

// ─── V1 Proxy ─────────────────────────────────────────────────────────────────

func handleV1Proxy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Api-Key")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
	if r.Method == http.MethodOptions {
		w.WriteHeader(204)
		return
	}
	if !svc.IsRunning() {
		writeError(w, "llama-server is not running", 503)
		return
	}
	svc.ProxyRequest(w, r, globalIOHub)
}
