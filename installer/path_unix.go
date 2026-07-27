//go:build !windows

package installer

// AddFolderToUserPath is a stub on Unix.
func AddFolderToUserPath(folderPath string) error {
	return nil
}
