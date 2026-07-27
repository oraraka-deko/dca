package utils

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// ProcessInfo holds information about a system process.
type ProcessInfo struct {
	PID           int32     `json:"pid"`
	Name          string    `json:"name"`
	CPUPercent    float64   `json:"cpu_percent"`
	MemoryPercent float32   `json:"memory_percent"`
	MemoryUsage   uint64    `json:"memory_usage"`
	Status        string    `json:"status"`
	Username      string    `json:"username"`
	CreateTime    time.Time `json:"create_time"`
}

// ServiceInfo holds information about a system service.
type ServiceInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"`
	Type        string `json:"type"`
}

// ScheduledTaskInfo holds info about crontab or Windows Scheduled Tasks.
type ScheduledTaskInfo struct {
	TaskName    string `json:"task_name"`
	NextRunTime string `json:"next_run_time"`
	Status      string `json:"status"`
	LastRunTime string `json:"last_run_time"`
	TaskToRun   string `json:"task_to_run"`
}

// ListProcesses returns a list of running processes based on filters and sorting.
func ListProcesses(query string, sortBy string, limit int) ([]ProcessInfo, error) {
	pids, err := process.Pids()
	if err != nil {
		return nil, fmt.Errorf("failed to get pids: %w", err)
	}

	var list []ProcessInfo
	queryLower := strings.ToLower(query)

	for _, pid := range pids {
		p, err := process.NewProcess(pid)
		if err != nil {
			continue // Skip processes that have exited
		}

		name, _ := p.Name()
		username, _ := p.Username()

		// Apply query filter (search PID, Name, Username)
		if queryLower != "" {
			pidStr := strconv.Itoa(int(pid))
			match := strings.Contains(strings.ToLower(name), queryLower) ||
				strings.Contains(strings.ToLower(username), queryLower) ||
				strings.Contains(pidStr, queryLower)
			if !match {
				continue
			}
		}

		cpuPercent, _ := p.CPUPercent()
		memPercent, _ := p.MemoryPercent()
		memInfo, _ := p.MemoryInfo()
		var memUsage uint64
		if memInfo != nil {
			memUsage = memInfo.RSS
		}

		status, _ := p.Status()
		createTimeMs, _ := p.CreateTime()
		createTime := time.Unix(createTimeMs/1000, (createTimeMs%1000)*1000000)

		list = append(list, ProcessInfo{
			PID:           pid,
			Name:          name,
			CPUPercent:    cpuPercent,
			MemoryPercent: memPercent,
			MemoryUsage:   memUsage,
			Status:        strings.Join(status, ", "),
			Username:      username,
			CreateTime:    createTime,
		})
	}

	// Sort list
	sort.Slice(list, func(i, j int) bool {
		switch strings.ToLower(sortBy) {
		case "cpu":
			return list[i].CPUPercent > list[j].CPUPercent
		case "mem":
			return list[i].MemoryUsage > list[j].MemoryUsage
		case "name":
			return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
		case "pid":
			fallthrough
		default:
			return list[i].PID < list[j].PID
		}
	})

	// Limit count
	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}

	return list, nil
}

// KillProcess terminates a process by PID.
func KillProcess(pid int32) error {
	p, err := process.NewProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

// SignalProcess sends control actions to a process (kill, terminate, suspend, resume).
func SignalProcess(pid int32, sig string) error {
	p, err := process.NewProcess(pid)
	if err != nil {
		return err
	}

	switch strings.ToLower(sig) {
	case "kill":
		return p.Kill()
	case "terminate":
		return p.Terminate()
	case "suspend":
		return p.Suspend()
	case "resume":
		return p.Resume()
	default:
		return fmt.Errorf("unsupported signal action: %s", sig)
	}
}

// ListServices queries system services for Windows (sc) and Linux (systemctl).
func ListServices(query string, limit int) ([]ServiceInfo, error) {
	if runtime.GOOS == "windows" {
		return listServicesWindows(query, limit)
	}
	return listServicesUnix(query, limit)
}

func listServicesWindows(query string, limit int) ([]ServiceInfo, error) {
	cmd := exec.Command("sc", "query", "type=", "service", "state=", "all", "bufsize=", "262144")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to query windows services: %w", err)
	}

	lines := strings.Split(string(output), "\n")
	var services []ServiceInfo
	var current ServiceInfo
	queryLower := strings.ToLower(query)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) < 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "SERVICE_NAME":
			if current.Name != "" {
				if matchService(current, queryLower) {
					services = append(services, current)
				}
				current = ServiceInfo{}
			}
			current.Name = val
		case "DISPLAY_NAME":
			current.DisplayName = val
		case "TYPE":
			current.Type = val
		case "STATE":
			// E.g. "4  RUNNING" -> extract "RUNNING"
			stateParts := strings.Fields(val)
			if len(stateParts) >= 2 {
				current.Status = stateParts[1]
			} else {
				current.Status = val
			}
		}
	}

	// Append last parsed service
	if current.Name != "" && matchService(current, queryLower) {
		services = append(services, current)
	}

	if limit > 0 && len(services) > limit {
		services = services[:limit]
	}

	return services, nil
}

