package main

import (
	"strings"
	"sync"
)

// ─── Log Hub ──────────────────────────────────────────────────────────────────

type LogHub struct {
	mu          sync.Mutex
	subscribers map[chan string]struct{}
	buffer      []string
	totalChars  int
}

const maxLogChars = 200_000

func newLogHub() *LogHub {
	return &LogHub{subscribers: make(map[chan string]struct{})}
}

func (h *LogHub) Subscribe() chan string {
	ch := make(chan string, 1000)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subscribers[ch] = struct{}{}
	for _, line := range h.buffer {
		select {
		case ch <- line:
		default:
		}
	}
	return ch
}

func (h *LogHub) Unsubscribe(ch chan string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subscribers, ch)
}

func (h *LogHub) Publish(line string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buffer = append(h.buffer, line)
	h.totalChars += len(line)
	for h.totalChars > maxLogChars && len(h.buffer) > 0 {
		h.totalChars -= len(h.buffer[0])
		h.buffer = h.buffer[1:]
	}
	for ch := range h.subscribers {
		select {
		case ch <- line:
		default:
		}
	}
}

func (h *LogHub) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buffer = nil
	h.totalChars = 0
}

// globalLogHub receives all log lines from the service.
var globalLogHub = newLogHub()

// ─── Log classification ───────────────────────────────────────────────────────

func logTag(line string) string {
	lower := strings.ToLower(line)
	if strings.Contains(lower, "error") && !strings.Contains(lower, "n_threads") {
		return "ERR"
	}
	if strings.Contains(lower, "warning") || strings.Contains(lower, " warn ") {
		return "WRN"
	}
	return "OUT"
}

// ─── Status SSE broadcast ──────────────────────────────────────────────────────

type StatusHub struct {
	mu          sync.Mutex
	subscribers map[chan struct{}]struct{}
}

var statusHub = &StatusHub{subscribers: make(map[chan struct{}]struct{})}

func statusBroadcast() {
	statusHub.mu.Lock()
	defer statusHub.mu.Unlock()
	for ch := range statusHub.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (h *StatusHub) Subscribe() chan struct{} {
	ch := make(chan struct{}, 4)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subscribers[ch] = struct{}{}
	return ch
}

func (h *StatusHub) Unsubscribe(ch chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subscribers, ch)
}
