package proxy

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func TestVMessAEADRequestAndResponse(t *testing.T) {
	token := "33333333-3333-4333-8333-333333333333"
	var wire bytes.Buffer
	session, err := writeVMessTCPRequest(&wire, token, "example.com", 443)
	if err != nil {
		t.Fatal(err)
	}
	req, err := readVMessTCPRequest(bytes.NewReader(wire.Bytes()), token)
	if err != nil {
		t.Fatal(err)
	}
	if req.host != "example.com" || req.port != 443 {
		t.Fatalf("request = %s:%d, want example.com:443", req.host, req.port)
	}
	if req.vmessSession == nil {
		t.Fatal("missing VMess session")
	}
	if *req.vmessSession != session {
		t.Fatal("server session does not match client session")
	}

	var response bytes.Buffer
	if err := writeVMessResponseHeader(&response, *req.vmessSession); err != nil {
		t.Fatal(err)
	}
	serverSide, err := newVMessResponseConn(writeOnlyConn{Writer: &response}, *req.vmessSession)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := serverSide.Write([]byte("OK")); err != nil {
		t.Fatal(err)
	}
	clientReader := bytes.NewReader(response.Bytes())
	clientSide := newVMessClientConn(readOnlyConn{Reader: clientReader}, session)
	reply := make([]byte, 2)
	if _, err := io.ReadFull(clientSide, reply); err != nil {
		t.Fatal(err)
	}
	if string(reply) != "OK" {
		t.Fatalf("reply = %q, want OK", reply)
	}
}

type writeOnlyConn struct {
	io.Writer
}

func (writeOnlyConn) Read([]byte) (int, error)         { return 0, io.ErrClosedPipe }
func (writeOnlyConn) Close() error                     { return nil }
func (writeOnlyConn) LocalAddr() net.Addr              { return addrString{network: "test", address: "local"} }
func (writeOnlyConn) RemoteAddr() net.Addr             { return addrString{network: "test", address: "remote"} }
func (writeOnlyConn) SetDeadline(time.Time) error      { return nil }
func (writeOnlyConn) SetReadDeadline(time.Time) error  { return nil }
func (writeOnlyConn) SetWriteDeadline(time.Time) error { return nil }

type readOnlyConn struct {
	io.Reader
}

func (readOnlyConn) Write([]byte) (int, error)        { return 0, io.ErrClosedPipe }
func (readOnlyConn) Close() error                     { return nil }
func (readOnlyConn) LocalAddr() net.Addr              { return addrString{network: "test", address: "local"} }
func (readOnlyConn) RemoteAddr() net.Addr             { return addrString{network: "test", address: "remote"} }
func (readOnlyConn) SetDeadline(time.Time) error      { return nil }
func (readOnlyConn) SetReadDeadline(time.Time) error  { return nil }
func (readOnlyConn) SetWriteDeadline(time.Time) error { return nil }
