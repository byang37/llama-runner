package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LlamaService manages one llama-server process in --models-dir routing mode.
//
// Architecture:
//   - llama-server listens on a randomly chosen internal port (srvPort).
//   - Our HTTP mux exposes a reverse proxy on the user-configured port
//     (proxyPort = AppSettings.ServicePort), forwarding all traffic to
//     llama-server and recording every request in globalIOHub.
//   - External clients (Postman, OpenAI SDKs, etc.) connect to proxyPort.
type LlamaService struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	pid        int
	srvHost    string // llama-server internal listen address
	srvPort    string // llama-server internal port (random)
	proxyHost  string // user-visible proxy host
	proxyPort  string // user-visible proxy port
	proxySrv   *http.Server
	startedAt  time.Time
}

var svc = &LlamaService{}

// StatusPayload is broadcast over the status SSE stream.
type StatusPayload struct {
	Running   bool   `json:"running"`
	Host      string `json:"host"`
	Port      string `json:"port"`
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at,omitempty"`
}

func (s *LlamaService) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmd != nil
}

func (s *LlamaService) Addr() (host, port string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.srvHost, s.srvPort
}

func (s *LlamaService) Status() StatusPayload {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := StatusPayload{
		Running: s.cmd != nil,
		Host:    s.proxyHost,
		Port:    s.proxyPort,
		PID:     s.pid,
	}
	if !s.startedAt.IsZero() {
		p.StartedAt = s.startedAt.Format(time.RFC3339)
	}
	return p
}

// pickFreePort returns an unused TCP port on 127.0.0.1.
func pickFreePort() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "11434"
	}
	defer l.Close()
	return fmt.Sprintf("%d", l.Addr().(*net.TCPAddr).Port)
}

// Start launches llama-server on a random internal port, then opens a
// transparent reverse proxy on the user-configured port so all external
// requests are intercepted and logged.
func (s *LlamaService) Start(st AppSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd != nil {
		return fmt.Errorf("service is already running")
	}
	if st.ModelsDir == "" {
		return fmt.Errorf("models directory not configured")
	}

	exe := filepath.Join(libDir, executableName())
	if _, err := os.Stat(exe); os.IsNotExist(err) {
		return fmt.Errorf("llama-server binary not found in lib/ (expected %s)", executableName())
	}

	// llama-server always binds to 127.0.0.1 on a free port.
	srvPort := pickFreePort()
	srvHost := "127.0.0.1"

	proxyHost := st.ServiceHost
	if proxyHost == "" {
		proxyHost = "127.0.0.1"
	}
	proxyPort := st.ServicePort
	if proxyPort == "" {
		proxyPort = "8080"
	}

	args := []string{
		"--host", srvHost,
		"--port", srvPort,
		"--models-dir", st.ModelsDir,
	}
	if presetsPath := presetsINIPath(); fileExists(presetsPath) {
		args = append(args, "--models-preset", presetsPath)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, exe, args...)
	hideWindow(cmd)

	cmd.Env = os.Environ()
	for _, ev := range st.EnvVars {
		if strings.TrimSpace(ev.Key) != "" {
			cmd.Env = append(cmd.Env, ev.Key+"="+ev.Value)
		}
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("failed to start llama-server: %w", err)
	}

	// Ensure llama-server is inside the Windows Job Object so it is
	// terminated automatically if this process exits unexpectedly.
	assignToJob(cmd)

	s.cmd = cmd
	s.cancel = cancel
	s.pid = cmd.Process.Pid
	s.srvHost = srvHost
	s.srvPort = srvPort
	s.proxyHost = proxyHost
	s.proxyPort = proxyPort
	s.startedAt = time.Now()

	pub := func(line string) { globalLogHub.Publish(line) }
	pub(fmt.Sprintf("[%s] SYS start pid=%d internal=%s:%s proxy=%s:%s models-dir=%s",
		ts(), cmd.Process.Pid, srvHost, srvPort, proxyHost, proxyPort, st.ModelsDir))

	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			pub(fmt.Sprintf("[%s] OUT %s", ts(), sc.Text()))
		}
	}()
	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			pub(fmt.Sprintf("[%s] %s %s", ts(), logTag(line), line))
		}
	}()
	go func() {
		_ = cmd.Wait()
		s.mu.Lock()
		s.cmd = nil
		s.cancel = nil
		s.pid = 0
		// stop the proxy too
		if s.proxySrv != nil {
			ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel2()
			_ = s.proxySrv.Shutdown(ctx2)
			s.proxySrv = nil
		}
		s.mu.Unlock()
		pub(fmt.Sprintf("[%s] SYS stop", ts()))
		statusBroadcast()
	}()

	// Start the recording proxy on the user-visible port.
	if err := s.startProxy(proxyHost, proxyPort, srvHost, srvPort); err != nil {
		pub(fmt.Sprintf("[%s] WRN proxy start failed on %s:%s: %v", ts(), proxyHost, proxyPort, err))
		// Non-fatal: llama-server still works; recording just won't happen.
	} else {
		pub(fmt.Sprintf("[%s] SYS proxy listening on %s:%s", ts(), proxyHost, proxyPort))
	}

	statusBroadcast()
	return nil
}

