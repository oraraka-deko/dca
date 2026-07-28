package utils

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHAuthOptions specifies authentication credentials for an SSH connection.
type SSHAuthOptions struct {
	Password   string `json:"password,omitempty"`
	KeyPath    string `json:"key_path,omitempty"`
	KeyContent string `json:"key_content,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
}

// SSHClientManager manages native SSH connections and remote command execution.
type SSHClientManager struct{}

// NewSSHClientManager creates a new SSH client manager.
func NewSSHClientManager() *SSHClientManager {
	return &SSHClientManager{}
}

// BuildClientConfig constructs a native x/crypto/ssh ClientConfig from auth options.
func (m *SSHClientManager) BuildClientConfig(user string, auth SSHAuthOptions, timeout time.Duration) (*ssh.ClientConfig, error) {
	var authMethods []ssh.AuthMethod

	// 1. Private key string authentication
	if strings.TrimSpace(auth.KeyContent) != "" {
		var signer ssh.Signer
		var err error
		if auth.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(auth.KeyContent), []byte(auth.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(auth.KeyContent))
		}
		if err != nil {
			return nil, fmt.Errorf("failed to parse private key content: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	// 2. Private key file authentication
	if strings.TrimSpace(auth.KeyPath) != "" {
		keyData, err := os.ReadFile(auth.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read private key file '%s': %w", auth.KeyPath, err)
		}
		var signer ssh.Signer
		if auth.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(keyData, []byte(auth.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(keyData)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to parse private key file '%s': %w", auth.KeyPath, err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	// 3. Password authentication
	if auth.Password != "" {
		authMethods = append(authMethods, ssh.Password(auth.Password))
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no valid SSH authentication method provided (password, key_path, or key_content required)")
	}

	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Prevent host key verification hangs
		Timeout:         timeout,
	}

	return config, nil
}

// Connect establishes a native SSH client connection to the target host and port.
func (m *SSHClientManager) Connect(ctx context.Context, host string, port int, user string, auth SSHAuthOptions, timeout time.Duration) (*ssh.Client, error) {
	if port <= 0 {
		port = 22
	}
	target := fmt.Sprintf("%s:%d", host, port)

	cfg, err := m.BuildClientConfig(user, auth, timeout)
	if err != nil {
		return nil, err
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SSH host %s: %w", target, err)
	}

	c, chans, reqs, err := ssh.NewClientConn(conn, target, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("SSH handshake failed for %s: %w", target, err)
	}

	return ssh.NewClient(c, chans, reqs), nil
}

// ExecuteRemoteCommand connects to an SSH host and runs a single command, returning the execution result.
func (m *SSHClientManager) ExecuteRemoteCommand(ctx context.Context, host string, port int, user string, auth SSHAuthOptions, command string, timeout time.Duration) (*ExecutionResult, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	startTime := time.Now()

	client, err := m.Connect(ctx, host, port, user, auth, timeout)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	var stdoutBuf, stderrBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	session.Stderr = &stderrBuf

	errChan := make(chan error, 1)
	go func() {
		errChan <- session.Run(command)
	}()

	var runErr error
	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		duration := time.Since(startTime).Milliseconds()
		return &ExecutionResult{
			Output:   fmt.Sprintf("%s\n[SSH Execution Timeout Reached]", stdoutBuf.String()),
			ExitCode: -124,
			Duration: duration,
		}, nil
	case runErr = <-errChan:
	}

	duration := time.Since(startTime).Milliseconds()
	combinedOutput := stdoutBuf.String()
	if stderrBuf.Len() > 0 {
		if len(combinedOutput) > 0 {
			combinedOutput += "\n--- STDERR ---\n" + stderrBuf.String()
		} else {
			combinedOutput = stderrBuf.String()
		}
	}

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*ssh.ExitError); ok {
			exitCode = exitErr.ExitStatus()
		} else {
			exitCode = -1
		}
	}

	return &ExecutionResult{
		Output:   strings.TrimSpace(combinedOutput),
		ExitCode: exitCode,
		Duration: duration,
	}, nil
}

// TestConnection tests SSH reachability and credentials for a target host.
func (m *SSHClientManager) TestConnection(ctx context.Context, host string, port int, user string, auth SSHAuthOptions, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client, err := m.Connect(ctx, host, port, user, auth, timeout)
	if err != nil {
		return err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("SSH connection established but failed to open session: %w", err)
	}
	defer session.Close()

	return nil
}