func listServicesUnix(query string, limit int) ([]ServiceInfo, error) {
	cmd := exec.Command("systemctl", "list-units", "--type=service", "--all", "--no-legend", "--no-pager")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to query unix services: %w", err)
	}

	lines := strings.Split(string(output), "\n")
	var services []ServiceInfo
	queryLower := strings.ToLower(query)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		// systemctl output columns: UNIT LOAD ACTIVE SUB DESCRIPTION
		unit := fields[0]
		name := strings.TrimSuffix(unit, ".service")
		loadVal := fields[1]
		activeVal := fields[2]
		subVal := fields[3]

		desc := ""
		if len(fields) >= 5 {
			desc = strings.Join(fields[4:], " ")
		}

		s := ServiceInfo{
			Name:        name,
			DisplayName: desc,
			Status:      fmt.Sprintf("%s (%s)", activeVal, subVal),
			Type:        loadVal,
		}

		if matchService(s, queryLower) {
			services = append(services, s)
		}
	}

	if limit > 0 && len(services) > limit {
		services = services[:limit]
	}

	return services, nil
}

func matchService(s ServiceInfo, query string) bool {
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(s.Name), query) ||
		strings.Contains(strings.ToLower(s.DisplayName), query) ||
		strings.Contains(strings.ToLower(s.Status), query)
}

