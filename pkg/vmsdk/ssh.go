package vmsdk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHExec implements GuestExec over SSH. A new connection is made per Exec call
// (no pooling needed for Boxy's short-lived VM use case).
type SSHExec struct {
	Host      string
	Port      string // default "22"
	User      string
	PrivateKey []byte // PEM-encoded private key; used when non-empty
	Password  string // used when no PrivateKey is provided
}

// Exec opens an SSH session, runs the command, and returns stdout/stderr/exit code.
func (s *SSHExec) Exec(ctx context.Context, cmd string, args ...string) (*ExecResult, error) {
	port := s.Port
	if port == "" {
		port = "22"
	}
	addr := net.JoinHostPort(s.Host, port)

	authMethods := make([]ssh.AuthMethod, 0, 2)
	if len(s.PrivateKey) > 0 {
		signer, err := ssh.ParsePrivateKey(s.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
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
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	defer client.Close() //nolint:errcheck

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("ssh new session: %w", err)
	}
	defer session.Close() //nolint:errcheck

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	exitCode := 0
	if runErr := session.Run(shellJoin(cmd, args)); runErr != nil {
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
