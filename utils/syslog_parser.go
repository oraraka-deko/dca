package utils

import (
	"bufio"
	"io"
	"strings"
	"time"

	"github.com/leodido/go-syslog/v4/rfc3164"
	"github.com/leodido/go-syslog/v4/rfc5424"
)

// LogSeverity defines the syslog severity levels.
type LogSeverity string

const (
	SeverityEmergency LogSeverity = "emergency" // 0
	SeverityAlert     LogSeverity = "alert"     // 1
	SeverityCritical  LogSeverity = "critical"  // 2
	SeverityError     LogSeverity = "error"     // 3
	SeverityWarning   LogSeverity = "warning"   // 4
	SeverityNotice    LogSeverity = "notice"    // 5
	SeverityInfo      LogSeverity = "info"      // 6
	SeverityDebug     LogSeverity = "debug"     // 7
	SeverityUnknown   LogSeverity = "unknown"
)

// SeverityFromPriority extracts severity from priority value.
func SeverityFromPriority(prio int) LogSeverity {
	switch prio % 8 {
	case 0:
		return SeverityEmergency
	case 1:
		return SeverityAlert
	case 2:
		return SeverityCritical
	case 3:
		return SeverityError
	case 4:
		return SeverityWarning
	case 5:
		return SeverityNotice
	case 6:
		return SeverityInfo
	case 7:
		return SeverityDebug
	default:
		return SeverityUnknown
	}
}

// SyslogEntry represents a parsed syslog entry.
type SyslogEntry struct {
	Timestamp time.Time   `json:"timestamp"`
	Priority  int         `json:"priority"`
	Facility  int         `json:"facility"`
	Severity  LogSeverity `json:"severity"`
	Hostname  string      `json:"hostname"`
	AppName   string      `json:"app_name"`
	ProcID    string      `json:"proc_id"`
	MsgID     string      `json:"msg_id"`
	Message   string      `json:"message"`
	Raw       string      `json:"raw"`
}

// LogFilter defines the criteria for filtering system logs.
type LogFilter struct {
	Severities []LogSeverity
	Query      string // Query search term inside message, appname, or hostname
	Limit      int    // Limit number of entries returned (0 or negative means unlimited)
}

// ParseSyslog reads syslog data from an io.Reader and parses it line by line.
func ParseSyslog(r io.Reader) ([]SyslogEntry, error) {
	var entries []SyslogEntry
	scanner := bufio.NewScanner(r)

	parser5424 := rfc5424.NewParser()
	parser3164 := rfc3164.NewParser()

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		entry := SyslogEntry{
			Raw:      line,
			Severity: SeverityUnknown,
		}

		lineBytes := []byte(line)

		// Attempt RFC 5424 parsing
		if msg, err := parser5424.Parse(lineBytes); err == nil {
			if m, ok := msg.(*rfc5424.SyslogMessage); ok {
				if m.Timestamp != nil {
					entry.Timestamp = *m.Timestamp
				}
				if m.Priority != nil {
					entry.Priority = int(*m.Priority)
					entry.Facility = int(*m.Priority / 8)
					entry.Severity = SeverityFromPriority(entry.Priority)
				}
				if m.Hostname != nil {
					entry.Hostname = *m.Hostname
				}
				if m.Appname != nil {
					entry.AppName = *m.Appname
				}
				if m.ProcID != nil {
					entry.ProcID = *m.ProcID
				}
				if m.MsgID != nil {
					entry.MsgID = *m.MsgID
				}
				if m.Message != nil {
					entry.Message = *m.Message
				}
				entries = append(entries, entry)
				continue
			}
		}

		// Attempt RFC 3164 parsing as fallback
		if msg, err := parser3164.Parse(lineBytes); err == nil {
			if m, ok := msg.(*rfc3164.SyslogMessage); ok {
				if m.Timestamp != nil {
					entry.Timestamp = *m.Timestamp
				}
				if m.Priority != nil {
					entry.Priority = int(*m.Priority)
					entry.Facility = int(*m.Priority / 8)
					entry.Severity = SeverityFromPriority(entry.Priority)
				}
				if m.Hostname != nil {
					entry.Hostname = *m.Hostname
				}
				if m.Appname != nil {
					entry.AppName = *m.Appname
				}
				if m.Message != nil {
					entry.Message = *m.Message
				}
				entries = append(entries, entry)
				continue
			}
		}

		// If both parsing attempts fail, treat it as unparsed/raw entry
		entry.Message = line
		entry.Timestamp = time.Now()
		entries = append(entries, entry)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return entries, nil
}

// FilterEntries filters a slice of SyslogEntry using the given LogFilter.
func FilterEntries(entries []SyslogEntry, filter LogFilter) []SyslogEntry {
	var result []SyslogEntry

	severityMap := make(map[LogSeverity]bool)
	for _, sev := range filter.Severities {
		severityMap[sev] = true
	}

	queryLower := strings.ToLower(filter.Query)

	for _, entry := range entries {
		// Filter by severity if filter.Severities is not empty
		if len(filter.Severities) > 0 {
			if !severityMap[entry.Severity] {
				continue
			}
		}

		// Filter by query if query is specified
		if queryLower != "" {
			match := strings.Contains(strings.ToLower(entry.Message), queryLower) ||
				strings.Contains(strings.ToLower(entry.AppName), queryLower) ||
				strings.Contains(strings.ToLower(entry.Hostname), queryLower) ||
				strings.Contains(strings.ToLower(entry.Raw), queryLower)
			if !match {
				continue
			}
		}

		result = append(result, entry)

		if filter.Limit > 0 && len(result) >= filter.Limit {
			break
		}
	}

	return result
}
