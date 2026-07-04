// Package androidbridge exposes tcptun to Android through gomobile bind.
package androidbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	tcptun "sskycn/tcptun"
)

const (
	statusStopped  = "Stopped"
	statusStarting = "Starting"
	statusRunning  = "Running"
	statusError    = "Error"

	stopTimeout = 5 * time.Second
)

// LogCallback receives tcptun log lines from Android.
type LogCallback interface {
	OnLog(line string)
}

// StatusCallback receives active tcptun runtime status events as JSON.
type StatusCallback interface {
	OnStatus(eventJson string)
}

// SocketProtector wraps VpnService.protect(fd) for sockets opened by tcptun.
type SocketProtector interface {
	Protect(fd int64) bool
}

type bridgeState struct {
	mu        sync.Mutex
	status    string
	cancel    context.CancelFunc
	done      chan error
	lastError error
	logCB     LogCallback
	statusCB  StatusCallback
	protector SocketProtector
	listen    string
	remote    string
}

var state = bridgeState{status: statusStopped}

type statusEvent struct {
	State             string `json:"state"`
	Phase             string `json:"phase"`
	Listen            string `json:"listen"`
	Remote            string `json:"remote"`
	ActiveConnections int    `json:"active_connections"`
	LastError         string `json:"last_error"`
	TimestampMS       int64  `json:"timestamp_ms"`
}

var (
	uuidPattern           = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	sensitiveFieldPattern = regexp.MustCompile(`(?i)(token|password|passwd|secret|uuid|private[_-]?key)(\s*[:=]\s*)("[^"]*"|'[^']*'|[^,\s}]+)`)
)

// Start parses configJSON and starts tcptun in the background.
func Start(configJSON string) error {
	cfg, err := parseConfigJSON(configJSON)
	if err != nil {
		setError(err)
		return err
	}
	cfg.DialControl = protectSocket
	listen := statusListen(cfg)
	remote := strings.TrimSpace(cfg.ServerAddr)

	state.mu.Lock()
	if state.status == statusStarting || state.status == statusRunning {
		state.mu.Unlock()
		return errors.New("tcptun is already running")
	}
	if state.cancel != nil {
		state.mu.Unlock()
		return errors.New("tcptun is stopping")
	}

	log := io.Writer(io.Discard)
	if cfg.Verbose {
		log = logWriter{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	state.status = statusStarting
	state.cancel = cancel
	state.done = done
	state.lastError = nil
	state.listen = listen
	state.remote = remote
	state.mu.Unlock()
	emitStatus(statusEvent{
		State:  "starting",
		Phase:  "Start called",
		Listen: listen,
		Remote: remote,
	})

	go func() {
		err := tcptun.RunProxy(ctx, cfg, statusLogWriter{out: log, listen: listen, remote: remote})
		state.mu.Lock()
		if err != nil && ctx.Err() == nil {
			state.status = statusError
			state.lastError = err
		} else {
			state.status = statusStopped
			state.lastError = nil
		}
		state.cancel = nil
		state.done = nil
		state.mu.Unlock()
		if err != nil && ctx.Err() == nil {
			emitStatus(statusEvent{
				State:     "error",
				Phase:     "Proxy service stopped with error",
				Listen:    listen,
				Remote:    remote,
				LastError: sanitizeErrorSummary(err),
			})
		} else {
			emitStatus(statusEvent{
				State:  "stopped",
				Phase:  "Proxy service stopped",
				Listen: listen,
				Remote: remote,
			})
		}
		done <- err
		close(done)
	}()

	return nil
}

// Stop cancels the running tcptun instance and waits for it to exit.
func Stop() error {
	state.mu.Lock()
	cancel := state.cancel
	done := state.done
	listen := state.listen
	remote := state.remote
	if cancel == nil || done == nil {
		state.status = statusStopped
		state.lastError = nil
		state.mu.Unlock()
		emitStatus(statusEvent{
			State:  "stopped",
			Phase:  "Proxy service is already stopped",
			Listen: listen,
			Remote: remote,
		})
		return nil
	}
	state.mu.Unlock()

	emitStatus(statusEvent{
		State:  "stopping",
		Phase:  "Stop called",
		Listen: listen,
		Remote: remote,
	})
	cancel()
	timer := time.NewTimer(stopTimeout)
	defer timer.Stop()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			emitStatus(statusEvent{
				State:     "error",
				Phase:     "Proxy service stopped with error",
				Listen:    listen,
				Remote:    remote,
				LastError: sanitizeErrorSummary(err),
			})
			return err
		}
		emitStatus(statusEvent{
			State:  "stopped",
			Phase:  "Stop completed",
			Listen: listen,
			Remote: remote,
		})
		return nil
	case <-timer.C:
		err := fmt.Errorf("timed out waiting %s for tcptun to stop", stopTimeout)
		emitStatus(statusEvent{
			State:     "error",
			Phase:     "Stop timed out",
			Listen:    listen,
			Remote:    remote,
			LastError: sanitizeErrorSummary(err),
		})
		return err
	}
}

