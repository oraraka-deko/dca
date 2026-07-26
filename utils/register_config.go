// utils/config.go
package utils

type AppConfig struct {
	AppName     string
	AppKey      string // Unique key (e.g., "potplayer") - Keep lowercase for Linux
	ExePath     string
	IconPath    string   // Path to .png or .ico
	Extensions  []string // e.g. []string{".mp4", ".mkv"}
	Description string
}