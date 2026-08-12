package vmsdk_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Geogboe/boxy/pkg/eventstream"
	"github.com/Geogboe/boxy/pkg/vmsdk"
)

// generateKeyPair returns an ssh.Signer and the PEM-encoded private key bytes.
func generateKeyPair(t *testing.T) (ssh.Signer, []byte) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("ssh signer from key: %v", err)
	}
	privDER := x509.MarshalPKCS1PrivateKey(priv)
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: privDER})
	return signer, privPEM
}

// startTestSSHServer starts an in-process SSH server on a random port.
// handler receives the full command string and returns (stdout, exitCode).
// Returns (host, port, clientPrivateKeyPEM).
func startTestSSHServer(t *testing.T, handler func(cmd string) (string, int)) (host, port string, clientPEM []byte) {
	t.Helper()

	clientSigner, clientPEM := generateKeyPair(t)
	hostSigner, _ := generateKeyPair(t)
	authorizedKeyBytes := clientSigner.PublicKey().Marshal()

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) == string(authorizedKeyBytes) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unknown public key")
		},
	}
	cfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() }) //nolint:errcheck

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveSSHConn(conn, cfg, handler)
		}
	}()

	tcpAddr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", fmt.Sprintf("%d", tcpAddr.Port), clientPEM
}

func serveSSHConn(conn net.Conn, cfg *ssh.ServerConfig, handler func(string) (string, int)) {
	serverConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer serverConn.Close() //nolint:errcheck
	go ssh.DiscardRequests(reqs)
	for ch := range chans {
		if ch.ChannelType() != "session" {
			ch.Reject(ssh.UnknownChannelType, "unsupported channel type") //nolint:errcheck
			continue
		}
		channel, requests, err := ch.Accept()
		if err != nil {
			continue
		}
		go serveSession(channel, requests, handler)
	}
}

func serveSession(ch ssh.Channel, reqs <-chan *ssh.Request, handler func(string) (string, int)) {
	defer ch.Close() //nolint:errcheck
	for req := range reqs {
		switch req.Type {
		case "exec":
			if req.WantReply {
				req.Reply(true, nil) //nolint:errcheck
			}
			// SSH exec payload: uint32 length + command bytes.
			if len(req.Payload) < 4 {
				sendExitStatus(ch, 1)
				return
			}
			cmdLen := binary.BigEndian.Uint32(req.Payload[:4])
			if int(cmdLen) > len(req.Payload)-4 {
				sendExitStatus(ch, 1)
				return
			}
			cmd := string(req.Payload[4 : 4+cmdLen])
			stdout, exitCode := handler(cmd)
			io.WriteString(ch, stdout) //nolint:errcheck
			sendExitStatus(ch, uint32(exitCode))
			return
		default:
			if req.WantReply {
				req.Reply(false, nil) //nolint:errcheck
			}
		}
	}
}

func sendExitStatus(ch ssh.Channel, code uint32) {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, code)
	ch.SendRequest("exit-status", false, payload) //nolint:errcheck
}

// Tests

func TestSSHExec_Exec_PasswordAuth(t *testing.T) {
	const wantOutput = "hello from guest\n"
	host, port, _ := startTestSSHServer(t, func(cmd string) (string, int) {
		return wantOutput, 0
	})

	// Note: password auth requires the server to accept passwords.
	// Our test server only accepts public keys, so we test key auth below.
	// This test uses a server that accepts any password.
	passCfg := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, _ []byte) (*ssh.Permissions, error) {
			return &ssh.Permissions{}, nil
		},
	}
	hostSigner, _ := generateKeyPair(t)
	passCfg.AddHostKey(hostSigner)

	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln2.Close() }) //nolint:errcheck
	go func() {
		for {
			conn, err := ln2.Accept()
			if err != nil {
				return
			}
			go serveSSHConn(conn, passCfg, func(cmd string) (string, int) {
				return wantOutput, 0
			})
		}
	}()
	_ = host
	_ = port
	passPort := fmt.Sprintf("%d", ln2.Addr().(*net.TCPAddr).Port)

	exec := &vmsdk.SSHExec{
		Host:     "127.0.0.1",
		Port:     passPort,
		User:     "testuser",
		Password: "testpass",
	}
	result, err := exec.Exec(context.Background(), "echo", "hello from guest")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.Stdout != wantOutput {
		t.Errorf("stdout = %q, want %q", result.Stdout, wantOutput)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}
}

