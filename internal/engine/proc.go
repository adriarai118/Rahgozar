package engine

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"rahgozar/internal/sys"
)

func runCtx(ctx context.Context, c *exec.Cmd) (string, error) {
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	if err := c.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	select {
	case err := <-done:
		return buf.String(), err
	case <-ctx.Done():
		c.Process.Kill()
		return buf.String(), ctx.Err()
	}
}

// Proc is a running engine process with captured output.
type Proc struct {
	Name   string
	cmd    *exec.Cmd
	done   chan struct{}
	err    error
	mu     sync.Mutex
	tail   []string
	OnLine func(name, line string)
}

// Start launches exe with args in dir.
func Start(name, exe, dir string, args []string, onLine func(name, line string)) (*Proc, error) {
	c := sys.Command(exe, args...)
	c.Dir = dir
	stdout, err := c.StdoutPipe()
	if err != nil {
		return nil, err
	}
	c.Stderr = c.Stdout
	p := &Proc{Name: name, cmd: c, done: make(chan struct{}), OnLine: onLine}
	if err := c.Start(); err != nil {
		return nil, err
	}
	go p.pump(stdout)
	go func() {
		p.err = c.Wait()
		close(p.done)
	}()
	return p, nil
}

var ansi = strings.NewReplacer("\x1b[31m", "", "\x1b[32m", "", "\x1b[33m", "", "\x1b[34m", "", "\x1b[35m", "", "\x1b[36m", "", "\x1b[0m", "", "\x1b[37m", "", "\x1b[90m", "")

func (p *Proc) pump(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := ansi.Replace(sc.Text())
		p.mu.Lock()
		p.tail = append(p.tail, line)
		if len(p.tail) > 30 {
			p.tail = p.tail[len(p.tail)-30:]
		}
		p.mu.Unlock()
		if p.OnLine != nil {
			p.OnLine(p.Name, line)
		}
	}
}

// PID returns the process id.
func (p *Proc) PID() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Done is closed when the process exits.
func (p *Proc) Done() <-chan struct{} { return p.done }

// Alive reports whether the process is still running.
func (p *Proc) Alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// ExitError describes why the process stopped, with its last output lines.
func (p *Proc) ExitError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var important []string
	for _, l := range p.tail {
		ll := strings.ToLower(l)
		if strings.Contains(ll, "fatal") || strings.Contains(ll, "error") || strings.Contains(ll, "failed") {
			important = append(important, l)
		}
	}
	if len(important) == 0 && len(p.tail) > 0 {
		important = p.tail[max(0, len(p.tail)-3):]
	}
	msg := strings.Join(important, " | ")
	if len(msg) > 600 {
		msg = msg[len(msg)-600:]
	}
	if p.err != nil {
		return fmt.Errorf("%s stopped (%v): %s", p.Name, p.err, msg)
	}
	return fmt.Errorf("%s stopped: %s", p.Name, msg)
}

// Stop kills the process and waits briefly.
func (p *Proc) Stop() {
	if p == nil || p.cmd.Process == nil {
		return
	}
	if !p.Alive() {
		return
	}
	interrupt(p.cmd)
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		p.cmd.Process.Kill()
		<-p.done
	}
}

// Label returns the process display name.
func (p *Proc) Label() string { return p.Name }
