package main

import (
	"os"
	"syscall"
	"testing"
	"time"

	"sskycn/tcptun"
)

func TestApplyModeConfigPathDefault(t *testing.T) {
	t.Setenv("PROXY_TEST_PLACEHOLDER", "1")
	oldArgs := os.Args
	defer func() {
		os.Args = oldArgs
	}()

	os.Args = []string{"tcptun", "client"}
	cfg := tcptun.DefaultConfig()
	applyModeConfigPathDefault(&cfg, "client.json")
	if cfg.ConfigPath != "client.json" {
		t.Fatalf("config path = %q, want client.json", cfg.ConfigPath)
	}

	cfg = tcptun.DefaultConfig()
	cfg.ConfigPath = "/tmp/explicit.json"
	applyModeConfigPathDefault(&cfg, "server.json")
	if cfg.ConfigPath != "/tmp/explicit.json" {
		t.Fatalf("explicit config path = %q", cfg.ConfigPath)
	}

	cfg = tcptun.DefaultConfig()
	cfg.ConfigPath = ""
	applyModeConfigPathDefault(&cfg, "server.json")
	if cfg.ConfigPath != "" {
		t.Fatalf("disabled config path = %q", cfg.ConfigPath)
	}

	os.Args = []string{"tcptun", "client", "--config", "config.json"}
	cfg = tcptun.DefaultConfig()
	applyModeConfigPathDefault(&cfg, "server.json")
	if cfg.ConfigPath != "config.json" {
		t.Fatalf("explicit default config path = %q", cfg.ConfigPath)
	}
}

func TestHasExplicitConfigPathFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{name: "none", args: []string{"client"}, want: false},
		{name: "long", args: []string{"client", "--config", "client.json"}, want: true},
		{name: "long equals", args: []string{"--config=client.json", "client"}, want: true},
		{name: "short", args: []string{"client", "-c", "client.json"}, want: true},
		{name: "short equals", args: []string{"client", "-c=client.json"}, want: true},
		{name: "other short", args: []string{"client", "-v"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hasExplicitConfigPathFlag(tc.args)
			if got != tc.want {
				t.Fatalf("hasExplicitConfigPathFlag(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestHasExplicitListenFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{name: "none", args: []string{"server"}, want: false},
		{name: "long", args: []string{"server", "--listen", "127.0.0.1:1080"}, want: true},
		{name: "long equals", args: []string{"--listen=127.0.0.1:1080", "server"}, want: true},
		{name: "short", args: []string{"server", "-l", "127.0.0.1:1080"}, want: true},
		{name: "short equals", args: []string{"server", "-l=127.0.0.1:1080"}, want: true},
		{name: "other short", args: []string{"server", "-v"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hasExplicitListenFlag(tc.args)
			if got != tc.want {
				t.Fatalf("hasExplicitListenFlag(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestHandleShutdownSignalsCancelsThenExitsOnSecondSignal(t *testing.T) {
	signals := make(chan os.Signal, 2)
	canceled := make(chan struct{})
	exitCode := make(chan int, 1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		handleShutdownSignals(signals, func() {
			close(canceled)
		}, func(code int) {
			exitCode <- code
		})
	}()

	signals <- os.Interrupt
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("first signal did not cancel context")
	}
	select {
	case code := <-exitCode:
		t.Fatalf("first signal exited with code %d", code)
	default:
	}

	signals <- os.Interrupt
	select {
	case code := <-exitCode:
		if code != 130 {
			t.Fatalf("second interrupt exit code = %d, want 130", code)
		}
	case <-time.After(time.Second):
		t.Fatal("second signal did not exit")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("signal handler did not return")
	}
}

func TestExitCodeForSignal(t *testing.T) {
	if got := exitCodeForSignal(os.Interrupt); got != 130 {
		t.Fatalf("interrupt exit code = %d, want 130", got)
	}
	if got := exitCodeForSignal(syscall.SIGTERM); got != 143 {
		t.Fatalf("sigterm exit code = %d, want 143", got)
	}
	if got := exitCodeForSignal(syscall.SIGHUP); got != 1 {
		t.Fatalf("fallback exit code = %d, want 1", got)
	}
}
