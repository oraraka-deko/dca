package utils

import (
	"strings"
	"testing"
)

func TestParseSyslog_RFC5424(t *testing.T) {
	input := `<165>1 2018-10-11T22:14:15.003Z mymach.it example 1234 ID45 [ex@32473 iut="3"] An application event log entry...`
	reader := strings.NewReader(input)

	entries, err := ParseSyslog(reader)
	if err != nil {
		t.Fatalf("failed to parse syslog: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry.Priority != 165 {
		t.Errorf("expected priority 165, got %d", entry.Priority)
	}
	if entry.Facility != 20 { // 165 / 8 = 20
		t.Errorf("expected facility 20, got %d", entry.Facility)
	}
	if entry.Severity != SeverityNotice { // 165 % 8 = 5 (notice)
		t.Errorf("expected severity notice, got %s", entry.Severity)
	}
	if entry.Hostname != "mymach.it" {
		t.Errorf("expected hostname mymach.it, got %s", entry.Hostname)
	}
	if entry.AppName != "example" {
		t.Errorf("expected AppName example, got %s", entry.AppName)
	}
	if entry.ProcID != "1234" {
		t.Errorf("expected ProcID 1234, got %s", entry.ProcID)
	}
	if entry.MsgID != "ID45" {
		t.Errorf("expected MsgID ID45, got %s", entry.MsgID)
	}
	if entry.Message != "An application event log entry..." {
		t.Errorf("expected message, got %s", entry.Message)
	}
}

func TestParseSyslog_RFC3164(t *testing.T) {
	input := `<34>Oct 11 22:14:15 mymachine su: 'su root' failed`
	reader := strings.NewReader(input)

	entries, err := ParseSyslog(reader)
	if err != nil {
		t.Fatalf("failed to parse syslog: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry.Priority != 34 {
		t.Errorf("expected priority 34, got %d", entry.Priority)
	}
	if entry.Facility != 4 { // 34 / 8 = 4
		t.Errorf("expected facility 4, got %d", entry.Facility)
	}
	if entry.Severity != SeverityCritical { // 34 % 8 = 2 (critical)
		t.Errorf("expected severity critical, got %s", entry.Severity)
	}
	if entry.Hostname != "mymachine" {
		t.Errorf("expected hostname mymachine, got %s", entry.Hostname)
	}
	if entry.AppName != "su" {
		t.Errorf("expected app name su, got %s", entry.AppName)
	}
	if entry.Message != "'su root' failed" {
		t.Errorf("expected message, got %s", entry.Message)
	}
}

func TestParseSyslog_Fallback(t *testing.T) {
	input := `this is some completely unparsed text line`
	reader := strings.NewReader(input)

	entries, err := ParseSyslog(reader)
	if err != nil {
		t.Fatalf("failed to parse syslog: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry.Severity != SeverityUnknown {
		t.Errorf("expected severity unknown, got %s", entry.Severity)
	}
	if entry.Message != input {
		t.Errorf("expected raw input as message, got %s", entry.Message)
	}
}

func TestFilterEntries(t *testing.T) {
	entries := []SyslogEntry{
		{
			Severity: SeverityInfo,
			AppName:  "web",
			Message:  "User login success",
			Hostname: "host-a",
		},
		{
			Severity: SeverityWarning,
			AppName:  "database",
			Message:  "Connection pool high load",
			Hostname: "host-b",
		},
		{
			Severity: SeverityCritical,
			AppName:  "auth",
			Message:  "Brute force attempt detected",
			Hostname: "host-a",
		},
		{
			Severity: SeverityInfo,
			AppName:  "web",
			Message:  "Page view home",
			Hostname: "host-a",
		},
	}

	// 1. Filter by severity only
	filter1 := LogFilter{
		Severities: []LogSeverity{SeverityWarning, SeverityCritical},
	}
	res1 := FilterEntries(entries, filter1)
	if len(res1) != 2 {
		t.Errorf("expected 2 filtered entries, got %d", len(res1))
	}

	// 2. Filter by query search (case insensitive)
	filter2 := LogFilter{
		Query: "Login",
	}
	res2 := FilterEntries(entries, filter2)
	if len(res2) != 1 {
		t.Errorf("expected 1 entry for query 'Login', got %d", len(res2))
	}
	if res2[0].AppName != "web" {
		t.Errorf("expected web app entry, got %s", res2[0].AppName)
	}

	// 3. Filter by query and severity
	filter3 := LogFilter{
		Severities: []LogSeverity{SeverityInfo},
		Query:      "host-a",
	}
	res3 := FilterEntries(entries, filter3)
	if len(res3) != 2 {
		t.Errorf("expected 2 entries for severity Info and host-a query, got %d", len(res3))
	}

	// 4. Limit results
	filter4 := LogFilter{
		Limit: 2,
	}
	res4 := FilterEntries(entries, filter4)
	if len(res4) != 2 {
		t.Errorf("expected 2 entries under limit of 2, got %d", len(res4))
	}
}
