package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

const helperServerPassword = "test-only-server-password"

func TestMain(m *testing.M) {
	if os.Getenv("OPENCODE_PASSWORD") == helperServerPassword && len(os.Args) > 1 && os.Args[1] == "serve" {
		os.Exit(runFakeOpenCodeServer(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func runFakeOpenCodeServer(args []string) int {
	var port int
	for index := 0; index < len(args); index++ {
		if args[index] == "--port" && index+1 < len(args) {
			port, _ = strconv.Atoi(args[index+1])
			index++
		}
		if args[index] == "--hostname" && index+1 < len(args) && args[index+1] != "127.0.0.1" {
			return 2
		}
	}
	if port < 1 || port > 65535 {
		return 2
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return 3
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, request *http.Request) {
		username, password, ok := request.BasicAuth()
		if !ok || username != "opencode" || password != helperServerPassword {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version": "test",
			"pid":     os.Getpid(),
			"urls":    []string{},
			"paths":   map[string]string{"tmp": os.TempDir()},
		})
	})
	server := &http.Server{Handler: mux}
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		return 4
	}
	return 0
}

func TestStartServerOwnsLoopbackChildAndPassesPasswordThroughEnvironment(t *testing.T) {
	t.Setenv("OPENCODE_PASSWORD", "inherited-password")
	t.Setenv("OPENCODE_SERVER_PASSWORD", "legacy-inherited-password")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	process, err := StartServer(ctx, ServerOptions{
		Executable:     os.Args[0],
		Password:       helperServerPassword,
		StartupTimeout: 3 * time.Second,
		PollInterval:   10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if process.URL() == "" || !strings.HasPrefix(process.URL(), "http://127.0.0.1:") {
		t.Fatalf("server was not bound to IPv4 loopback: %q", process.URL())
	}
	if process.Port() < 1 || process.PID() < 1 {
		t.Fatalf("invalid server address or PID: %#v", process)
	}
	info, err := process.Client().GetInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.PID != int64(process.PID()) {
		t.Fatalf("readiness PID %d does not match child PID %d", info.PID, process.PID())
	}
	if strings.Contains(strings.Join(process.cmd.Args, " "), helperServerPassword) {
		t.Fatal("server password was passed in command-line arguments")
	}
	passwordCount := 0
	for _, entry := range process.cmd.Env {
		if strings.HasPrefix(entry, "OPENCODE_PASSWORD=") {
			passwordCount++
			if entry != "OPENCODE_PASSWORD="+helperServerPassword {
				t.Fatal("child did not receive the requested password")
			}
		}
		if strings.HasPrefix(entry, "OPENCODE_SERVER_PASSWORD=") {
			t.Fatal("legacy server password was retained in child environment")
		}
	}
	if passwordCount != 1 {
		t.Fatalf("expected one password environment entry, got %d", passwordCount)
	}
	if strings.Contains(process.String(), helperServerPassword) || strings.Contains(fmt.Sprintf("%#v", process), helperServerPassword) {
		t.Fatal("server process formatting exposed its password")
	}

	if err := process.Close(); err != nil {
		t.Fatal(err)
	}
	if err := process.Close(); err != nil {
		t.Fatalf("second Close was not safe: %v", err)
	}
	select {
	case <-process.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("owned child did not exit after Close")
	}
}

func TestStartServerDoesNotTakeOverAnExistingLoopbackService(t *testing.T) {
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", "0"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "still-running")
	})}
	go server.Serve(listener)
	defer server.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = StartServer(ctx, ServerOptions{
		Executable:     os.Args[0],
		Password:       helperServerPassword,
		Port:           port,
		StartupTimeout: time.Second,
		PollInterval:   10 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected startup to fail when the chosen port is already occupied")
	}

	response, err := http.Get("http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("pre-existing service was stopped: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "still-running" {
		t.Fatalf("pre-existing service returned unexpected body: %q", body)
	}
}

func TestStartServerRequiresASetPassword(t *testing.T) {
	options := ServerOptions{Executable: os.Args[0], Password: helperServerPassword}
	if strings.Contains(options.String(), helperServerPassword) || strings.Contains(fmt.Sprintf("%#v", options), helperServerPassword) {
		t.Fatal("server options formatting exposed the password")
	}
	if _, err := StartServer(context.Background(), ServerOptions{Executable: os.Args[0]}); err == nil {
		t.Fatal("expected an empty password to be rejected")
	}
}