// Status returns Stopped, Starting, Running, or Error.
func Status() string {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.status
}

// SetLogCallback installs the Android log callback. Passing nil disables it.
func SetLogCallback(cb LogCallback) {
	state.mu.Lock()
	state.logCB = cb
	state.mu.Unlock()
}

// SetStatusCallback installs the Android status callback. Passing nil disables it.
func SetStatusCallback(cb StatusCallback) {
	state.mu.Lock()
	state.statusCB = cb
	state.mu.Unlock()
}

// SetSocketProtector installs the Android socket protector. Passing nil disables it.
func SetSocketProtector(p SocketProtector) {
	state.mu.Lock()
	state.protector = p
	state.mu.Unlock()
}

func setStatus(status string) {
	state.mu.Lock()
	state.status = status
	state.mu.Unlock()
}

func setError(err error) {
	state.mu.Lock()
	state.status = statusError
	state.lastError = err
	state.mu.Unlock()
	emitStatus(statusEvent{
		State:     "error",
		Phase:     "Start failed",
		LastError: sanitizeErrorSummary(err),
	})
}

type logWriter struct{}

func (logWriter) Write(p []byte) (int, error) {
	state.mu.Lock()
	cb := state.logCB
	state.mu.Unlock()
	if cb == nil {
		return len(p), nil
	}

	for _, line := range strings.SplitAfter(string(p), "\n") {
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			continue
		}
		cb.OnLog(trimmed)
	}
	return len(p), nil
}

type statusLogWriter struct {
	out    io.Writer
	listen string
	remote string
}

func (w statusLogWriter) Write(p []byte) (int, error) {
	if w.out == nil {
		w.out = io.Discard
	}
	for _, line := range strings.SplitAfter(string(p), "\n") {
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			continue
		}
		w.observe(trimmed)
	}
	n, err := w.out.Write(p)
	if err != nil {
		return n, err
	}
	if n != len(p) {
		return n, io.ErrShortWrite
	}
	return n, nil
}

func (w statusLogWriter) observe(line string) {
	lower := strings.ToLower(line)
	switch {
	case strings.Contains(lower, "listening on "):
		listen := extractListenAddr(line, w.listen)
		emitStatus(statusEvent{
			State:  "listening",
			Phase:  "Local listener is ready",
			Listen: listen,
			Remote: w.remote,
		})
		emitStatus(statusEvent{
			State:  "running",
			Phase:  "Proxy service is accepting connections",
			Listen: listen,
			Remote: w.remote,
		})
	case strings.Contains(lower, "retrying"):
		emitStatus(statusEvent{
			State:     "reconnecting",
			Phase:     "Retrying connection",
			Listen:    w.listen,
			Remote:    w.remote,
			LastError: sanitizeErrorSummary(errors.New(line)),
		})
	case strings.Contains(lower, " failed") || strings.Contains(lower, " error"):
		emitStatus(statusEvent{
			State:     "degraded",
			Phase:     "Runtime connection issue",
			Listen:    w.listen,
			Remote:    w.remote,
			LastError: sanitizeErrorSummary(errors.New(line)),
		})
	}
}

