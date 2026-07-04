package androidbridge

import (
	"encoding/json"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestParseConfigJSON(t *testing.T) {
	cfg, err := parseConfigJSON(`{
		"mode": "client",
		"listen_addrs": ["127.0.0.1:18080", " 127.0.0.1:18081 "],
		"server_addr": "203.0.113.10:9443",
		"token": "secret",
		"tunnel_protocol": "vless",
		"tunnel_transport": "h2",
		"tunnel_tls": true,
		"tunnel_tls_insecure": true,
		"tunnel_mux": false,
		"gateway_port": 7890,
		"upstream_protocol": "mixed",
		"direct_probe_timeout": "750ms",
		"heartbeat_interval": "30s",
		"connection_idle_timeout": "3m",
		"udp_session_timeout": "45s",
		"retry_initial_interval": "100ms",
		"retry_max_interval": "2s",
		"scan_workers": 32,
		"buffer_size": 65536,
		"verbose": true
	}`)
	if err != nil {
		t.Fatalf("parseConfigJSON returned error: %v", err)
	}
	if cfg.Mode != "client" {
		t.Fatalf("mode = %q, want client", cfg.Mode)
	}
	if strings.Join(cfg.ListenAddrs, ",") != "127.0.0.1:18080,127.0.0.1:18081" {
		t.Fatalf("listen addrs = %#v", cfg.ListenAddrs)
	}
	if cfg.ListenAddr != "" {
		t.Fatalf("listen addr = %q, want empty when listen_addrs is set", cfg.ListenAddr)
	}
	if cfg.ServerAddr != "203.0.113.10:9443" || cfg.Token != "secret" {
		t.Fatalf("server/token not parsed: %#v", cfg)
	}
	if !cfg.TunnelTLS || !cfg.TunnelTLSInsecure {
		t.Fatalf("tls flags not parsed: %#v", cfg)
	}
	if cfg.TunnelMux {
		t.Fatalf("tunnel mux = true, want false")
	}
	if cfg.DirectProbeTimeout != 750*time.Millisecond {
		t.Fatalf("direct probe timeout = %s", cfg.DirectProbeTimeout)
	}
	if cfg.HeartbeatInterval != 30*time.Second {
		t.Fatalf("heartbeat interval = %s", cfg.HeartbeatInterval)
	}
	if cfg.ConnectionIdleTimeout != 3*time.Minute {
		t.Fatalf("connection idle timeout = %s", cfg.ConnectionIdleTimeout)
	}
	if cfg.UDPSessionTimeout != 45*time.Second {
		t.Fatalf("udp session timeout = %s", cfg.UDPSessionTimeout)
	}
	if cfg.RetryInitialInterval != 100*time.Millisecond {
		t.Fatalf("retry initial interval = %s", cfg.RetryInitialInterval)
	}
	if cfg.RetryMaxInterval != 2*time.Second {
		t.Fatalf("retry max interval = %s", cfg.RetryMaxInterval)
	}
	if cfg.ScanWorkers != 32 || cfg.BufferSize != 65536 || !cfg.Verbose {
		t.Fatalf("runtime tunables not parsed: %#v", cfg)
	}
}

func TestParseConfigJSONListenCompatibility(t *testing.T) {
	cfg, err := parseConfigJSON(`{"local_listen_addr":"127.0.0.1:18082"}`)
	if err != nil {
		t.Fatalf("parseConfigJSON returned error: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:18082" {
		t.Fatalf("listen addr = %q", cfg.ListenAddr)
	}
	if strings.Join(cfg.ListenAddrs, ",") != "127.0.0.1:18082" {
		t.Fatalf("listen addrs = %#v", cfg.ListenAddrs)
	}

	cfg, err = parseConfigJSON(`{"listen_addrs":["127.0.0.1:18083"],"local_listen_addr":"127.0.0.1:18084"}`)
	if err != nil {
		t.Fatalf("parseConfigJSON returned error: %v", err)
	}
	if cfg.ListenAddr != "" || strings.Join(cfg.ListenAddrs, ",") != "127.0.0.1:18083" {
		t.Fatalf("listen_addrs should take precedence: listen=%q addrs=%#v", cfg.ListenAddr, cfg.ListenAddrs)
	}
}

func TestParseConfigJSONDurationError(t *testing.T) {
	_, err := parseConfigJSON(`{"heartbeat_interval":"not-a-duration"}`)
	if err == nil {
		t.Fatal("parseConfigJSON returned nil error for invalid duration")
	}
}

func TestSetStatusCallbackNilClearsCallback(t *testing.T) {
	var calls atomic.Int64
	SetStatusCallback(statusCallbackFunc(func(string) {
		calls.Add(1)
	}))
	SetStatusCallback(nil)
	t.Cleanup(func() {
		SetStatusCallback(nil)
	})

	emitStatus(statusEvent{State: "starting", Phase: "unit test"})
	if calls.Load() != 0 {
		t.Fatalf("status callback calls = %d, want 0", calls.Load())
	}
}

func TestEmitStatusCallbackReceivesValidJSON(t *testing.T) {
	events := make(chan string, 1)
	SetStatusCallback(statusCallbackFunc(func(eventJSON string) {
		events <- eventJSON
	}))
	t.Cleanup(func() {
		SetStatusCallback(nil)
	})

	emitStatus(statusEvent{
		State:             "running",
		Phase:             "unit test running",
		Listen:            "127.0.0.1:1080",
		Remote:            "example.com:443",
		ActiveConnections: 3,
		LastError:         "token=secret 14c1bdf2-9815-46ff-862e-50f459b84cbf",
	})

	event := readStatusEvent(t, events)
	if event.State != "running" || event.Phase != "unit test running" {
		t.Fatalf("unexpected status event: %#v", event)
	}
	if event.Listen != "127.0.0.1:1080" || event.Remote != "example.com:443" {
		t.Fatalf("unexpected addresses in status event: %#v", event)
	}
	if event.ActiveConnections != 3 {
		t.Fatalf("active connections = %d, want 3", event.ActiveConnections)
	}
	if event.TimestampMS == 0 {
		t.Fatal("timestamp_ms is zero")
	}
	if strings.Contains(event.LastError, "secret") || strings.Contains(event.LastError, "14c1bdf2") {
		t.Fatalf("last_error was not redacted: %q", event.LastError)
	}
}

func TestEmitStatusRecoversCallbackPanic(t *testing.T) {
	SetStatusCallback(statusCallbackFunc(func(string) {
		panic("android callback panic")
	}))
	t.Cleanup(func() {
		SetStatusCallback(nil)
	})

	emitStatus(statusEvent{State: "running", Phase: "panic test"})
}

func TestEmitStatusUpdatesSimpleStatus(t *testing.T) {
	SetStatusCallback(nil)
	t.Cleanup(func() {
		SetStatusCallback(nil)
	})

	emitStatus(statusEvent{State: "starting", Phase: "status test"})
	if got := Status(); got != statusStarting {
		t.Fatalf("Status() = %q, want %q", got, statusStarting)
	}
	emitStatus(statusEvent{State: "degraded", Phase: "status test"})
	if got := Status(); got != statusRunning {
		t.Fatalf("Status() = %q, want %q", got, statusRunning)
	}
	emitStatus(statusEvent{State: "error", Phase: "status test"})
	if got := Status(); got != statusError {
		t.Fatalf("Status() = %q, want %q", got, statusError)
	}
	emitStatus(statusEvent{State: "stopped", Phase: "status test"})
	if got := Status(); got != statusStopped {
		t.Fatalf("Status() = %q, want %q", got, statusStopped)
	}
}

func TestConcurrentSetStatusCallbackAndEmitStatus(t *testing.T) {
	var calls atomic.Int64
	cb := statusCallbackFunc(func(string) {
		calls.Add(1)
	})
	t.Cleanup(func() {
		SetStatusCallback(nil)
	})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 250; j++ {
				SetStatusCallback(cb)
				SetStatusCallback(nil)
			}
		}()
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 250; j++ {
				emitStatus(statusEvent{State: "degraded", Phase: "race test"})
			}
		}()
	}
	wg.Wait()
}

