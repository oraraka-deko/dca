package utils

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/spf13/afero"
)

// SandboxType represents the underlying filesystem implementation type.
type SandboxType string

const (
	SandboxTypeMem          SandboxType = "MemFs"
	SandboxTypeBasePath     SandboxType = "BasePathFs"
	SandboxTypeCopyOnWrite  SandboxType = "CopyOnWriteFs"
	SandboxTypeContainer    SandboxType = "ContainerFs"
)

// SandboxFileInfo represents metadata for a sandboxed file or directory.
type SandboxFileInfo struct {
	Name    string      `json:"name"`
	Path    string      `json:"path"`
	Size    int64       `json:"size"`
	IsDir   bool        `json:"is_dir"`
	ModTime time.Time   `json:"mod_time"`
	Mode    os.FileMode `json:"mode"`
}

// SearchResult holds match detail when searching within sandbox files.
type SearchResult struct {
	Path        string `json:"path"`
	LineNumber  int    `json:"line_number"`
	LineContent string `json:"line_content"`
}

// VFSSandbox represents an isolated virtual workspace environment.
type VFSSandbox struct {
	mu      sync.RWMutex
	ID      string      `json:"id"`
	Type    SandboxType `json:"type"`
	BaseDir string      `json:"base_dir,omitempty"`
	Fs      afero.Fs    `json:"-"`
}

// ReadFile reads the entire contents of a file inside the sandbox.
func (sb *VFSSandbox) ReadFile(path string) ([]byte, error) {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	return afero.ReadFile(sb.Fs, path)
}

// WriteFile writes data to a file inside the sandbox, creating parent directories if needed.
func (sb *VFSSandbox) WriteFile(path string, data []byte, perm os.FileMode) error {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	dir := filepath.Dir(path)
	if dir != "." && dir != "/" {
		if err := sb.Fs.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed creating parent directories: %w", err)
		}
	}
	return afero.WriteFile(sb.Fs, path, data, perm)
}

// RemoveFile removes a file or directory inside the sandbox.
func (sb *VFSSandbox) RemoveFile(path string) error {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.Fs.RemoveAll(path)
}

// Exists checks if a path exists within the sandbox.
func (sb *VFSSandbox) Exists(path string) (bool, error) {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	return afero.Exists(sb.Fs, path)
}

// MkdirAll creates a directory and all necessary parents inside the sandbox.
func (sb *VFSSandbox) MkdirAll(path string, perm os.FileMode) error {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.Fs.MkdirAll(path, perm)
}

// ListDir lists files and subdirectories within a directory inside the sandbox.
func (sb *VFSSandbox) ListDir(path string) ([]SandboxFileInfo, error) {
	sb.mu.RLock()
	defer sb.mu.RUnlock()

	entries, err := afero.ReadDir(sb.Fs, path)
	if err != nil {
		return nil, err
	}

	var list []SandboxFileInfo
	for _, entry := range entries {
		list = append(list, SandboxFileInfo{
			Name:    entry.Name(),
			Path:    filepath.Join(path, entry.Name()),
			Size:    entry.Size(),
			IsDir:   entry.IsDir(),
			ModTime: entry.ModTime(),
			Mode:    entry.Mode(),
		})
	}
	return list, nil
}

// Copy copies a file or directory from src to dst inside the sandbox.
func (sb *VFSSandbox) Copy(src string, dst string) error {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	info, err := sb.Fs.Stat(src)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return sb.copyDirLocked(src, dst)
	}
	return sb.copyFileLocked(src, dst)
}

func (sb *VFSSandbox) copyFileLocked(src string, dst string) error {
	srcFile, err := sb.Fs.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dir := filepath.Dir(dst)
	if dir != "." && dir != "/" {
		_ = sb.Fs.MkdirAll(dir, 0755)
	}

	dstFile, err := sb.Fs.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

func (sb *VFSSandbox) copyDirLocked(src string, dst string) error {
	return afero.Walk(sb.Fs, src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(dst, rel)
		if info.IsDir() {
			return sb.Fs.MkdirAll(targetPath, info.Mode())
		}
		return sb.copyFileLocked(path, targetPath)
	})
}

// Move renames/moves a file or directory inside the sandbox.
func (sb *VFSSandbox) Move(src string, dst string) error {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	dir := filepath.Dir(dst)
	if dir != "." && dir != "/" {
		_ = sb.Fs.MkdirAll(dir, 0755)
	}

	// Try rename first
	err := sb.Fs.Rename(src, dst)
	if err == nil {
		return nil
	}

	// Fallback to copy & remove
	if err := sb.copyDirLocked(src, dst); err != nil {
		return err
	}
	return sb.Fs.RemoveAll(src)
}

// BatchCreate creates multiple files inside the sandbox in a single call.
func (sb *VFSSandbox) BatchCreate(files map[string][]byte) error {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	for path, content := range files {
		dir := filepath.Dir(path)
		if dir != "." && dir != "/" {
			_ = sb.Fs.MkdirAll(dir, 0755)
		}
		if err := afero.WriteFile(sb.Fs, path, content, 0644); err != nil {
			return fmt.Errorf("batch create failed on %s: %w", path, err)
		}
	}
	return nil
}

// BatchRemove deletes multiple files or directories inside the sandbox.
func (sb *VFSSandbox) BatchRemove(paths []string) error {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	for _, path := range paths {
		if err := sb.Fs.RemoveAll(path); err != nil {
			return fmt.Errorf("batch remove failed on %s: %w", path, err)
		}
	}
	return nil
}