func emitStatus(event statusEvent) {
	if strings.TrimSpace(event.State) == "" {
		return
	}
	if event.TimestampMS == 0 {
		event.TimestampMS = time.Now().UnixMilli()
	}
	if event.LastError != "" {
		event.LastError = sanitizeErrorSummary(errors.New(event.LastError))
	}

	state.mu.Lock()
	if event.Listen == "" {
		event.Listen = state.listen
	}
	if event.Remote == "" {
		event.Remote = state.remote
	}
	if event.LastError != "" {
		state.lastError = errors.New(event.LastError)
	}
	state.status = statusFromEventState(event.State)
	cb := state.statusCB
	state.mu.Unlock()

	if cb == nil {
		return
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	cb.OnStatus(string(payload))
}

func statusFromEventState(eventState string) string {
	switch eventState {
	case "starting":
		return statusStarting
	case "listening", "running", "upstream_connecting", "upstream_connected", "degraded", "reconnecting":
		return statusRunning
	case "stopping", "stopped":
		return statusStopped
	case "error":
		return statusError
	default:
		return state.status
	}
}

func statusListen(cfg tcptun.Config) string {
	for _, addr := range cfg.ListenAddrs {
		if trimmed := strings.TrimSpace(addr); trimmed != "" {
			return trimmed
		}
	}
	return strings.TrimSpace(cfg.ListenAddr)
}

func extractListenAddr(line string, fallback string) string {
	const marker = "listening on "
	lower := strings.ToLower(line)
	idx := strings.Index(lower, marker)
	if idx < 0 {
		return fallback
	}
	rest := strings.TrimSpace(line[idx+len(marker):])
	if rest == "" {
		return fallback
	}
	addr := strings.Fields(rest)[0]
	addr = strings.TrimRight(addr, ",")
	if addr == "" {
		return fallback
	}
	return addr
}

func sanitizeErrorSummary(err error) string {
	if err == nil {
		return ""
	}
	summary := strings.TrimSpace(err.Error())
	if summary == "" {
		return ""
	}
	summary = sensitiveFieldPattern.ReplaceAllString(summary, `$1$2<redacted>`)
	summary = uuidPattern.ReplaceAllString(summary, "<redacted>")
	const maxSummary = 512
	if len(summary) > maxSummary {
		summary = summary[:maxSummary]
	}
	return summary
}

func protectSocket(network, address string, c syscall.RawConn) error {
	state.mu.Lock()
	protector := state.protector
	state.mu.Unlock()
	if protector == nil {
		return nil
	}

	var protectErr error
	if err := c.Control(func(fd uintptr) {
		if !protector.Protect(int64(fd)) {
			protectErr = fmt.Errorf("socket protector rejected fd %d for %s %s", fd, network, address)
		}
	}); err != nil {
		return err
	}
	return protectErr
}

type mobileConfig struct {
	Mode                   string       `json:"mode,omitempty"`
	ListenAddr             string       `json:"listen_addr,omitempty"`
	ListenAddrs            []string     `json:"listen_addrs,omitempty"`
	LocalListenAddr        string       `json:"local_listen_addr,omitempty"`
	ServerAddr             string       `json:"server_addr,omitempty"`
	Token                  string       `json:"token,omitempty"`
	TunnelProtocol         string       `json:"tunnel_protocol,omitempty"`
	TunnelTransport        string       `json:"tunnel_transport,omitempty"`
	TunnelPath             string       `json:"tunnel_path,omitempty"`
	TunnelTLS              *bool        `json:"tunnel_tls,omitempty"`
	TunnelTLSCert          string       `json:"tunnel_tls_cert,omitempty"`
	TunnelTLSKey           string       `json:"tunnel_tls_key,omitempty"`
	TunnelTLSServerName    string       `json:"tunnel_tls_server_name,omitempty"`
	TunnelTLSInsecure      *bool        `json:"tunnel_tls_insecure,omitempty"`
	TunnelSecurity         string       `json:"tunnel_security,omitempty"`
	TunnelFlow             string       `json:"tunnel_flow,omitempty"`
	RealityServerName      string       `json:"reality_server_name,omitempty"`
	RealityServerNames     []string     `json:"reality_server_names,omitempty"`
	RealityFingerprint     string       `json:"reality_fingerprint,omitempty"`
	RealityPublicKey       string       `json:"reality_public_key,omitempty"`
	RealityPrivateKey      string       `json:"reality_private_key,omitempty"`
	RealityShortID         string       `json:"reality_short_id,omitempty"`
	RealityShortIDs        []string     `json:"reality_short_ids,omitempty"`
	RealityDest            string       `json:"reality_dest,omitempty"`
	RealitySpiderX         string       `json:"reality_spider_x,omitempty"`
	TunnelMux              *bool        `json:"tunnel_mux,omitempty"`
	GatewayIP              string       `json:"gateway_ip,omitempty"`
	GatewayPort            int          `json:"gateway_port,omitempty"`
	UpstreamProtocol       string       `json:"upstream_protocol,omitempty"`
	SOCKS5Username         string       `json:"socks5_username,omitempty"`
	SOCKS5Password         string       `json:"socks5_password,omitempty"`
	UpstreamSOCKS5Username string       `json:"upstream_socks5_username,omitempty"`
	UpstreamSOCKS5Password string       `json:"upstream_socks5_password,omitempty"`
	ConfigPath             string       `json:"config_path,omitempty"`
	RouteConfigPath        string       `json:"route_config_path,omitempty"`
	DirectProbeTimeout     jsonDuration `json:"direct_probe_timeout,omitempty"`
	HeartbeatInterval      jsonDuration `json:"heartbeat_interval,omitempty"`
	ConnectionIdleTimeout  jsonDuration `json:"connection_idle_timeout,omitempty"`
	UDPSessionTimeout      jsonDuration `json:"udp_session_timeout,omitempty"`
	RetryInitialInterval   jsonDuration `json:"retry_initial_interval,omitempty"`
	RetryMaxInterval       jsonDuration `json:"retry_max_interval,omitempty"`
	ScanWorkers            int          `json:"scan_workers,omitempty"`
	BufferSize             int          `json:"buffer_size,omitempty"`
	Verbose                bool         `json:"verbose,omitempty"`
}

type jsonDuration struct {
	value time.Duration
	set   bool
}

func (d *jsonDuration) UnmarshalJSON(data []byte) error {
	if d == nil {
		return errors.New("duration target is nil")
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*d = jsonDuration{}
		return nil
	}
	if strings.HasPrefix(trimmed, "\"") {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		text = strings.TrimSpace(text)
		if text == "" {
			*d = jsonDuration{}
			return nil
		}
		duration, err := time.ParseDuration(text)
		if err != nil {
			return err
		}
		d.value = duration
		d.set = true
		return nil
	}
	var nanos int64
	if err := json.Unmarshal(data, &nanos); err != nil {
		return err
	}
	d.value = time.Duration(nanos)
	d.set = true
	return nil
}

func parseConfigJSON(configJSON string) (tcptun.Config, error) {
	cfg := tcptun.DefaultConfig()
	if strings.TrimSpace(configJSON) == "" {
		return cfg, nil
	}

	var mobile mobileConfig
	if err := json.Unmarshal([]byte(configJSON), &mobile); err != nil {
		return tcptun.Config{}, err
	}

	applyString(&cfg.Mode, mobile.Mode)
	applyListenConfig(&cfg, mobile)
	applyString(&cfg.ServerAddr, mobile.ServerAddr)
	applyString(&cfg.Token, mobile.Token)
	applyString(&cfg.TunnelProtocol, mobile.TunnelProtocol)
	applyString(&cfg.TunnelTransport, mobile.TunnelTransport)
	applyString(&cfg.TunnelPath, mobile.TunnelPath)
	applyBool(&cfg.TunnelTLS, mobile.TunnelTLS)
	applyString(&cfg.TunnelTLSCert, mobile.TunnelTLSCert)
	applyString(&cfg.TunnelTLSKey, mobile.TunnelTLSKey)
	applyString(&cfg.TunnelTLSServerName, mobile.TunnelTLSServerName)
	applyBool(&cfg.TunnelTLSInsecure, mobile.TunnelTLSInsecure)
	applyString(&cfg.TunnelSecurity, mobile.TunnelSecurity)
	applyString(&cfg.TunnelFlow, mobile.TunnelFlow)
	applyString(&cfg.RealityServerName, mobile.RealityServerName)
	applyStringSlice(&cfg.RealityServerNames, mobile.RealityServerNames)
	applyString(&cfg.RealityFingerprint, mobile.RealityFingerprint)
	applyString(&cfg.RealityPublicKey, mobile.RealityPublicKey)
	applyString(&cfg.RealityPrivateKey, mobile.RealityPrivateKey)
	applyString(&cfg.RealityShortID, mobile.RealityShortID)
	applyStringSlice(&cfg.RealityShortIDs, mobile.RealityShortIDs)
	applyString(&cfg.RealityDest, mobile.RealityDest)
	applyString(&cfg.RealitySpiderX, mobile.RealitySpiderX)
	applyBool(&cfg.TunnelMux, mobile.TunnelMux)
	applyString(&cfg.GatewayIP, mobile.GatewayIP)
	applyInt(&cfg.GatewayPort, mobile.GatewayPort)
	applyString(&cfg.UpstreamProtocol, mobile.UpstreamProtocol)
	applyString(&cfg.SOCKS5Username, mobile.SOCKS5Username)
	applyString(&cfg.SOCKS5Password, mobile.SOCKS5Password)
	applyString(&cfg.UpstreamSOCKS5Username, mobile.UpstreamSOCKS5Username)
	applyString(&cfg.UpstreamSOCKS5Password, mobile.UpstreamSOCKS5Password)
	applyString(&cfg.ConfigPath, mobile.ConfigPath)
	applyString(&cfg.RouteConfigPath, mobile.RouteConfigPath)
	applyDuration(&cfg.DirectProbeTimeout, mobile.DirectProbeTimeout)
	applyDuration(&cfg.HeartbeatInterval, mobile.HeartbeatInterval)
	applyDuration(&cfg.ConnectionIdleTimeout, mobile.ConnectionIdleTimeout)
	applyDuration(&cfg.UDPSessionTimeout, mobile.UDPSessionTimeout)
	applyDuration(&cfg.RetryInitialInterval, mobile.RetryInitialInterval)
	applyDuration(&cfg.RetryMaxInterval, mobile.RetryMaxInterval)
	applyInt(&cfg.ScanWorkers, mobile.ScanWorkers)
	applyInt(&cfg.BufferSize, mobile.BufferSize)
	cfg.Verbose = mobile.Verbose
	return cfg, nil
}

func applyListenConfig(cfg *tcptun.Config, mobile mobileConfig) {
	if len(mobile.ListenAddrs) > 0 {
		cfg.ListenAddr = ""
		cfg.ListenAddrs = compactStrings(mobile.ListenAddrs)
		return
	}
	if strings.TrimSpace(mobile.LocalListenAddr) != "" {
		cfg.ListenAddr = strings.TrimSpace(mobile.LocalListenAddr)
		cfg.ListenAddrs = []string{cfg.ListenAddr}
		return
	}
	if strings.TrimSpace(mobile.ListenAddr) != "" {
		cfg.ListenAddr = strings.TrimSpace(mobile.ListenAddr)
		cfg.ListenAddrs = []string{cfg.ListenAddr}
	}
}

func compactStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func applyString(target *string, value string) {
	if strings.TrimSpace(value) != "" {
		*target = strings.TrimSpace(value)
	}
}

func applyStringSlice(target *[]string, value []string) {
	if len(value) > 0 {
		*target = compactStrings(value)
	}
}

func applyBool(target *bool, value *bool) {
	if value != nil {
		*target = *value
	}
}

func applyInt(target *int, value int) {
	if value != 0 {
		*target = value
	}
}

func applyDuration(target *time.Duration, value jsonDuration) {
	if value.set {
		*target = value.value
	}
}
