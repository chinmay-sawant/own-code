package opencode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultStartupTimeout = 15 * time.Second
	defaultPollInterval   = 100 * time.Millisecond
	stopWaitDelay         = 3 * time.Second
)

// ServerOptions configures an owned foreground `opencode serve` process.
// Port zero asks the manager to reserve an unused loopback port before launch.
type ServerOptions struct {
	Executable     string
	Directory      string
	Password       string
	Port           int
	StartupTimeout time.Duration
	PollInterval   time.Duration
}

// String returns a representation that does not reveal the configured password.
func (o ServerOptions) String() string {
	return fmt.Sprintf("OpenCodeServerOptions{executable:%q,directory:%q,password:[redacted],port:%d}", o.Executable, o.Directory, o.Port)
}

// GoString returns a redacted representation for %#v formatting.
func (o ServerOptions) GoString() string { return o.String() }

// ServerProcess owns one foreground OpenCode server process. Closing the
// handle cancels and waits for only the process started by StartServer.
type ServerProcess struct {
	cmd    *exec.Cmd
	client *Client
	url    string
	port   int
	pid    int
	cancel context.CancelFunc
	done   chan struct{}

	mu      sync.Mutex
	waitErr error
	close   sync.Once
}

// StartServer starts `opencode serve` on loopback and waits until /api/info
// confirms the server PID matches the child process. ctx governs both startup
// and process lifetime; Close stops the child earlier when needed.
func StartServer(ctx context.Context, options ServerOptions) (*ServerProcess, error) {
	if ctx == nil {
		return nil, errors.New("server context is nil")
	}
	if options.Password == "" {
		return nil, errors.New("OpenCode server password is required")
	}
	if options.Port < 0 || options.Port > 65535 {
		return nil, errors.New("OpenCode server port must be between 0 and 65535")
	}
	if options.Port == 0 {
		port, err := reserveLoopbackPort()
		if err != nil {
			return nil, fmt.Errorf("reserve OpenCode loopback port: %w", err)
		}
		options.Port = port
	}
	if options.Executable == "" {
		options.Executable = "opencode"
	}
	if options.StartupTimeout <= 0 {
		options.StartupTimeout = defaultStartupTimeout
	}
	if options.PollInterval <= 0 {
		options.PollInterval = defaultPollInterval
	}

	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(options.Port))
	serverURL := "http://" + address
	client, err := NewClient(serverURL, options.Password)
	if err != nil {
		return nil, err
	}

	lifetimeCtx, cancel := context.WithCancel(ctx)
	args := []string{"serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(options.Port)}
	cmd := exec.CommandContext(lifetimeCtx, options.Executable, args...)
	cmd.Dir = options.Directory
	cmd.Env = serverEnvironment(options.Password)
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.WaitDelay = stopWaitDelay
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start OpenCode server process: %w", err)
	}

	process := &ServerProcess{
		cmd:    cmd,
		client: client,
		url:    serverURL,
		port:   options.Port,
		pid:    cmd.Process.Pid,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go process.wait()

	startupCtx, startupCancel := context.WithTimeout(ctx, options.StartupTimeout)
	defer startupCancel()
	if err := process.waitUntilReady(startupCtx, options.PollInterval); err != nil {
		_ = process.Close()
		return nil, err
	}
	return process, nil
}

func reserveLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", "0"))
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("loopback listener did not return a TCP address")
	}
	return address.Port, nil
}

func serverEnvironment(password string) []string {
	parent := os.Environ()
	environment := make([]string, 0, len(parent)+1)
	for _, entry := range parent {
		key, _, found := strings.Cut(entry, "=")
		if found && (strings.EqualFold(key, "OPENCODE_PASSWORD") || strings.EqualFold(key, "OPENCODE_SERVER_PASSWORD")) {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, "OPENCODE_PASSWORD="+password)
}

func (p *ServerProcess) wait() {
	err := p.cmd.Wait()
	p.mu.Lock()
	p.waitErr = err
	p.mu.Unlock()
	close(p.done)
}

func (p *ServerProcess) waitUntilReady(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		info, err := p.client.GetInfo(ctx)
		if err == nil {
			if info.PID != int64(p.pid) {
				return errors.New("OpenCode readiness endpoint belongs to another process")
			}
			return nil
		}
		select {
		case <-p.done:
			if waitErr := p.processError(); waitErr != nil {
				return fmt.Errorf("OpenCode server exited before readiness: %w", waitErr)
			}
			return errors.New("OpenCode server exited before readiness")
		case <-ctx.Done():
			return fmt.Errorf("OpenCode server did not become ready: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (p *ServerProcess) processError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitErr
}

// URL returns the loopback address of the owned server.
func (p *ServerProcess) URL() string { return p.url }

// Port returns the TCP port assigned to the owned server.
func (p *ServerProcess) Port() int { return p.port }

// PID returns the operating-system process ID of the owned server process.
func (p *ServerProcess) PID() int { return p.pid }

// Client returns an authenticated client for this owned server.
func (p *ServerProcess) Client() *Client { return p.client }

// Done is closed when the owned process exits.
func (p *ServerProcess) Done() <-chan struct{} { return p.done }

// Wait waits for the owned process to exit and returns its exit error, if any.
func (p *ServerProcess) Wait() error {
	<-p.done
	return p.processError()
}

// Close stops and reaps only the child process created by StartServer. It is
// safe to call Close more than once.
func (p *ServerProcess) Close() error {
	p.close.Do(func() {
		p.cancel()
		<-p.done
	})
	return nil
}

// String avoids exposing the child environment, which contains the server
// password.
func (p *ServerProcess) String() string {
	return fmt.Sprintf("OpenCodeServerProcess{url:%q,pid:%d}", p.url, p.pid)
}

// GoString returns a redacted representation for %#v formatting.
func (p *ServerProcess) GoString() string { return p.String() }
