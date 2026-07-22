package utils

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SortMode defines the sorting options for the file manager.
type SortMode int

const (
	SortByName SortMode = iota
	SortBySize
	SortByDate
)

// FileEntryInfo represents a file/directory entry with rich properties.
type FileEntryInfo struct {
	Path                 string    `json:"path"`
	Name                 string    `json:"name"`
	IsDir                bool      `json:"is_dir"`
	Size                 int64     `json:"size"`
	Extension            string    `json:"extension"`
	IsSymlink            bool      `json:"is_symlink"`
	SymlinkTarget        string    `json:"symlink_target"`
	SymlinkTargetExists  bool      `json:"symlink_target_exists"`
	IsSymlinkDirectory   bool      `json:"is_symlink_directory"`
	IsEmptyDirectory     bool      `json:"is_empty_directory"`
	ModifiedTime         time.Time `json:"modified_time"`
	ModifiedTimeStr      string    `json:"modified_time_str"`
}

// ClipboardState represents the files copied or cut.
type ClipboardState struct {
	Paths []string
	IsCut bool
}

// FileManager manages the headless file operations and state.
type FileManager struct {
	CurrentPath    string
	SelectedIndex  int
	BackHistory    []string
	ForwardHistory []string
	MultiSelection map[string]bool
	Clipboard      ClipboardState
	PinnedPaths    []string
	SortMode       SortMode
	ShowHidden     bool
}

// NewFileManager creates and initializes a new headless FileManager.
func NewFileManager(startingPath string) (*FileManager, error) {
	if startingPath == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get working directory: %w", err)
		}
		startingPath = wd
	}

	absPath, err := filepath.Abs(startingPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	return &FileManager{
		CurrentPath:    absPath,
		MultiSelection: make(map[string]bool),
		SortMode:       SortByName,
		ShowHidden:     false,
	}, nil
}

// List retrieves and sorts the files/directories in CurrentPath.
func (fm *FileManager) List() ([]FileEntryInfo, error) {
	entries, err := os.ReadDir(fm.CurrentPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	var fileInfos []FileEntryInfo
	for _, entry := range entries {
		name := entry.Name()
		if !fm.ShowHidden && strings.HasPrefix(name, ".") {
			continue
		}

		fullPath := filepath.Join(fm.CurrentPath, name)
		info, err := GetFileEntryInfo(fullPath)
		if err != nil {
			// Skip or log error, keep listing
			continue
		}
		fileInfos = append(fileInfos, info)
	}

	// Sort: Directories always come first, then sorted by SortMode
	sort.Slice(fileInfos, func(i, j int) bool {
		if fileInfos[i].IsDir && !fileInfos[j].IsDir {
			return true
		}
		if !fileInfos[i].IsDir && fileInfos[j].IsDir {
			return false
		}

		switch fm.SortMode {
		case SortBySize:
			return fileInfos[i].Size > fileInfos[j].Size
		case SortByDate:
			return fileInfos[i].ModifiedTime.After(fileInfos[j].ModifiedTime)
		case SortByName:
			fallthrough
		default:
			return strings.ToLower(fileInfos[i].Name) < strings.ToLower(fileInfos[j].Name)
		}
	})

	return fileInfos, nil
}

// GetFileEntryInfo queries the OS to populate a FileEntryInfo struct.
func GetFileEntryInfo(path string) (FileEntryInfo, error) {
	lInfo, err := os.Lstat(path)
	if err != nil {
		return FileEntryInfo{}, err
	}

	info := FileEntryInfo{
		Path:            path,
		Name:            lInfo.Name(),
		IsDir:           lInfo.IsDir(),
		Size:            lInfo.Size(),
		Extension:       strings.ToLower(filepath.Ext(path)),
		ModifiedTime:    lInfo.ModTime(),
		ModifiedTimeStr: lInfo.ModTime().Format("2006-01-02 15:04"),
	}

	// Symlink checks
	if lInfo.Mode()&os.ModeSymlink != 0 {
		info.IsSymlink = true
		target, err := os.Readlink(path)
		if err == nil {
			info.SymlinkTarget = target
			// Resolve absolute path of symlink
			resolved := target
			if !filepath.IsAbs(target) {
				resolved = filepath.Join(filepath.Dir(path), target)
			}
			tInfo, err := os.Stat(resolved)
			if err == nil {
				info.SymlinkTargetExists = true
				if tInfo.IsDir() {
					info.IsSymlinkDirectory = true
					info.IsDir = true
				}
			}
		}
	}

	// Directory specific properties
	if info.IsDir {
		info.Size = 0 // Folder display size is generally 0 or uncalculated natively
		subEntries, err := os.ReadDir(path)
		if err == nil && len(subEntries) == 0 {
			info.IsEmptyDirectory = true
		}
	}

	return info, nil
}

// GoTo changes the current path, updating history.
func (fm *FileManager) GoTo(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	fi, err := os.Stat(absPath)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("path is not a directory")
	}

	if fm.CurrentPath != absPath {
		fm.BackHistory = append(fm.BackHistory, fm.CurrentPath)
		fm.CurrentPath = absPath
		fm.ForwardHistory = nil // Clear forward history on new navigation
		fm.SelectedIndex = 0
	}
	return nil
}