func TestSSHExec_Exec_PublicKeyAuth(t *testing.T) {
	const wantOutput = "key auth works\n"
	host, port, clientPEM := startTestSSHServer(t, func(cmd string) (string, int) {
		// Verify the command is properly shell-quoted.
		if !strings.Contains(cmd, "key") {
			return "unexpected command: " + cmd + "\n", 1
		}
		return wantOutput, 0
	})

	exec := &vmsdk.SSHExec{
		Host:       host,
		Port:       port,
		User:       "testuser",
		PrivateKey: clientPEM,
	}
	result, err := exec.Exec(context.Background(), "echo", "key auth works")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.Stdout != wantOutput {
		t.Errorf("stdout = %q, want %q", result.Stdout, wantOutput)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}
}

func TestSSHExec_Exec_NonZeroExitCode(t *testing.T) {
	host, port, clientPEM := startTestSSHServer(t, func(cmd string) (string, int) {
		return "error output\n", 42
	})

	exec := &vmsdk.SSHExec{
		Host:       host,
		Port:       port,
		User:       "testuser",
		PrivateKey: clientPEM,
	}
	result, err := exec.Exec(context.Background(), "false")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.ExitCode != 42 {
		t.Errorf("exit code = %d, want 42", result.ExitCode)
	}
}

func TestSSHExec_Exec_InvalidPrivateKey(t *testing.T) {
	exec := &vmsdk.SSHExec{
		Host:       "127.0.0.1",
		Port:       "22",
		User:       "testuser",
		PrivateKey: []byte("not a valid pem key"),
	}
	_, err := exec.Exec(context.Background(), "echo", "hi")
	if err == nil {
		t.Fatal("expected error for invalid private key")
	}
	if !strings.Contains(err.Error(), "parse private key") {
		t.Errorf("error %q should mention parse private key", err.Error())
	}
}

func TestSSHExec_Exec_ConnectionRefused(t *testing.T) {
	exec := &vmsdk.SSHExec{
		Host:     "127.0.0.1",
		Port:     "1", // port 1 is always refused
		User:     "testuser",
		Password: "pw",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2000000000) // 2s
	defer cancel()
	_, err := exec.Exec(ctx, "echo", "hi")
	if err == nil {
		t.Fatal("expected connection error")
	}
}

// --- ExecStream tests ---
//
// These use a variant of the fake SSH server that hands the handler direct
// access to the SSH channel, instead of a single (stdout, exitCode) return
// value: ExecStream demultiplexes the regular data stream (stdout) from the
// extended data stream (stderr) and forwards each independently as it
// arrives, which startTestSSHServer's single combined-output handler can't
// exercise, and a handler that can block lets a cancellation test simulate
// a long-running remote command.

// collectingStreamSink records every event's channel/payload for assertion,
// and reassembles per-channel output preserving that channel's own arrival
// order (stdout and stderr are read by independent goroutines in
// SSHExec.ExecStream, so only within-channel order is guaranteed).
type collectingStreamSink struct {
	mu     sync.Mutex
	events []eventstream.Event
}

func (s *collectingStreamSink) Send(_ context.Context, event eventstream.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *collectingStreamSink) channel(name eventstream.Channel) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	for _, e := range s.events {
		if e.Channel == name {
			b.Write(e.Payload)
		}
	}
	return b.String()
}

// startTestSSHServerRaw is like startTestSSHServer but hands the handler the
// live ssh.Channel and the exec command string instead of collapsing the
// exchange to a single (stdout, exitCode) return. The handler is
// responsible for writing output (via io.WriteString(ch, ...) for stdout,
// ch.Stderr() for stderr) and sending its own exit status.
func startTestSSHServerRaw(t *testing.T, handler func(ch ssh.Channel, cmd string)) (host, port string, clientPEM []byte) {
	t.Helper()

	clientSigner, clientPEM := generateKeyPair(t)
	hostSigner, _ := generateKeyPair(t)
	authorizedKeyBytes := clientSigner.PublicKey().Marshal()

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) == string(authorizedKeyBytes) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unknown public key")
		},
	}
	cfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() }) //nolint:errcheck

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveSSHConnRaw(conn, cfg, handler)
		}
	}()

	tcpAddr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", fmt.Sprintf("%d", tcpAddr.Port), clientPEM
}

