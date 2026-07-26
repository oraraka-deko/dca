package utils

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-git/go-git/v6"
)

type CompactGitStatus struct {
	Branch    string   `json:"branch"`
	IsClean   bool     `json:"is_clean"`
	Modified  []string `json:"modified,omitempty"`
	Untracked []string `json:"untracked,omitempty"`
	Staged    []string `json:"staged,omitempty"`
}

// GetCompactStatus returns an ultra-compact representation of git status.
func (gm *GitManager) GetCompactStatus() (*CompactGitStatus, error) {
	branch, _ := gm.Branch()

	status, err := gm.Status()
	if err != nil {
		return nil, err
	}

	res := &CompactGitStatus{
		Branch:  branch,
		IsClean: status.IsClean(),
	}

	for path, state := range status {
		if state.Staging != git.Unmodified && state.Staging != git.Untracked {
			res.Staged = append(res.Staged, path)
		}
		if state.Worktree == git.Modified {
			res.Modified = append(res.Modified, path)
		} else if state.Worktree == git.Untracked {
			res.Untracked = append(res.Untracked, path)
		}
	}

	return res, nil
}

// CreateGitCheckpoint stages all changes and commits them as a checkpoint snapshot.
func (gm *GitManager) CreateGitCheckpoint(label string) (string, error) {
	if gm.worktree == nil {
		return "", fmt.Errorf("bare repository does not support checkpoints")
	}

	if label == "" {
		label = fmt.Sprintf("checkpoint-%d", time.Now().Unix())
	}

	_, err := gm.worktree.Add(".")
	if err != nil {
		return "", fmt.Errorf("checkpoint failed to stage: %w", err)
	}

	hash, err := gm.Commit(fmt.Sprintf("[CHECKPOINT] %s", label), "Agent", "agent@mcp.local")
	if err != nil {
		return "", fmt.Errorf("checkpoint failed: %w", err)
	}

	return hash.String(), nil
}

// GetCompactLog returns the last N commits as short-hash | first-line-of-message strings.
func (gm *GitManager) GetCompactLog(limit int) ([]string, error) {
	commits, err := gm.Log(limit)
	if err != nil {
		return nil, err
	}

	var logs []string
	for _, c := range commits {
		shortHash := c.Hash.String()[:7]
		firstLine := strings.Split(c.Message, "\n")[0]
		logs = append(logs, fmt.Sprintf("%s | %s", shortHash, firstLine))
	}

	return logs, nil
}