// GoUp navigates to the parent directory.
func (fm *FileManager) GoUp() error {
	parent := filepath.Dir(fm.CurrentPath)
	if parent == fm.CurrentPath {
		return fmt.Errorf("already at root")
	}
	return fm.GoTo(parent)
}

// GoBack goes back in navigation history.
func (fm *FileManager) GoBack() error {
	if len(fm.BackHistory) == 0 {
		return fmt.Errorf("no back history")
	}

	prev := fm.BackHistory[len(fm.BackHistory)-1]
	fm.BackHistory = fm.BackHistory[:len(fm.BackHistory)-1]

	fm.ForwardHistory = append(fm.ForwardHistory, fm.CurrentPath)
	fm.CurrentPath = prev
	fm.SelectedIndex = 0
	return nil
}

// GoForward goes forward in navigation history.
func (fm *FileManager) GoForward() error {
	if len(fm.ForwardHistory) == 0 {
		return fmt.Errorf("no forward history")
	}

	next := fm.ForwardHistory[len(fm.ForwardHistory)-1]
	fm.ForwardHistory = fm.ForwardHistory[:len(fm.ForwardHistory)-1]

	fm.BackHistory = append(fm.BackHistory, fm.CurrentPath)
	fm.CurrentPath = next
	fm.SelectedIndex = 0
	return nil
}

// ToggleSelect adds or removes a path to the multi-selection set.
func (fm *FileManager) ToggleSelect(path string) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return
	}
	if fm.MultiSelection[absPath] {
		delete(fm.MultiSelection, absPath)
	} else {
		fm.MultiSelection[absPath] = true
	}
}

// ClearSelection clears current file selections.
func (fm *FileManager) ClearSelection() {
	fm.MultiSelection = make(map[string]bool)
}

// Copy copies selected paths (or specified path) to clipboard state.
func (fm *FileManager) Copy(paths []string) {
	fm.Clipboard = ClipboardState{
		Paths: paths,
		IsCut: false,
	}
}

// Cut cuts selected paths (or specified path) to clipboard state.
func (fm *FileManager) Cut(paths []string) {
	fm.Clipboard = ClipboardState{
		Paths: paths,
		IsCut: true,
	}
}

// Paste executes the copying or moving of files from clipboard to CurrentPath.
func (fm *FileManager) Paste() error {
	if len(fm.Clipboard.Paths) == 0 {
		return fmt.Errorf("clipboard is empty")
	}

	for _, src := range fm.Clipboard.Paths {
		baseName := filepath.Base(src)
		dst := filepath.Join(fm.CurrentPath, baseName)

		if src == dst {
			continue // Avoid pasting to same location
		}

		if fm.Clipboard.IsCut {
			if err := os.Rename(src, dst); err != nil {
				return fmt.Errorf("failed to move %q to %q: %w", src, dst, err)
			}
		} else {
			if err := copyPath(src, dst); err != nil {
				return fmt.Errorf("failed to copy %q to %q: %w", src, dst, err)
			}
		}
	}

	if fm.Clipboard.IsCut {
		fm.Clipboard = ClipboardState{} // Clear clipboard after cut & paste
	}
	return nil
}