// ManageService starts or stops a service on Windows or Unix.
func ManageService(name string, action string) error {
	action = strings.ToLower(action)
	if action != "start" && action != "stop" {
		return fmt.Errorf("unsupported action: %s", action)
	}

	if runtime.GOOS == "windows" {
		cmd := exec.Command("sc", action, name)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("sc %s failed: %v, output: %s", action, err, strings.TrimSpace(string(out)))
		}
		return nil
	}

	cmd := exec.Command("systemctl", action, name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl %s failed: %v, output: %s", action, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// CreateService creates a system service on Windows or Unix.
func CreateService(name string, displayName string, binPath string) error {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("sc", "create", name, "binPath=", binPath, "DisplayName=", displayName, "start=", "auto")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("sc create failed: %v, output: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}

	// For Unix: Write systemd unit file (requires root privileges)
	unitContent := fmt.Sprintf(`[Unit]
Description=%s
After=network.target

[Service]
ExecStart=%s
Restart=always

[Install]
WantedBy=multi-user.target
`, displayName, binPath)

	unitPath := fmt.Sprintf("/etc/systemd/system/%s.service", name)
	err := os.WriteFile(unitPath, []byte(unitContent), 0644)
	if err != nil {
		return fmt.Errorf("failed to write systemd unit file: %w", err)
	}

	// Reload systemd daemon
	cmd := exec.Command("systemctl", "daemon-reload")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %v, output: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ListScheduledTasks lists scheduled tasks / cron jobs on Windows or Unix.
func ListScheduledTasks(query string, limit int) ([]ScheduledTaskInfo, error) {
	if runtime.GOOS == "windows" {
		return listTasksWindows(query, limit)
	}
	return listTasksUnix(query, limit)
}

func listTasksWindows(query string, limit int) ([]ScheduledTaskInfo, error) {
	// schtasks /query /fo csv /v
	cmd := exec.Command("schtasks", "/query", "/fo", "csv", "/v")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to query windows scheduled tasks: %w", err)
	}

	r := csv.NewReader(bytes.NewReader(output))
	// Windows schtasks might output warnings in the first few lines, skip until we find header
	var records [][]string
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue // skip malformed lines
		}
		records = append(records, rec)
	}

	if len(records) < 2 {
		return []ScheduledTaskInfo{}, nil
	}

	// Find column indices
	header := records[0]
	nameIdx, nextRunIdx, statusIdx, lastRunIdx, authorIdx := -1, -1, -1, -1, -1
	for idx, col := range header {
		colClean := strings.TrimSpace(col)
		switch colClean {
		case "TaskName":
			nameIdx = idx
		case "Next Run Time":
			nextRunIdx = idx
		case "Status":
			statusIdx = idx
		case "Last Run Time":
			lastRunIdx = idx
		case "Task To Run":
			authorIdx = idx
		}
	}

	var tasks []ScheduledTaskInfo
	queryLower := strings.ToLower(query)

	for _, rec := range records[1:] {
		name := ""
		if nameIdx >= 0 && nameIdx < len(rec) {
			name = rec[nameIdx]
		}
		nextRun := ""
		if nextRunIdx >= 0 && nextRunIdx < len(rec) {
			nextRun = rec[nextRunIdx]
		}
		status := ""
		if statusIdx >= 0 && statusIdx < len(rec) {
			status = rec[statusIdx]
		}
		lastRun := ""
		if lastRunIdx >= 0 && lastRunIdx < len(rec) {
			lastRun = rec[lastRunIdx]
		}
		taskToRun := ""
		if authorIdx >= 0 && authorIdx < len(rec) {
			taskToRun = rec[authorIdx]
		}

		t := ScheduledTaskInfo{
			TaskName:    name,
			NextRunTime: nextRun,
			Status:      status,
			LastRunTime: lastRun,
			TaskToRun:   taskToRun,
		}

		if matchTask(t, queryLower) {
			tasks = append(tasks, t)
		}
	}

	if limit > 0 && len(tasks) > limit {
		tasks = tasks[:limit]
	}

	return tasks, nil
}

func listTasksUnix(query string, limit int) ([]ScheduledTaskInfo, error) {
	cmd := exec.Command("crontab", "-l")
	output, err := cmd.Output()
	var lines []string
	if err == nil {
		lines = strings.Split(string(output), "\n")
	}

	var tasks []ScheduledTaskInfo
	queryLower := strings.ToLower(query)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue // skip empty lines and comments
		}

		// Simple cron parsing: * * * * * command
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}

		schedule := strings.Join(fields[:5], " ")
		command := strings.Join(fields[5:], " ")

		t := ScheduledTaskInfo{
			TaskName:    fmt.Sprintf("Cron: %s", schedule),
			NextRunTime: "N/A",
			Status:      "Enabled",
			LastRunTime: "N/A",
			TaskToRun:   command,
		}

		if matchTask(t, queryLower) {
			tasks = append(tasks, t)
		}
	}

	if limit > 0 && len(tasks) > limit {
		tasks = tasks[:limit]
	}

	return tasks, nil
}

func matchTask(t ScheduledTaskInfo, query string) bool {
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(t.TaskName), query) ||
		strings.Contains(strings.ToLower(t.TaskToRun), query) ||
		strings.Contains(strings.ToLower(t.Status), query)
}

