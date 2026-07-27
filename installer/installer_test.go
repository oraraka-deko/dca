package installer

import (
	"strings"
	"testing"
)

func TestInstaller_SystemdUnitFile(t *testing.T) {
	unit := GenerateSystemdUnitFile("/usr/local/bin/mymcp", "/etc/mymcp/config.json")
	if !strings.Contains(unit, "ExecStart=/usr/local/bin/mymcp -config /etc/mymcp/config.json") {
		t.Fatalf("unexpected unit file format:\n%s", unit)
	}
	if !strings.Contains(unit, "Restart=always") {
		t.Fatalf("missing restart directive in unit file")
	}
}