// startProxy launches an HTTP reverse proxy that forwards all traffic from
// proxyHost:proxyPort to srvHost:srvPort, recording every request.
func (s *LlamaService) startProxy(proxyHost, proxyPort, srvHost, srvPort string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Api-Key")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		forwardAndRecord(w, r, srvHost, srvPort, globalIOHub)
	})

	srv := &http.Server{
		Addr:    proxyHost + ":" + proxyPort,
		Handler: mux,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-time.After(150 * time.Millisecond):
		s.proxySrv = srv
		return nil
	}
}

// Stop terminates llama-server (the proxy stops automatically when the
// goroutine waiting on cmd.Wait() runs).
func (s *LlamaService) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil {
		return fmt.Errorf("service is not running")
	}
	s.cancel()
	return nil
}

// ─── /v1/models polling ───────────────────────────────────────────────────────

type modelStatusValue struct{ Value string }

func (m *modelStatusValue) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		m.Value = s
		return nil
	}
	var obj struct{ Value string `json:"value"` }
	if err := json.Unmarshal(data, &obj); err == nil {
		m.Value = obj.Value
	}
	return nil
}

// QueryLoadedModels calls GET /v1/models on the internal llama-server port.
func (s *LlamaService) QueryLoadedModels() map[string]bool {
	host, port := s.Addr()
	result := make(map[string]bool)
	if host == "" {
		return result
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s:%s/v1/models", host, port))
	if err != nil {
		return result
	}
	defer resp.Body.Close()
	var body struct {
		Data []struct {
			ID     string           `json:"id"`
			Status modelStatusValue `json:"status"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return result
	}
	for _, m := range body.Data {
		if m.Status.Value == "" || m.Status.Value == "loaded" {
			result[m.ID] = true
		}
	}
	return result
}

// ProxyRequest forwards a request to llama-server (used by our /v1/ catch-all).
func (s *LlamaService) ProxyRequest(w http.ResponseWriter, r *http.Request, hub *IOHub) {
	host, port := s.Addr()
	if host == "" {
		http.Error(w, `{"error":"llama-server is not running"}`, 503)
		return
	}
	forwardAndRecord(w, r, host, port, hub)
}

// ─── Load / Unload ────────────────────────────────────────────────────────────

// LoadModel calls POST /models/load on the internal llama-server port.
func (s *LlamaService) LoadModel(modelID string, hub *IOHub) error {
	host, port := s.Addr()
	if host == "" {
		return fmt.Errorf("llama-server is not running")
	}
	payload := map[string]string{"model": modelID}
	data, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Post(
		fmt.Sprintf("http://%s:%s/models/load", host, port),
		"application/json", bytes.NewReader(data),
	)
	if err != nil {
		return fmt.Errorf("load request failed: %w", err)
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	recordEntry(hub, "POST", "/models/load", payload, result, resp.StatusCode)
	if resp.StatusCode >= 400 {
		return extractServerError(result, resp.StatusCode)
	}
	return nil
}

// UnloadModel calls POST /models/unload on the internal llama-server port.
func (s *LlamaService) UnloadModel(modelID string, hub *IOHub) error {
	host, port := s.Addr()
	if host == "" {
		return fmt.Errorf("llama-server is not running")
	}
	payload := map[string]string{"model": modelID}
	data, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(
		fmt.Sprintf("http://%s:%s/models/unload", host, port),
		"application/json", bytes.NewReader(data),
	)
	if err != nil {
		return fmt.Errorf("unload request failed: %w", err)
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	recordEntry(hub, "POST", "/models/unload", payload, result, resp.StatusCode)
	if resp.StatusCode >= 400 {
		return extractServerError(result, resp.StatusCode)
	}
	return nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func ts() string { return time.Now().Format("15:04:05") }

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func recordEntry(hub *IOHub, method, path string, req, resp interface{}, status int) {
	e := IOEntry{
		ID:       fmt.Sprintf("%s_%d", strings.ReplaceAll(path, "/", "_"), time.Now().UnixNano()),
		Time:     time.Now().Format("15:04:05"),
		Method:   method, Path: path,
		Request: req, Response: resp,
		Duration: "-", Status: status,
	}
	hub.Publish(e)
	if hub != globalIOHub {
		e2 := e
		e2.ID += "_g"
		globalIOHub.Publish(e2)
	}
}

func extractServerError(result map[string]interface{}, status int) error {
	if errObj, ok := result["error"].(map[string]interface{}); ok {
		if msg, ok := errObj["message"].(string); ok && msg != "" {
			return fmt.Errorf("server error: %s", msg)
		}
	}
	return fmt.Errorf("server returned status %d", status)
}