// SearchFiles performs line-by-line pattern matching across all files in the sandbox.
func (sb *VFSSandbox) SearchFiles(pattern string, caseInsensitive bool) ([]SearchResult, error) {
	sb.mu.RLock()
	defer sb.mu.RUnlock()

	var results []SearchResult
	pat := pattern
	if caseInsensitive {
		pat = strings.ToLower(pattern)
	}

	err := afero.Walk(sb.Fs, "", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, err := afero.ReadFile(sb.Fs, path)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(data), "\n")
		for idx, line := range lines {
			cmpLine := line
			if caseInsensitive {
				cmpLine = strings.ToLower(line)
			}
			if strings.Contains(cmpLine, pat) {
				results = append(results, SearchResult{
					Path:        path,
					LineNumber:  idx + 1,
					LineContent: strings.TrimRight(line, "\r"),
				})
			}
		}
		return nil
	})

	return results, err
}

// SearchAndReplace replaces instances of query with replacement across all files matching the pattern.
func (sb *VFSSandbox) SearchAndReplace(query string, replacement string, isRegex bool) (int, error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	var re *regexp.Regexp
	var err error
	if isRegex {
		re, err = regexp.Compile(query)
		if err != nil {
			return 0, fmt.Errorf("invalid regex query: %w", err)
		}
	}

	replacedCount := 0

	walkErr := afero.Walk(sb.Fs, "", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, err := afero.ReadFile(sb.Fs, path)
		if err != nil {
			return nil
		}

		content := string(data)
		var newContent string
		matches := 0

		if isRegex {
			locs := re.FindAllStringIndex(content, -1)
			matches = len(locs)
			if matches > 0 {
				newContent = re.ReplaceAllString(content, replacement)
			}
		} else {
			matches = strings.Count(content, query)
			if matches > 0 {
				newContent = strings.ReplaceAll(content, query, replacement)
			}
		}

		if matches > 0 {
			replacedCount += matches
			_ = afero.WriteFile(sb.Fs, path, []byte(newContent), info.Mode())
		}
		return nil
	})

	return replacedCount, walkErr
}

// VFSSandboxManager manages active isolated sandboxes.
type VFSSandboxManager struct {
	mu        sync.RWMutex
	sandboxes map[string]*VFSSandbox
}

// NewVFSSandboxManager creates a new VFSSandboxManager instance.
func NewVFSSandboxManager() *VFSSandboxManager {
	return &VFSSandboxManager{
		sandboxes: make(map[string]*VFSSandbox),
	}
}

// CreateMemSandbox initializes an in-memory virtual sandbox.
func (mgr *VFSSandboxManager) CreateMemSandbox(id string) (*VFSSandbox, error) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	if _, exists := mgr.sandboxes[id]; exists {
		return nil, fmt.Errorf("sandbox %s already exists", id)
	}

	sb := &VFSSandbox{
		ID:   id,
		Type: SandboxTypeMem,
		Fs:   afero.NewMemMapFs(),
	}
	mgr.sandboxes[id] = sb
	return sb, nil
}

// CreateBasePathSandbox initializes a disk-isolated sandbox restricted to baseDir.
func (mgr *VFSSandboxManager) CreateBasePathSandbox(id string, baseDir string) (*VFSSandbox, error) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	if _, exists := mgr.sandboxes[id]; exists {
		return nil, fmt.Errorf("sandbox %s already exists", id)
	}

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed creating base directory: %w", err)
	}

	baseFs := afero.NewOsFs()
	sb := &VFSSandbox{
		ID:      id,
		Type:    SandboxTypeBasePath,
		BaseDir: baseDir,
		Fs:      afero.NewBasePathFs(baseFs, baseDir),
	}
	mgr.sandboxes[id] = sb
	return sb, nil
}

// CreateCopyOnWriteSandbox creates a Copy-on-Write overlay sandbox over baseDir.
func (mgr *VFSSandboxManager) CreateCopyOnWriteSandbox(id string, baseDir string) (*VFSSandbox, error) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	if _, exists := mgr.sandboxes[id]; exists {
		return nil, fmt.Errorf("sandbox %s already exists", id)
	}

	baseFs := afero.NewOsFs()
	readOnlyBase := afero.NewReadOnlyFs(afero.NewBasePathFs(baseFs, baseDir))
	writeMem := afero.NewMemMapFs()

	cowFs := afero.NewCopyOnWriteFs(readOnlyBase, writeMem)

	sb := &VFSSandbox{
		ID:      id,
		Type:    SandboxTypeCopyOnWrite,
		BaseDir: baseDir,
		Fs:      cowFs,
	}
	mgr.sandboxes[id] = sb
	return sb, nil
}

// GetSandbox returns an active sandbox by ID.
func (mgr *VFSSandboxManager) GetSandbox(id string) (*VFSSandbox, bool) {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	sb, exists := mgr.sandboxes[id]
	return sb, exists
}

// RemoveSandbox deletes a sandbox from the manager.
func (mgr *VFSSandboxManager) RemoveSandbox(id string) error {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	sb, exists := mgr.sandboxes[id]
	if !exists {
		return fmt.Errorf("sandbox %s not found", id)
	}

	delete(mgr.sandboxes, id)
	if sb.Type == SandboxTypeBasePath && sb.BaseDir != "" {
		_ = os.RemoveAll(sb.BaseDir)
	}
	return nil
}

// ListSandboxes returns all active sandboxes.
func (mgr *VFSSandboxManager) ListSandboxes() []*VFSSandbox {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	var list []*VFSSandbox
	for _, sb := range mgr.sandboxes {
		list = append(list, sb)
	}
	return list
}

// RemoveAll cleans up and removes all active sandboxes.
func (mgr *VFSSandboxManager) RemoveAll() error {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	for id, sb := range mgr.sandboxes {
		if sb.Type == SandboxTypeBasePath && sb.BaseDir != "" {
			_ = os.RemoveAll(sb.BaseDir)
		}
		delete(mgr.sandboxes, id)
	}
	return nil
}