func TestStartStopEmitStatusEvents(t *testing.T) {
	SetStatusCallback(nil)
	if err := Stop(); err != nil {
		t.Fatalf("initial Stop returned error: %v", err)
	}

	events := make(chan string, 16)
	SetStatusCallback(statusCallbackFunc(func(eventJSON string) {
		events <- eventJSON
	}))
	t.Cleanup(func() {
		SetStatusCallback(nil)
		if err := Stop(); err != nil {
			t.Fatalf("cleanup Stop returned error: %v", err)
		}
	})

	configJSON := `{"mode":"client","listen_addrs":["127.0.0.1:0"],"server_addr":"127.0.0.1:1","token":"secret","route_config_path":"` + t.TempDir() + `/route.json"}`
	if err := Start(configJSON); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	waitStatusState(t, events, "starting")

	if err := Stop(); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	waitStatusState(t, events, "stopping")
	waitStatusState(t, events, "stopped")
}

func TestStartDuplicateCallReturnsError(t *testing.T) {
	t.Cleanup(func() {
		if err := Stop(); err != nil {
			t.Fatalf("cleanup Stop returned error: %v", err)
		}
	})

	err := Start(`{"mode":"client","listen_addrs":["127.0.0.1:0"],"server_addr":"127.0.0.1:1","token":"secret","route_config_path":"` + t.TempDir() + `/route.json"}`)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if err := Start(`{"mode":"client","listen_addrs":["127.0.0.1:0"],"server_addr":"127.0.0.1:1","token":"secret"}`); err == nil {
		t.Fatal("second Start returned nil error")
	}
}

