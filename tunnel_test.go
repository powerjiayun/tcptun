package tcptun

import (
	"bytes"
	"errors"
	"testing"
)

func TestNativeTunnelRequestIsEncrypted(t *testing.T) {
	const token = "secret-token"
	var buf bytes.Buffer
	req := tunnelRequest{
		cmd:   tunnelCmdTCPConnect,
		token: token,
		host:  "example.com",
		port:  443,
	}
	if err := writeTunnelRequest(&buf, req); err != nil {
		t.Fatal(err)
	}
	packet := buf.Bytes()
	if bytes.Contains(packet, []byte("PSK1")) {
		t.Fatalf("native request contains legacy magic: %x", packet)
	}
	if bytes.Contains(packet, []byte(token)) {
		t.Fatalf("native request contains token: %x", packet)
	}
	if bytes.Contains(packet, []byte(req.host)) {
		t.Fatalf("native request contains host: %x", packet)
	}

	got, err := readTunnelRequest(bytes.NewReader(packet), token)
	if err != nil {
		t.Fatal(err)
	}
	if got.cmd != req.cmd || got.host != req.host || got.port != req.port {
		t.Fatalf("request = %+v, want %+v", got, req)
	}
}

func TestNativeTunnelRequestRejectsWrongToken(t *testing.T) {
	var buf bytes.Buffer
	if err := writeTunnelRequest(&buf, tunnelRequest{
		cmd:   tunnelCmdTCPConnect,
		token: "good-token",
		host:  "example.com",
		port:  443,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := readTunnelRequest(bytes.NewReader(buf.Bytes()), "bad-token"); !errors.Is(err, errTunnelUnauthorized) {
		t.Fatalf("read with wrong token error = %v, want %v", err, errTunnelUnauthorized)
	}
}

func TestNativeTunnelResponseIsEncrypted(t *testing.T) {
	const token = "secret-token"
	const message = "target rejected"
	var buf bytes.Buffer
	if err := writeTunnelResponse(&buf, token, tunnelStatusError, message); err != nil {
		t.Fatal(err)
	}
	packet := buf.Bytes()
	if bytes.Contains(packet, []byte("PSK1")) {
		t.Fatalf("native response contains legacy magic: %x", packet)
	}
	if bytes.Contains(packet, []byte(message)) {
		t.Fatalf("native response contains message: %x", packet)
	}
	if err := readTunnelResponse(bytes.NewReader(packet), token); err == nil || err.Error() != message {
		t.Fatalf("response error = %v, want %q", err, message)
	}
}