func serveSSHConnRaw(conn net.Conn, cfg *ssh.ServerConfig, handler func(ch ssh.Channel, cmd string)) {
	serverConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer serverConn.Close() //nolint:errcheck
	go ssh.DiscardRequests(reqs)
	for ch := range chans {
		if ch.ChannelType() != "session" {
			ch.Reject(ssh.UnknownChannelType, "unsupported channel type") //nolint:errcheck
			continue
		}
		channel, requests, err := ch.Accept()
		if err != nil {
			continue
		}
		go serveSessionRaw(channel, requests, handler)
	}
}

func serveSessionRaw(ch ssh.Channel, reqs <-chan *ssh.Request, handler func(ch ssh.Channel, cmd string)) {
	defer ch.Close() //nolint:errcheck
	for req := range reqs {
		switch req.Type {
		case "exec":
			if req.WantReply {
				req.Reply(true, nil) //nolint:errcheck
			}
			if len(req.Payload) < 4 {
				sendExitStatus(ch, 1)
				return
			}
			cmdLen := binary.BigEndian.Uint32(req.Payload[:4])
			if int(cmdLen) > len(req.Payload)-4 {
				sendExitStatus(ch, 1)
				return
			}
			cmd := string(req.Payload[4 : 4+cmdLen])
			handler(ch, cmd)
			return
		default:
			if req.WantReply {
				req.Reply(false, nil) //nolint:errcheck
			}
		}
	}
}

func TestSSHExec_ExecStream_ForwardsStdoutAndStderrIncrementally(t *testing.T) {
	host, port, clientPEM := startTestSSHServerRaw(t, func(ch ssh.Channel, _ string) {
		io.WriteString(ch, "out-1 ")          //nolint:errcheck
		io.WriteString(ch.Stderr(), "err-1 ") //nolint:errcheck
		io.WriteString(ch, "out-2")           //nolint:errcheck
		sendExitStatus(ch, 9)
	})

	exec := &vmsdk.SSHExec{Host: host, Port: port, User: "testuser", PrivateKey: clientPEM}
	sink := &collectingStreamSink{}
	result, err := exec.ExecStream(context.Background(), "echo", []string{"hi"}, sink)
	if err != nil {
		t.Fatalf("ExecStream: %v", err)
	}
	if result.ExitCode != 9 {
		t.Errorf("exit code = %d, want 9", result.ExitCode)
	}
	if got := sink.channel("stdout"); got != "out-1 out-2" {
		t.Errorf("stdout = %q, want %q", got, "out-1 out-2")
	}
	if got := sink.channel("stderr"); got != "err-1 " {
		t.Errorf("stderr = %q, want %q", got, "err-1 ")
	}
}

func TestSSHExec_ExecStream_RequiresSink(t *testing.T) {
	exec := &vmsdk.SSHExec{Host: "127.0.0.1", Port: "22", User: "testuser", Password: "pw"}
	_, err := exec.ExecStream(context.Background(), "echo", nil, nil)
	if err == nil {
		t.Fatal("expected error for a nil sink")
	}
}

// TestSSHExec_ExecStream_ContextCancelReturnsContextError proves ExecStream
// abandons a still-running remote command as soon as ctx is cancelled
// (client disconnect behavior), rather than blocking until the server-side
// command eventually finishes on its own.
func TestSSHExec_ExecStream_ContextCancelReturnsContextError(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	host, port, clientPEM := startTestSSHServerRaw(t, func(_ ssh.Channel, _ string) {
		<-block // never responds until the test cleans up
	})

	exec := &vmsdk.SSHExec{Host: host, Port: port, User: "testuser", PrivateKey: clientPEM}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := exec.ExecStream(ctx, "sleep", []string{"100"}, &collectingStreamSink{})
		errCh <- err
	}()

	time.Sleep(100 * time.Millisecond) // let ExecStream reach its wait loop
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for ExecStream to return after cancel")
	}
}