func TestSetSocketProtectorDialControlCallsProtector(t *testing.T) {
	var calls atomic.Int64
	SetSocketProtector(testProtector{calls: &calls, ok: true})
	t.Cleanup(func() {
		SetSocketProtector(nil)
	})

	cfg, err := parseConfigJSON(`{}`)
	if err != nil {
		t.Fatalf("parseConfigJSON returned error: %v", err)
	}
	cfg.DialControl = protectSocket

	conn, rawConn := testRawConn(t)
	defer func() {
		if err := conn.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	}()

	if err := cfg.DialControl("udp", "127.0.0.1:443", rawConn); err != nil {
		t.Fatalf("DialControl returned error: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("protector calls = %d, want 1", calls.Load())
	}
}

func TestProtectSocketReturnsErrorWhenProtectorRejects(t *testing.T) {
	SetSocketProtector(testProtector{ok: false})
	t.Cleanup(func() {
		SetSocketProtector(nil)
	})

	conn, rawConn := testRawConn(t)
	defer func() {
		if err := conn.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	}()

	if err := protectSocket("tcp", "127.0.0.1:443", rawConn); err == nil {
		t.Fatal("protectSocket returned nil error for rejected fd")
	}
}

type testProtector struct {
	calls *atomic.Int64
	ok    bool
}

func (p testProtector) Protect(fd int64) bool {
	if p.calls != nil {
		p.calls.Add(1)
	}
	return p.ok
}

func testRawConn(t *testing.T) (*net.UDPConn, syscall.RawConn) {
	t.Helper()
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		t.Fatalf("ListenUDP returned error: %v", err)
	}
	rawConn, err := conn.SyscallConn()
	if err != nil {
		closeErr := conn.Close()
		if closeErr != nil {
			t.Fatalf("SyscallConn returned %v and Close returned %v", err, closeErr)
		}
		t.Fatalf("SyscallConn returned error: %v", err)
	}
	return conn, rawConn
}

type statusCallbackFunc func(string)

func (f statusCallbackFunc) OnStatus(eventJSON string) {
	f(eventJSON)
}

func readStatusEvent(t *testing.T, events <-chan string) statusEvent {
	t.Helper()
	select {
	case eventJSON := <-events:
		var event statusEvent
		if err := json.Unmarshal([]byte(eventJSON), &event); err != nil {
			t.Fatalf("status event is not valid JSON: %v; payload=%q", err, eventJSON)
		}
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for status event")
		return statusEvent{}
	}
}

func waitStatusState(t *testing.T, events <-chan string, want string) statusEvent {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case eventJSON := <-events:
			var event statusEvent
			if err := json.Unmarshal([]byte(eventJSON), &event); err != nil {
				t.Fatalf("status event is not valid JSON: %v; payload=%q", err, eventJSON)
			}
			if event.State == want {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for status state %q", want)
			return statusEvent{}
		}
	}
}