// Delete deletes the specified paths.
func (fm *FileManager) Delete(paths []string) error {
	for _, p := range paths {
		if err := os.RemoveAll(p); err != nil {
			return fmt.Errorf("failed to delete %q: %w", p, err)
		}
	}
	return nil
}

// Rename renames a file/directory.
func (fm *FileManager) Rename(oldPath, newName string) error {
	dir := filepath.Dir(oldPath)
	newPath := filepath.Join(dir, newName)
	return os.Rename(oldPath, newPath)
}

// Mkdir creates a new directory in CurrentPath.
func (fm *FileManager) Mkdir(name string) error {
	path := filepath.Join(fm.CurrentPath, name)
	return os.MkdirAll(path, 0755)
}

// CreateFile creates a new empty file in CurrentPath.
func (fm *FileManager) CreateFile(name string) error {
	path := filepath.Join(fm.CurrentPath, name)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return f.Close()
}

// AddBookmark adds a path to pinned paths.
func (fm *FileManager) AddBookmark(path string) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return
	}
	for _, p := range fm.PinnedPaths {
		if p == absPath {
			return
		}
	}
	fm.PinnedPaths = append(fm.PinnedPaths, absPath)
}

// RemoveBookmark removes a path from pinned paths.
func (fm *FileManager) RemoveBookmark(path string) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return
	}
	var newBookmarks []string
	for _, p := range fm.PinnedPaths {
		if p != absPath {
			newBookmarks = append(newBookmarks, p)
		}
	}
	fm.PinnedPaths = newBookmarks
}

// GetBookmarks retrieves all pinned paths.
func (fm *FileManager) GetBookmarks() []string {
	return fm.PinnedPaths
}

// Search searches for files in CurrentPath recursively matching query.
func (fm *FileManager) Search(query string) ([]FileEntryInfo, error) {
	var results []FileEntryInfo
	queryLower := strings.ToLower(query)

	err := filepath.WalkDir(fm.CurrentPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip items with permission errors
		}
		if !fm.ShowHidden && strings.HasPrefix(d.Name(), ".") && path != fm.CurrentPath {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if strings.Contains(strings.ToLower(d.Name()), queryLower) {
			info, err := GetFileEntryInfo(path)
			if err == nil {
				results = append(results, info)
			}
		}
		return nil
	})

	return results, err
}

// GetPreview determines the file content type and returns preview details.
// It returns (previewType, previewText, error)
func (fm *FileManager) GetPreview(path string, maxLines int) (string, string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "none", "", err
	}

	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return "directory", "Unable to read directory contents", nil
		}
		var lines []string
		lines = append(lines, fmt.Sprintf("Directory: %s (%d items)", info.Name(), len(entries)))
		lines = append(lines, "----------------------------------------")
		for i, entry := range entries {
			if i >= maxLines {
				lines = append(lines, fmt.Sprintf("... and %d more items", len(entries)-maxLines))
				break
			}
			typeName := "File"
			if entry.IsDir() {
				typeName = "Folder"
			}
			lines = append(lines, fmt.Sprintf(" [%s] %s", typeName, entry.Name()))
		}
		return "directory", strings.Join(lines, "\n"), nil
	}

	// Read first 1024 bytes to check if it's text or binary
	file, err := os.Open(path)
	if err != nil {
		return "none", "", err
	}
	defer file.Close()

	buf := make([]byte, 1024)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return "none", "", err
	}

	if isBinaryFile(buf[:n]) {
		return "binary", fmt.Sprintf("Binary File: %s (%d bytes)", info.Name(), info.Size()), nil
	}

	// Read line by line for text file
	_, _ = file.Seek(0, 0)
	scanner := bufio.NewScanner(file)
	var textLines []string
	for i := 0; i < maxLines && scanner.Scan(); i++ {
		textLines = append(textLines, scanner.Text())
	}

	return "text", strings.Join(textLines, "\n"), scanner.Err()
}

func isBinaryFile(data []byte) bool {
	for _, b := range data {
		if b == 0x00 {
			return true
		}
	}
	return false
}

func copyPath(src, dst string) error {
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return err
	}

	if srcInfo.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	}

	if srcInfo.IsDir() {
		return copyDir(src, dst)
	}
	return copyFile(src, dst)
}

func copyDir(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if err := copyPath(srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
