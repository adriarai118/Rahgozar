package core

import (
	"strings"
	"sync"
	"time"
)

// LogLine is one log entry.
type LogLine struct {
	T    int64  `json:"t"`
	Src  string `json:"src"`
	Text string `json:"text"`
	Lvl  string `json:"lvl"`
}

type logRing struct {
	mu      sync.Mutex
	lines   []LogLine
	max     int
	pending []LogLine
}

func newLogRing(n int) *logRing { return &logRing{max: n} }

func level(s string) string {
	l := strings.ToLower(s)
	switch {
	case strings.Contains(l, "fatal") || strings.Contains(l, "error") || strings.Contains(l, "failed"):
		return "error"
	case strings.Contains(l, "warn"):
		return "warn"
	case strings.Contains(l, "debug") || strings.Contains(l, "trace"):
		return "debug"
	}
	return "info"
}

// Log appends a line and schedules a UI flush.
func (c *Core) Log(src, text string) {
	if c.logs == nil {
		return
	}
	ln := LogLine{T: time.Now().UnixMilli(), Src: src, Text: text, Lvl: level(text)}
	c.logs.mu.Lock()
	c.logs.lines = append(c.logs.lines, ln)
	if len(c.logs.lines) > c.logs.max {
		c.logs.lines = c.logs.lines[len(c.logs.lines)-c.logs.max:]
	}
	first := len(c.logs.pending) == 0
	c.logs.pending = append(c.logs.pending, ln)
	c.logs.mu.Unlock()
	if first {
		go func() {
			time.Sleep(400 * time.Millisecond)
			c.logs.mu.Lock()
			p := c.logs.pending
			c.logs.pending = nil
			c.logs.mu.Unlock()
			if c.Emit != nil && len(p) > 0 {
				c.Emit("log", p)
			}
		}()
	}
}

// Logs returns the buffered log lines.
func (c *Core) Logs() []LogLine {
	c.logs.mu.Lock()
	defer c.logs.mu.Unlock()
	return append([]LogLine(nil), c.logs.lines...)
}

// ClearLogs empties the buffer.
func (c *Core) ClearLogs() {
	c.logs.mu.Lock()
	c.logs.lines = nil
	c.logs.mu.Unlock()
}

func firstOr(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}
