package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ─── IOEntry ──────────────────────────────────────────────────────────────────

type IOEntry struct {
	ID       string      `json:"id"`
	Time     string      `json:"time"`
	Method   string      `json:"method"`
	Path     string      `json:"path"`
	Request  interface{} `json:"request"`
	Response interface{} `json:"response"`
	Duration string      `json:"duration"`
	Status   int         `json:"status"`
	Stream   bool        `json:"stream"`
}

// ─── IOHub ────────────────────────────────────────────────────────────────────

type IOHub struct {
	mu          sync.Mutex
	subscribers map[chan IOEntry]struct{}
	history     []IOEntry
}

var globalIOHub = newIOHub()

func newIOHub() *IOHub {
	return &IOHub{subscribers: make(map[chan IOEntry]struct{})}
}

func (h *IOHub) Subscribe() chan IOEntry {
	ch := make(chan IOEntry, 200)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subscribers[ch] = struct{}{}
	for _, e := range h.history {
		select {
		case ch <- e:
		default:
		}
	}
	return ch
}

func (h *IOHub) Unsubscribe(ch chan IOEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subscribers, ch)
}

func (h *IOHub) Publish(e IOEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.history = append(h.history, e)
	if len(h.history) > 500 {
		h.history = h.history[len(h.history)-500:]
	}
	for ch := range h.subscribers {
		select {
		case ch <- e:
		default:
		}
	}
}

func (h *IOHub) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.history = nil
}

// ─── Per-model IOHubs ─────────────────────────────────────────────────────────

var modelIOHubs = struct {
	sync.Mutex
	m map[string]*IOHub
}{m: make(map[string]*IOHub)}

func getModelIOHub(modelID string) *IOHub {
	modelIOHubs.Lock()
	defer modelIOHubs.Unlock()
	if h, ok := modelIOHubs.m[modelID]; ok {
		return h
	}
	h := newIOHub()
	modelIOHubs.m[modelID] = h
	return h
}

func clearModelIOHub(modelID string) {
	modelIOHubs.Lock()
	defer modelIOHubs.Unlock()
	if h, ok := modelIOHubs.m[modelID]; ok {
		h.Clear()
	}
}

// ─── forwardAndRecord ─────────────────────────────────────────────────────────

func forwardAndRecord(w http.ResponseWriter, r *http.Request, targetHost, targetPort string, hub *IOHub) {
	var reqBody []byte
	if r.Body != nil {
		reqBody, _ = io.ReadAll(r.Body)
		r.Body.Close()
	}
	var reqJSON interface{}
	if len(reqBody) > 0 {
		_ = json.Unmarshal(reqBody, &reqJSON)
	}

	start := time.Now()
	entryID := fmt.Sprintf("%d%d", start.UnixNano(), rand.Intn(9999))

	resolvedHost := targetHost
	if resolvedHost == "0.0.0.0" {
		resolvedHost = "127.0.0.1"
	}
	targetURL := fmt.Sprintf("http://%s:%s%s", resolvedHost, targetPort, r.URL.RequestURI())

	fwdReq, err := http.NewRequest(r.Method, targetURL, bytes.NewReader(reqBody))
	if err != nil {
		http.Error(w, "proxy error: "+err.Error(), 502)
		return
	}
	fwdReq.Header = r.Header.Clone()
	fwdReq.Header.Del("Accept-Encoding")

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(fwdReq)
	if err != nil {
		http.Error(w, "upstream error: "+err.Error(), 502)
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}

	ct := resp.Header.Get("Content-Type")
	isStream := strings.Contains(ct, "text/event-stream") || strings.Contains(ct, "application/x-ndjson")
	if m, ok := reqJSON.(map[string]interface{}); ok {
		if s, _ := m["stream"].(bool); s {
			isStream = true
		}
	}

	if isStream {
		w.WriteHeader(resp.StatusCode)
		flusher, _ := w.(http.Flusher)
		buf := make([]byte, 4096)
		var sampleLines []string
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
				if flusher != nil {
					flusher.Flush()
				}
				if len(sampleLines) < 3 {
					chunk := strings.TrimSpace(string(buf[:n]))
					if chunk != "" {
						l := len(chunk)
						if l > 200 {
							l = 200
						}
						sampleLines = append(sampleLines, chunk[:l])
					}
				}
			}
			if err != nil {
				break
			}
		}
		hub.Publish(IOEntry{
			ID: entryID, Time: start.Format("15:04:05"),
			Method: r.Method, Path: r.URL.Path,
			Request:  reqJSON,
			Response: map[string]interface{}{"note": "streaming", "sample": sampleLines},
			Duration: fmt.Sprintf("%dms", time.Since(start).Milliseconds()),
			Status:   resp.StatusCode, Stream: true,
		})
		return
	}

	respBody, _ := io.ReadAll(resp.Body)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)

	var respJSON interface{}
	if len(respBody) > 0 {
		if json.Unmarshal(respBody, &respJSON) != nil {
			respJSON = string(respBody)
		}
	}

	hub.Publish(IOEntry{
		ID: entryID, Time: start.Format("15:04:05"),
		Method: r.Method, Path: r.URL.Path,
		Request: reqJSON, Response: respJSON,
		Duration: fmt.Sprintf("%dms", time.Since(start).Milliseconds()),
		Status:   resp.StatusCode,
	})
}
