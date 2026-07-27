//go:build !windows

package main

import "dca/utils"

func runAsService(cfg utils.ServerConfig) (bool, error) {
	// Linux systemd services are run as standard foreground processes,
	// so no special service manager communication wrapper is required in the binary.
	return false, nil
}
