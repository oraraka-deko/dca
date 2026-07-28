package utils

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const BoundaryToken = "__CMD_DONE_MARKER__"

// ExecutionResult holds the stdout/stderr output and integer exit code of a command execution.
type ExecutionResult struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
	Duration int64  `json:"duration_ms"`
}

// CommandOptions configures a command execution inside a persistent shell session.
type CommandOptions struct {
	Command string            `json:"command"`
	Prompts map[string]string `json:"prompts,omitempty"`
	Timeout time.Duration     `json:"timeout,omitempty"`
}

// PersistentSession represents an active, stateful shell process (bash, sh, pwsh, cmd.exe).
type PersistentSession struct {
	ID         string
	Shell      string
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	cancel     context.CancelFunc
	mu         sync.Mutex
	outputChan chan []byte
	closed     bool
}

// StartPersistentSession launches a stateful shell process that stays alive for subsequent commands.
func StartPersistentSession(id string, shell string) (*PersistentSession, error) {
	if shell == "" {
		if runtime.GOOS == "windows" {
			shell = "cmd.exe"
		} else {
			shell = "/bin/bash"
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, shell)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to open stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to open stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout // Merge stderr into stdout stream

	session := &PersistentSession{
		ID:         id,
		Shell:      shell,
		cmd:        cmd,
		stdin:      stdin,
		cancel:     cancel,
		outputChan: make(chan []byte, 500),
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed starting shell process '%s': %w", shell, err)
	}

	go session.readLoop(stdout)

	go func() {
		_ = cmd.Wait()
		session.mu.Lock()
		session.closed = true
		session.mu.Unlock()
		close(session.outputChan)
	}()

	return session, nil
}

func (s *PersistentSession) readLoop(stdout io.Reader) {
	buf := make([]byte, 2048)
	for {
		n, err := stdout.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.outputChan <- chunk
		}
		if err != nil {
			break
		}
	}
}

// ExecuteAsync sends a command to the persistent shell and returns a channel yielding the result.
func (s *PersistentSession) ExecuteAsync(opts CommandOptions) <-chan ExecutionResult {
	resultChan := make(chan ExecutionResult, 1)

	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		if s.closed {
			resultChan <- ExecutionResult{Output: "Session is closed", ExitCode: -1}
			return
		}

		startTime := time.Now()
		tokenPrefix := BoundaryToken + "="

		// Format boundary token command according to target shell syntax
		var chainedCommand string
		shLower := strings.ToLower(s.Shell)
		if strings.Contains(shLower, "cmd") {
			chainedCommand = fmt.Sprintf("%s\r\necho %s=%%errorlevel%%\r\n", opts.Command, BoundaryToken)
		} else if strings.Contains(shLower, "pwsh") || strings.Contains(shLower, "powershell") {
			chainedCommand = fmt.Sprintf("%s\nWrite-Output \"%s=$LASTEXITCODE\"\n", opts.Command, BoundaryToken)
		} else {
			chainedCommand = fmt.Sprintf("%s\necho \"%s=$?\"\n", opts.Command, BoundaryToken)
		}

		if _, err := s.stdin.Write([]byte(chainedCommand)); err != nil {
			resultChan <- ExecutionResult{Output: fmt.Sprintf("Failed writing to shell stdin: %v", err), ExitCode: -1}
			return
		}

		var fullOutput strings.Builder
		var currentBuffer strings.Builder
		finalExitCode := 0

		timeout := opts.Timeout
		if timeout <= 0 {
			timeout = 60 * time.Second
		}
		timer := time.NewTimer(timeout)
		defer timer.Stop()

	ReadLoop:
		for {
			select {
			case chunk, ok := <-s.outputChan:
				if !ok {
					break ReadLoop
				}
				text := string(chunk)
				fullOutput.WriteString(text)
				currentBuffer.WriteString(text)

				// Check for boundary token + exit code marker
				if markerIdx := strings.Index(fullOutput.String(), tokenPrefix); markerIdx != -1 {
					remainder := fullOutput.String()[markerIdx+len(tokenPrefix):]
					newlineIdx := strings.IndexAny(remainder, "\r\n")
					var codeStr string
					if newlineIdx != -1 {
						codeStr = remainder[:newlineIdx]
					} else {
						codeStr = remainder
					}
					parsedCode, err := strconv.Atoi(strings.TrimSpace(codeStr))
					if err == nil {
						finalExitCode = parsedCode
					}
					break ReadLoop
				}

				// Check for expected prompt auto-replies
				matchStr := strings.TrimSpace(currentBuffer.String())
				for prompt, response := range opts.Prompts {
					if strings.HasSuffix(matchStr, prompt) {
						if !strings.HasSuffix(response, "\n") && !strings.HasSuffix(response, "\r\n") {
							response += "\n"
						}
						_, _ = s.stdin.Write([]byte(response))
						currentBuffer.Reset()
						break
					}
				}

			case <-timer.C:
				fullOutput.WriteString("\n[Execution Timeout Reached]\n")
				finalExitCode = -124
				break ReadLoop
			}
		}

		rawOutput := fullOutput.String()
		if markerIdx := strings.Index(rawOutput, tokenPrefix); markerIdx != -1 {
			lastNewline := strings.LastIndex(rawOutput[:markerIdx], "\n")
			if lastNewline != -1 {
				rawOutput = rawOutput[:lastNewline]
			} else {
				rawOutput = rawOutput[:markerIdx]
			}
		}

		duration := time.Since(startTime).Milliseconds()
		resultChan <- ExecutionResult{
			Output:   strings.TrimSpace(rawOutput),
			ExitCode: finalExitCode,
			Duration: duration,
		}
	}()

	return resultChan
}

// Close gracefully closes standard input and terminates the shell session.
func (s *PersistentSession) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		if s.stdin != nil {
			_ = s.stdin.Close()
		}
		if s.cancel != nil {
			s.cancel()
		}
	}
}

// RunElevated executes a command with administrative/root privileges using standard OS dialogs.
func RunElevated(command string, args ...string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		var quotedArgs []string
		for _, arg := range args {
			quotedArgs = append(quotedArgs, fmt.Sprintf("'%s'", arg))
		}
		psArgs := []string{
			"-NoProfile",
			"-NonInteractive",
			"-Command",
			fmt.Sprintf("Start-Process '%s' -ArgumentList %s -Verb RunAs -Wait", command, strings.Join(quotedArgs, ", ")),
		}
		cmd = exec.Command("powershell", psArgs...)

	case "darwin":
		fullCmd := command
		if len(args) > 0 {
			fullCmd += " " + strings.Join(args, " ")
		}
		script := fmt.Sprintf("do shell script %q with administrator privileges", fullCmd)
		cmd = exec.Command("osascript", "-e", script)

	case "linux":
		cmdArgs := append([]string{command}, args...)
		if _, err := exec.LookPath("pkexec"); err == nil {
			cmd = exec.Command("pkexec", cmdArgs...)
		} else {
			cmd = exec.Command("sudo", cmdArgs...)
		}

	default:
		return fmt.Errorf("unsupported OS for elevated privilege execution: %s", runtime.GOOS)
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