// parseCronToSchtasks parses simple cron expressions into Windows schtasks arguments.
func parseCronToSchtasks(cron string) (schedule string, modifier string, startTime string, day string, err error) {
	cron = strings.TrimSpace(cron)
	fields := strings.Fields(cron)
	if len(fields) != 5 {
		return "", "", "", "", fmt.Errorf("invalid cron expression format: must have exactly 5 fields")
	}

	minute, hour, dom, month, dow := fields[0], fields[1], fields[2], fields[3], fields[4]

	// 1. Check for minutes interval: */N * * * *
	if strings.HasPrefix(minute, "*/") && hour == "*" && dom == "*" && month == "*" && dow == "*" {
		minValStr := strings.TrimPrefix(minute, "*/")
		if _, err := strconv.Atoi(minValStr); err == nil {
			return "MINUTE", minValStr, "", "", nil
		}
	}

	// 2. Check for hourly: M * * * *
	if _, err1 := strconv.Atoi(minute); err1 == nil && hour == "*" && dom == "*" && month == "*" && dow == "*" {
		return "HOURLY", "1", "", "", nil
	}

	// 3. Check for daily: M H * * *
	mVal, err1 := strconv.Atoi(minute)
	hVal, err2 := strconv.Atoi(hour)
	if err1 == nil && err2 == nil && dom == "*" && month == "*" && dow == "*" {
		st := fmt.Sprintf("%02d:%02d", hVal, mVal)
		return "DAILY", "", st, "", nil
	}

	// 4. Check for weekly: M H * * D
	dVal, err3 := strconv.Atoi(dow)
	if err1 == nil && err2 == nil && dom == "*" && month == "*" && err3 == nil {
		st := fmt.Sprintf("%02d:%02d", hVal, mVal)
		days := []string{"SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"}
		if dVal >= 0 && dVal <= 7 {
			return "WEEKLY", "", st, days[dVal], nil
		}
	}

	// 5. Check for monthly: M H D * *
	domVal, err4 := strconv.Atoi(dom)
	if err1 == nil && err2 == nil && err4 == nil && month == "*" && dow == "*" {
		st := fmt.Sprintf("%02d:%02d", hVal, mVal)
		if domVal >= 1 && domVal <= 31 {
			return "MONTHLY", "", st, strconv.Itoa(domVal), nil
		}
	}

	return "", "", "", "", fmt.Errorf("unsupported complex cron expression on Windows: %s. Use simple expressions or standard Windows frequency (MINUTE, HOURLY, DAILY, WEEKLY, MONTHLY, ONCE, etc.)", cron)
}

// CreateScheduledTask schedules a new task on Windows or Unix crontab.
func CreateScheduledTask(name string, taskRun string, schedule string, startTime string) error {
	if runtime.GOOS == "windows" {
		sc := strings.ToUpper(strings.TrimSpace(schedule))
		var modifier, day string

		if strings.ContainsAny(schedule, " \t*") {
			var err error
			sc, modifier, startTime, day, err = parseCronToSchtasks(schedule)
			if err != nil {
				return fmt.Errorf("failed to parse schedule as cron for Windows: %w", err)
			}
		}

		args := []string{"/create", "/tn", name, "/tr", taskRun, "/sc", sc}
		if modifier != "" {
			args = append(args, "/mo", modifier)
		}
		if startTime != "" {
			args = append(args, "/st", startTime)
		}
		if day != "" {
			args = append(args, "/d", day)
		}
		args = append(args, "/f") // overwrite if exists

		cmd := exec.Command("schtasks", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("schtasks failed: %v, output: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}

	// For Unix: Add to cron (schedule is cron format, e.g. "0 5 * * *")
	currentTasks, _ := exec.Command("crontab", "-l").Output()
	newCronLine := fmt.Sprintf("%s %s # %s\n", schedule, taskRun, name)

	updatedContent := string(currentTasks)
	if !strings.HasSuffix(updatedContent, "\n") && len(updatedContent) > 0 {
		updatedContent += "\n"
	}
	updatedContent += newCronLine

	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(updatedContent)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("crontab failed: %v, output: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DeleteScheduledTask removes a scheduled task or cron job.
func DeleteScheduledTask(name string) error {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("schtasks", "/delete", "/tn", name, "/f")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("schtasks delete failed: %v, output: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}

	// For Unix: Remove line containing `# name` comment
	currentTasks, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		return err
	}

	lines := strings.Split(string(currentTasks), "\n")
	var keptLines []string
	commentSuffix := fmt.Sprintf("# %s", name)

	for _, line := range lines {
		if strings.Contains(line, commentSuffix) {
			continue // filter out this task
		}
		keptLines = append(keptLines, line)
	}

	updatedContent := strings.Join(keptLines, "\n")
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(updatedContent)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("crontab delete failed: %v, output: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
