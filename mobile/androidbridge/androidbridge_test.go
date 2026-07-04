package androidbridge

import (
	"net"
	"strings"
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
