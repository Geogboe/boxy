package vmsdk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Geogboe/boxy/pkg/eventstream"
)

// SSHExec implements GuestExec over SSH. A new connection is made per Exec call
// (no pooling needed for Boxy's short-lived VM use case).
type SSHExec struct {
	Host       string
	Port       string // default "22"
	User       string
	PrivateKey []byte // PEM-encoded private key; used when non-empty
	Password   string // used when no PrivateKey is provided
}

// Exec opens an SSH session, runs the command, and returns stdout/stderr/exit code.
func (s *SSHExec) Exec(ctx context.Context, cmd string, args ...string) (*ExecResult, error) {
	client, session, err := s.startSession(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()  //nolint:errcheck
	defer session.Close() //nolint:errcheck

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	if err := session.Start(shellJoin(cmd, args)); err != nil {
		return nil, fmt.Errorf("ssh start: %w", err)
	}

	exitCode := 0
	if runErr := session.Wait(); runErr != nil {
		var exitErr *ssh.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitStatus()
		} else {
			return nil, fmt.Errorf("ssh run: %w", runErr)
		}
	}

	return &ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, nil
}

// ExecStream opens an SSH session and forwards stdout/stderr as data arrives.
func (s *SSHExec) ExecStream(ctx context.Context, cmd string, args []string, sink eventstream.Sink) (*ExecResult, error) {
	if sink == nil {
		return nil, errors.New("ssh stream sink is required")
	}
	client, session, err := s.startSession(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()  //nolint:errcheck
	defer session.Close() //nolint:errcheck

	stdout, err := session.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ssh stdout pipe: %w", err)
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("ssh stderr pipe: %w", err)
	}
	if err := session.Start(shellJoin(cmd, args)); err != nil {
		return nil, fmt.Errorf("ssh start: %w", err)
	}

	type chunk struct {
		channel eventstream.Channel
		data    []byte
		err     error
	}
	chunks := make(chan chunk)
	var readers sync.WaitGroup
	readPipe := func(channel eventstream.Channel, reader io.Reader) {
		readers.Add(1)
		go func() {
			defer readers.Done()
			buf := make([]byte, 32*1024)
			for {
				n, readErr := reader.Read(buf)
				if n > 0 {
					payload := append([]byte(nil), buf[:n]...)
					select {
					case chunks <- chunk{channel: channel, data: payload}:
					case <-ctx.Done():
						return
					}
				}
				if readErr != nil {
					if !errors.Is(readErr, io.EOF) {
						select {
						case chunks <- chunk{channel: channel, err: readErr}:
						case <-ctx.Done():
						}
					}
					return
				}
			}
		}()
	}
	readPipe(eventstream.Channel("stdout"), stdout)
	readPipe(eventstream.Channel("stderr"), stderr)

	readDone := make(chan struct{})
	go func() {
		readers.Wait()
		close(chunks)
		close(readDone)
	}()
	waitCh := make(chan error, 1)
	go func() { waitCh <- session.Wait() }()

	var waitErr error
	waited := false
	for chunks != nil || !waited {
		select {
		case <-ctx.Done():
			_ = session.Close()
			return nil, ctx.Err()
		case item, ok := <-chunks:
			if !ok {
				chunks = nil
				continue
			}
			if item.err != nil {
				_ = session.Close()
				return nil, fmt.Errorf("ssh read %s: %w", item.channel, item.err)
			}
			if err := sink.Send(ctx, eventstream.Event{Kind: eventstream.Data, Channel: item.channel, Payload: item.data}); err != nil {
				_ = session.Close()
				return nil, err
			}
		case err := <-waitCh:
			waitErr = err
			waited = true
		}
	}
	<-readDone

	exitCode := 0
	if waitErr != nil {
		var exitErr *ssh.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitStatus()
		} else {
			return nil, fmt.Errorf("ssh run: %w", waitErr)
		}
	}
	return &ExecResult{ExitCode: exitCode}, nil
}

func (s *SSHExec) startSession(ctx context.Context) (*ssh.Client, *ssh.Session, error) {
	port := s.Port
	if port == "" {
		port = "22"
	}
	addr := net.JoinHostPort(s.Host, port)

	authMethods := make([]ssh.AuthMethod, 0, 2)
	if len(s.PrivateKey) > 0 {
		signer, err := ssh.ParsePrivateKey(s.PrivateKey)
		if err != nil {
			return nil, nil, fmt.Errorf("parse private key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}
	if s.Password != "" {
		authMethods = append(authMethods, ssh.Password(s.Password))
	}

	clientCfg := &ssh.ClientConfig{
		User:            s.User,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // Short-lived boxy VMs; GUID is the trust anchor
		Timeout:         30 * time.Second,
	}
	client, err := dialSSHContext(ctx, addr, clientCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	session, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("ssh new session: %w", err)
	}
	return client, session, nil
}

// dialSSHContext dials an SSH connection while respecting context cancellation.
func dialSSHContext(ctx context.Context, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	d := net.Dialer{Timeout: cfg.Timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}

// shellJoin builds a POSIX shell-safe command string from cmd and args.
func shellJoin(cmd string, args []string) string {
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, shellQuote(cmd))
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

// shellQuote wraps s in single quotes, escaping contained single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
