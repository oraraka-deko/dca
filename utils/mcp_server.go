package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"dca/database"
)

var windowsBuiltins = map[string]bool{
	"echo": true, "dir": true, "cd": true, "type": true, "copy": true,
	"move": true, "del": true, "md": true, "mkdir": true, "rd": true,
	"rmdir": true, "cls": true, "ver": true, "vol": true, "path": true,
	"set": true,
}

var unixBuiltins = map[string]bool{
	"echo": true, "cd": true, "pwd": true, "alias": true, "export": true,
	"set": true, "history": true,
}

// MCPServerWrapper encapsulates the GoFrame HTTP server and mcp-go tools.
type MCPServerWrapper struct {
	Cfg            ServerConfig
	MCPServer      *server.MCPServer
	TaskManager    *TaskManager
	SandboxManager *VFSSandboxManager
	TimerChain     *TimerChainManager
	Hook           *MultiHook
	FileManager    *FileManager
	Store          database.Store
	gfServer       *ghttp.Server
}

// NewMCPServerWrapper creates and initializes the MCP server wrapper with all utility tools.
func NewMCPServerWrapper(cfg ServerConfig) *MCPServerWrapper {
	hook := NewMultiHook()
	tm := NewTaskManager(4)
	tm.SetHook(hook)
	tm.Start()

	tcm := NewTimerChainManager(tm, hook)
	tcm.StartWatchdog(10*time.Second, 10*time.Minute) // Default watchdog safety

	vfsMgr := NewVFSSandboxManager()
	fm, _ := NewFileManager("")

	dbStore, err := database.NewStore(cfg.DBType, cfg.DBConnString)
	if err != nil {
		dbStore, _ = database.NewStore("sqlite", ":memory:")
	}
	if dbStore != nil {
		tm.SetStore(dbStore)
	}

	mcpSrv := server.NewMCPServer("dca-mcp-server", "1.0.0")

	wrapper := &MCPServerWrapper{
		Cfg:            cfg,
		MCPServer:      mcpSrv,
		TaskManager:    tm,
		SandboxManager: vfsMgr,
		TimerChain:     tcm,
		Hook:           hook,
		FileManager:    fm,
		Store:          dbStore,
	}

	wrapper.RegisterAllTools()
	return wrapper
}

// RegisterAllTools registers TaskManager, VFS Sandbox, System Monitor, and Git tools with mcp-go.
func (w *MCPServerWrapper) RegisterAllTools() {
	// ==========================================
	// 1. TaskManager Tools
	// ==========================================
	w.registerTool("submit_command", "Submit an OS shell or binary command as a background queued task",
		[]mcp.ToolOption{
			mcp.WithString("name", mcp.Required(), mcp.Description("Task descriptive name")),
			mcp.WithString("command", mcp.Required(), mcp.Description("Executable command")),
			mcp.WithString("args", mcp.Description("Space-separated command arguments")),
			mcp.WithBoolean("use_shell", mcp.Description("If true, runs command inside default system shell (cmd.exe or /bin/sh)")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			name, _ := req.RequireString("name")
			command, _ := req.RequireString("command")
			argsStr := req.GetString("args", "")
			useShell := req.GetBool("use_shell", false)

			cmdLower := strings.ToLower(strings.TrimSpace(command))
			if runtime.GOOS == "windows" {
				if windowsBuiltins[cmdLower] {
					useShell = true
				}
			} else {
				if unixBuiltins[cmdLower] {
					useShell = true
				}
			}

			var args []string
			if strings.TrimSpace(argsStr) != "" {
				args = strings.Fields(argsStr)
			}

			task, err := w.TaskManager.SubmitCommand(name, useShell, command, args...)
			if err != nil {
				return mcp.NewToolResultError("Failed submitting command task: " + err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Task submitted successfully. ID: %s, Status: %s", task.ID, task.Status)), nil
		},
	)

	w.registerTool("list_tasks", "List all queued, running, and completed background tasks",
		nil,
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tasks := w.TaskManager.ListTasks()
			var sb strings.Builder
			for _, t := range tasks {
				sb.WriteString(fmt.Sprintf("[%s] '%s' - %s (Progress: %.0f%%)\n", t.ID, t.Name, t.Status, t.Progress*100))
			}
			if len(tasks) == 0 {
				sb.WriteString("No active tasks in queue.")
			}
			return mcp.NewToolResultText(sb.String()), nil
		},
	)

	w.registerTool("get_task_status", "Get detailed status and output result of a task by ID",
		[]mcp.ToolOption{
			mcp.WithString("id", mcp.Required(), mcp.Description("Task ID")),
			mcp.WithBoolean("tail_only", mcp.Description("If true, returns only last lines of output")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			id, _ := req.RequireString("id")
			tailOnly := req.GetBool("tail_only", false)

			snap, found := w.TaskManager.GetTask(id)
			if !found {
				return mcp.NewToolResultError("Task ID not found: " + id), nil
			}

			output := snap.Result
			if snap.Error != "" {
				output += "\nError: " + snap.Error
			}

			truncatedOut, _ := SmartTruncate(output, TruncateOptions{
				MaxLines:  200,
				TailOnly:  tailOnly,
				TailLines: 50,
			})

			resText := fmt.Sprintf("Task ID: %s\nName: %s\nStatus: %s\nResult Output:\n%s", snap.ID, snap.Name, snap.Status, truncatedOut)
			return mcp.NewToolResultText(resText), nil
		},
	)

	w.registerTool("cancel_task", "Cancel a pending or running task by ID",
		[]mcp.ToolOption{
			mcp.WithString("id", mcp.Required(), mcp.Description("Task ID to cancel")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			id, _ := req.RequireString("id")
			if err := w.TaskManager.CancelTask(id); err != nil {
				return mcp.NewToolResultError("Failed cancelling task: " + err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Task %s cancelled.", id)), nil
		},
	)

	// ==========================================
	// 2. VFS Sandbox Tools
	// ==========================================
	w.registerTool("create_sandbox", "Create a new isolated Virtual Filesystem sandbox workspace",
		[]mcp.ToolOption{
			mcp.WithString("id", mcp.Required(), mcp.Description("Sandbox identifier")),
			mcp.WithString("type", mcp.Description("Sandbox type: MemFs (default), BasePathFs, CopyOnWriteFs")),
			mcp.WithString("base_dir", mcp.Description("Base directory path for BasePathFs or CopyOnWriteFs")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			id, _ := req.RequireString("id")
			sbType := req.GetString("type", "MemFs")
			baseDir := req.GetString("base_dir", "")

			var err error
			switch sbType {
			case "BasePathFs":
				_, err = w.SandboxManager.CreateBasePathSandbox(id, baseDir)
			case "CopyOnWriteFs":
				_, err = w.SandboxManager.CreateCopyOnWriteSandbox(id, baseDir)
			default:
				_, err = w.SandboxManager.CreateMemSandbox(id)
			}

			if err != nil {
				return mcp.NewToolResultError("Failed creating sandbox: " + err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Sandbox '%s' (%s) created successfully.", id, sbType)), nil
		},
	)

	w.registerTool("sandbox_write_file", "Write content to a file inside an isolated sandbox workspace",
		[]mcp.ToolOption{
			mcp.WithString("sandbox_id", mcp.Required(), mcp.Description("Sandbox ID")),
			mcp.WithString("path", mcp.Required(), mcp.Description("File relative path inside sandbox")),
			mcp.WithString("content", mcp.Required(), mcp.Description("File content string")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			sbID, _ := req.RequireString("sandbox_id")
			path, _ := req.RequireString("path")
			content, _ := req.RequireString("content")

			sb, found := w.SandboxManager.GetSandbox(sbID)
			if !found {
				return mcp.NewToolResultError("Sandbox not found: " + sbID), nil
			}

			if err := sb.WriteFile(path, []byte(content), 0644); err != nil {
				return mcp.NewToolResultError("WriteFile failed: " + err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Wrote %d bytes to '%s' in sandbox '%s'", len(content), path, sbID)), nil
		},
	)

	w.registerTool("sandbox_read_file", "Read content of a file inside a sandbox workspace",
		[]mcp.ToolOption{
			mcp.WithString("sandbox_id", mcp.Required(), mcp.Description("Sandbox ID")),
			mcp.WithString("path", mcp.Required(), mcp.Description("File relative path")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			sbID, _ := req.RequireString("sandbox_id")
			path, _ := req.RequireString("path")

			sb, found := w.SandboxManager.GetSandbox(sbID)
			if !found {
				return mcp.NewToolResultError("Sandbox not found: " + sbID), nil
			}

			data, err := sb.ReadFile(path)
			if err != nil {
				return mcp.NewToolResultError("ReadFile failed: " + err.Error()), nil
			}

			truncated, _ := SmartTruncate(string(data), DefaultTruncateOptions())
			return mcp.NewToolResultText(truncated), nil
		},
	)

	// ==========================================
	// 3. System & Monitor Tools
	// ==========================================
	w.registerTool("get_system_status", "Retrieve overall system health, CPU, memory, uptime, and IP information",
		nil,
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			status := GetStatusInfoJSON()
			info := fmt.Sprintf("Host: %s (%s %s)\nUptime: %d sec\nCPU Usage: %.1f%%\nMem Usage: %.1f%% (%d / %d MB)\nIPv4: %s\nIPv6: %s",
				status.Hostname, status.OS, status.KernelArch, status.Uptime, status.CPUPercent, status.MemUsedPercent,
				status.MemCurrent/1024/1024, status.MemTotal/1024/1024, strings.Join(status.IPv4, ", "), strings.Join(status.IPv6, ", "))
			return mcp.NewToolResultText(info), nil
		},
	)

	w.registerTool("list_processes", "List running system processes with CPU/Memory usage",
		[]mcp.ToolOption{
			mcp.WithString("query", mcp.Description("Filter by process name, PID, or user")),
			mcp.WithString("limit", mcp.Description("Max results count (default 20)")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			query := req.GetString("query", "")
			limitStr := req.GetString("limit", "20")
			limit, _ := strconv.Atoi(limitStr)
			if limit <= 0 {
				limit = 20
			}

			procs, err := ListProcesses(query, "cpu", limit)
			if err != nil {
				return mcp.NewToolResultError("Failed listing processes: " + err.Error()), nil
			}

			var sb strings.Builder
			for _, p := range procs {
				sb.WriteString(fmt.Sprintf("PID: %d | Name: %s | CPU: %.1f%% | Mem: %.1f%%\n", p.PID, p.Name, p.CPUPercent, p.MemoryPercent))
			}
			return mcp.NewToolResultText(sb.String()), nil
		},
	)

	// ==========================================
	// 4. File System Manager Tools (Real Disk)
	// ==========================================
	w.registerTool("file_manager_list", "List files and directories in current working directory or given path",
		[]mcp.ToolOption{
			mcp.WithString("path", mcp.Description("Optional path to list. If empty, lists current file manager directory")),
			mcp.WithBoolean("show_hidden", mcp.Description("Optional. If true, includes hidden files")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			path := req.GetString("path", "")
			showHidden := req.GetBool("show_hidden", false)

			w.FileManager.ShowHidden = showHidden
			if path != "" {
				if err := w.FileManager.GoTo(path); err != nil {
					return mcp.NewToolResultError("Failed to go to path: " + err.Error()), nil
				}
			}

			list, err := w.FileManager.List()
			if err != nil {
				return mcp.NewToolResultError("Failed to list directory: " + err.Error()), nil
			}

			data, _ := json.MarshalIndent(list, "", "  ")
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	w.registerTool("file_manager_goto", "Change the file manager's active working directory",
		[]mcp.ToolOption{
			mcp.WithString("path", mcp.Required(), mcp.Description("Absolute or relative path to navigate to")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			path, _ := req.RequireString("path")
			if err := w.FileManager.GoTo(path); err != nil {
				return mcp.NewToolResultError("Failed to change directory: " + err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Moved to %s", w.FileManager.CurrentPath)), nil
		},
	)

	w.registerTool("file_manager_mkdir", "Create a new directory in the file manager's active working directory",
		[]mcp.ToolOption{
			mcp.WithString("name", mcp.Required(), mcp.Description("Directory name")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			name, _ := req.RequireString("name")
			if err := w.FileManager.Mkdir(name); err != nil {
				return mcp.NewToolResultError("Failed to create directory: " + err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Directory %q created successfully", name)), nil
		},
	)

	w.registerTool("file_manager_create_file", "Create a new empty file in the file manager's active working directory",
		[]mcp.ToolOption{
			mcp.WithString("name", mcp.Required(), mcp.Description("File name")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			name, _ := req.RequireString("name")
			if err := w.FileManager.CreateFile(name); err != nil {
				return mcp.NewToolResultError("Failed to create file: " + err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("File %q created successfully", name)), nil
		},
	)

	w.registerTool("file_manager_delete", "Delete specified files and directories recursively",
		[]mcp.ToolOption{
			mcp.WithString("paths", mcp.Required(), mcp.Description("Comma-separated list of absolute/relative file paths to delete")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			pathsStr, _ := req.RequireString("paths")
			parts := strings.Split(pathsStr, ",")
			var list []string
			for _, p := range parts {
				trimmed := strings.TrimSpace(p)
				if trimmed != "" {
					list = append(list, trimmed)
				}
			}
			if err := w.FileManager.Delete(list); err != nil {
				return mcp.NewToolResultError("Failed to delete paths: " + err.Error()), nil
			}
			return mcp.NewToolResultText("Specified paths deleted successfully"), nil
		},
	)

	w.registerTool("file_manager_rename", "Rename a file or folder",
		[]mcp.ToolOption{
			mcp.WithString("old_path", mcp.Required(), mcp.Description("Full path to the existing file/folder")),
			mcp.WithString("new_name", mcp.Required(), mcp.Description("New name for the file/folder")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			oldPath, _ := req.RequireString("old_path")
			newName, _ := req.RequireString("new_name")
			if err := w.FileManager.Rename(oldPath, newName); err != nil {
				return mcp.NewToolResultError("Failed to rename: " + err.Error()), nil
			}
			return mcp.NewToolResultText("Renamed successfully"), nil
		},
	)

	w.registerTool("file_manager_copy", "Copy specified file/directory paths to clipboard state",
		[]mcp.ToolOption{
			mcp.WithString("paths", mcp.Required(), mcp.Description("Comma-separated list of file/directory paths to copy")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			pathsStr, _ := req.RequireString("paths")
			parts := strings.Split(pathsStr, ",")
			var list []string
			for _, p := range parts {
				trimmed := strings.TrimSpace(p)
				if trimmed != "" {
					list = append(list, trimmed)
				}
			}
			w.FileManager.Copy(list)
			return mcp.NewToolResultText("Paths copied to clipboard state"), nil
		},
	)

	w.registerTool("file_manager_cut", "Cut specified file/directory paths to clipboard state",
		[]mcp.ToolOption{
			mcp.WithString("paths", mcp.Required(), mcp.Description("Comma-separated list of file/directory paths to cut")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			pathsStr, _ := req.RequireString("paths")
			parts := strings.Split(pathsStr, ",")
			var list []string
			for _, p := range parts {
				trimmed := strings.TrimSpace(p)
				if trimmed != "" {
					list = append(list, trimmed)
				}
			}
			w.FileManager.Cut(list)
			return mcp.NewToolResultText("Paths cut to clipboard state"), nil
		},
	)

	w.registerTool("file_manager_paste", "Paste files from clipboard state to the active working directory",
		nil,
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if err := w.FileManager.Paste(); err != nil {
				return mcp.NewToolResultError("Paste failed: " + err.Error()), nil
			}
			return mcp.NewToolResultText("Clipboard contents pasted successfully"), nil
		},
	)

	w.registerTool("file_manager_search", "Recursively search for files/folders matching a query term",
		[]mcp.ToolOption{
			mcp.WithString("query", mcp.Required(), mcp.Description("Search term")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			query, _ := req.RequireString("query")
			results, err := w.FileManager.Search(query)
			if err != nil {
				return mcp.NewToolResultError("Search failed: " + err.Error()), nil
			}
			data, _ := json.MarshalIndent(results, "", "  ")
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	w.registerTool("file_manager_get_preview", "Get a text preview or metadata description of a file or directory",
		[]mcp.ToolOption{
			mcp.WithString("path", mcp.Required(), mcp.Description("Path to preview")),
			mcp.WithNumber("max_lines", mcp.Description("Optional. Max text lines to return (default 100)")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			path, _ := req.RequireString("path")
			maxLinesVal := req.GetFloat("max_lines", 100)
			maxLines := int(maxLinesVal)

			previewType, text, err := w.FileManager.GetPreview(path, maxLines)
			if err != nil {
				return mcp.NewToolResultError("Preview failed: " + err.Error()), nil
			}

			resText := fmt.Sprintf("Type: %s\nContent:\n%s", previewType, text)
			return mcp.NewToolResultText(resText), nil
		},
	)

	w.registerTool("file_manager_bookmarks", "Manage pinned bookmark directory paths",
		[]mcp.ToolOption{
			mcp.WithString("action", mcp.Required(), mcp.Description("Action to perform: add, remove, list")),
			mcp.WithString("path", mcp.Description("Path to add or remove")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			action, _ := req.RequireString("action")
			path := req.GetString("path", "")

			switch strings.ToLower(action) {
			case "add":
				if path == "" {
					return mcp.NewToolResultError("path is required for add action"), nil
				}
				w.FileManager.AddBookmark(path)
				return mcp.NewToolResultText("Bookmark added"), nil
			case "remove":
				if path == "" {
					return mcp.NewToolResultError("path is required for remove action"), nil
				}
				w.FileManager.RemoveBookmark(path)
				return mcp.NewToolResultText("Bookmark removed"), nil
			case "list":
				bookmarks := w.FileManager.GetBookmarks()
				data, _ := json.MarshalIndent(bookmarks, "", "  ")
				return mcp.NewToolResultText(string(data)), nil
			default:
				return mcp.NewToolResultError("Unsupported action. Choose 'add', 'remove', or 'list'"), nil
			}
		},
	)

	// ==========================================
	// 5. Git Version Control Tools
	// ==========================================
	w.registerTool("git_init", "Initialize a new Git repository",
		[]mcp.ToolOption{
			mcp.WithString("path", mcp.Required(), mcp.Description("Target directory path")),
			mcp.WithBoolean("bare", mcp.Description("Optional. Initialize as bare repository")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			path, _ := req.RequireString("path")
			bare := req.GetBool("bare", false)
			_, err := InitGitManager(path, bare)
			if err != nil {
				return mcp.NewToolResultError("Failed to init git: " + err.Error()), nil
			}
			return mcp.NewToolResultText("Git repository initialized successfully"), nil
		},
	)

	w.registerTool("git_status", "Get compact git status showing staged, modified, and untracked files",
		[]mcp.ToolOption{
			mcp.WithString("path", mcp.Required(), mcp.Description("Git repository directory path")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			path, _ := req.RequireString("path")
			g, err := NewGitManager(path)
			if err != nil {
				return mcp.NewToolResultError("Failed to open git repo: " + err.Error()), nil
			}
			status, err := g.GetCompactStatus()
			if err != nil {
				return mcp.NewToolResultError("Failed to get status: " + err.Error()), nil
			}
			data, _ := json.MarshalIndent(status, "", "  ")
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	w.registerTool("git_log", "Get compact git commit log history list",
		[]mcp.ToolOption{
			mcp.WithString("path", mcp.Required(), mcp.Description("Git repository directory path")),
			mcp.WithNumber("limit", mcp.Description("Optional. Max commits to return (default 20)")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			path, _ := req.RequireString("path")
			limitVal := req.GetFloat("limit", 20)
			limit := int(limitVal)

			g, err := NewGitManager(path)
			if err != nil {
				return mcp.NewToolResultError("Failed to open git repo: " + err.Error()), nil
			}
			logs, err := g.GetCompactLog(limit)
			if err != nil {
				return mcp.NewToolResultError("Failed to get log: " + err.Error()), nil
			}
			return mcp.NewToolResultText(strings.Join(logs, "\n")), nil
		},
	)

	w.registerTool("git_add", "Stage files in a git repository",
		[]mcp.ToolOption{
			mcp.WithString("path", mcp.Required(), mcp.Description("Git repository directory path")),
			mcp.WithString("files", mcp.Required(), mcp.Description("Comma-separated list of relative file paths to stage (use '.' to stage all)")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			path, _ := req.RequireString("path")
			filesStr, _ := req.RequireString("files")

			g, err := NewGitManager(path)
			if err != nil {
				return mcp.NewToolResultError("Failed to open git repo: " + err.Error()), nil
			}

			parts := strings.Split(filesStr, ",")
			var list []string
			for _, f := range parts {
				trimmed := strings.TrimSpace(f)
				if trimmed != "" {
					list = append(list, trimmed)
				}
			}

			if err := g.Add(list...); err != nil {
				return mcp.NewToolResultError("Failed to add files: " + err.Error()), nil
			}
			return mcp.NewToolResultText("Files staged successfully"), nil
		},
	)

	w.registerTool("git_commit", "Commit staged changes in a git repository",
		[]mcp.ToolOption{
			mcp.WithString("path", mcp.Required(), mcp.Description("Git repository directory path")),
			mcp.WithString("message", mcp.Required(), mcp.Description("Commit message")),
			mcp.WithString("author", mcp.Description("Optional author name")),
			mcp.WithString("email", mcp.Description("Optional author email")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			path, _ := req.RequireString("path")
			msg, _ := req.RequireString("message")
			author := req.GetString("author", "Agent")
			email := req.GetString("email", "agent@localhost")

			g, err := NewGitManager(path)
			if err != nil {
				return mcp.NewToolResultError("Failed to open git repo: " + err.Error()), nil
			}
			hash, err := g.Commit(msg, author, email)
			if err != nil {
				return mcp.NewToolResultError("Failed to commit: " + err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Committed successfully. Hash: %s", hash.String())), nil
		},
	)

	w.registerTool("git_checkout", "Checkout branch or commit hash in a git repository",
		[]mcp.ToolOption{
			mcp.WithString("path", mcp.Required(), mcp.Description("Git repository directory path")),
			mcp.WithString("branch", mcp.Required(), mcp.Description("Branch name or commit hash")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			path, _ := req.RequireString("path")
			branch, _ := req.RequireString("branch")

			g, err := NewGitManager(path)
			if err != nil {
				return mcp.NewToolResultError("Failed to open git repo: " + err.Error()), nil
			}

			if err := g.Checkout(branch, nil); err != nil {
				if len(branch) >= 7 {
					h := plumbing.NewHash(branch)
					if errHash := g.CheckoutHash(h); errHash == nil {
						return mcp.NewToolResultText(fmt.Sprintf("Checked out commit hash %s successfully", branch)), nil
					}
				}
				return mcp.NewToolResultError("Checkout failed: " + err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Checked out branch %s successfully", branch)), nil
		},
	)

	w.registerTool("git_branch", "List, create, or delete branches in a git repository",
		[]mcp.ToolOption{
			mcp.WithString("path", mcp.Required(), mcp.Description("Git repository directory path")),
			mcp.WithString("action", mcp.Required(), mcp.Description("Action: list, create, delete")),
			mcp.WithString("name", mcp.Description("Branch name (required for create/delete)")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			path, _ := req.RequireString("path")
			action, _ := req.RequireString("action")
			name := req.GetString("name", "")

			g, err := NewGitManager(path)
			if err != nil {
				return mcp.NewToolResultError("Failed to open git repo: " + err.Error()), nil
			}

			switch strings.ToLower(action) {
			case "list":
				branches, err := g.Branches()
				if err != nil {
					return mcp.NewToolResultError("Failed to list branches: " + err.Error()), nil
				}
				var list []string
				for _, ref := range branches {
					list = append(list, ref.Name().Short())
				}
				return mcp.NewToolResultText(strings.Join(list, "\n")), nil
			case "create":
				if name == "" {
					return mcp.NewToolResultError("name is required to create a branch"), nil
				}
				if err := g.CreateBranch(name); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf("Branch %q created successfully", name)), nil
			case "delete":
				if name == "" {
					return mcp.NewToolResultError("name is required to delete a branch"), nil
				}
				if err := g.DeleteBranch(name); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf("Branch %q deleted successfully", name)), nil
			default:
				return mcp.NewToolResultError("Unsupported action. Use 'list', 'create', or 'delete'"), nil
			}
		},
	)

	w.registerTool("git_checkpoint", "Stage all changes and commit a checkpoint snapshot for history backup",
		[]mcp.ToolOption{
			mcp.WithString("path", mcp.Required(), mcp.Description("Git repository directory path")),
			mcp.WithString("label", mcp.Description("Optional checkpoint label")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			path, _ := req.RequireString("path")
			label := req.GetString("label", "")

			g, err := NewGitManager(path)
			if err != nil {
				return mcp.NewToolResultError("Failed to open git repo: " + err.Error()), nil
			}
			hash, err := g.CreateGitCheckpoint(label)
			if err != nil {
				return mcp.NewToolResultError("Failed to create checkpoint: " + err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Checkpoint created successfully. Commit Hash: %s", hash)), nil
		},
	)

	// ==========================================
	// 6. System Process & Service Tools
	// ==========================================
	w.registerTool("kill_process", "Kill/terminate a system process by PID",
		[]mcp.ToolOption{
			mcp.WithNumber("pid", mcp.Required(), mcp.Description("Process PID")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			pidVal, _ := req.RequireFloat("pid")
			pid := int32(pidVal)
			if err := KillProcess(pid); err != nil {
				return mcp.NewToolResultError("Failed to kill process: " + err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Process %d killed successfully", pid)), nil
		},
	)

	w.registerTool("signal_process", "Send control signals to a process (kill, terminate, suspend, resume)",
		[]mcp.ToolOption{
			mcp.WithNumber("pid", mcp.Required(), mcp.Description("Process PID")),
			mcp.WithString("signal", mcp.Required(), mcp.Description("Signal action: kill, terminate, suspend, resume")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			pidVal, _ := req.RequireFloat("pid")
			pid := int32(pidVal)
			sig, _ := req.RequireString("signal")
			if err := SignalProcess(pid, sig); err != nil {
				return mcp.NewToolResultError("Failed sending signal to process: " + err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Signal %q sent to process %d successfully", sig, pid)), nil
		},
	)

	w.registerTool("list_services", "List system services (Windows sc or Linux systemd)",
		[]mcp.ToolOption{
			mcp.WithString("query", mcp.Description("Optional filter query")),
			mcp.WithNumber("limit", mcp.Description("Optional limit on return items (default 100)")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			query := req.GetString("query", "")
			limitVal := req.GetFloat("limit", 100)
			limit := int(limitVal)

			services, err := ListServices(query, limit)
			if err != nil {
				return mcp.NewToolResultError("Failed to list services: " + err.Error()), nil
			}
			data, _ := json.MarshalIndent(services, "", "  ")
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	w.registerTool("manage_service", "Start or stop a system service (Windows sc or Linux systemctl)",
		[]mcp.ToolOption{
			mcp.WithString("name", mcp.Required(), mcp.Description("Service name")),
			mcp.WithString("action", mcp.Required(), mcp.Description("Action: start, stop")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			name, _ := req.RequireString("name")
			action, _ := req.RequireString("action")
			if err := ManageService(name, action); err != nil {
				return mcp.NewToolResultError("Service management failed: " + err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Service %q command %q completed successfully", name, action)), nil
		},
	)

	w.registerTool("create_service", "Register a new background system service on the host",
		[]mcp.ToolOption{
			mcp.WithString("name", mcp.Required(), mcp.Description("Service name")),
			mcp.WithString("display_name", mcp.Required(), mcp.Description("Service display name description")),
			mcp.WithString("bin_path", mcp.Required(), mcp.Description("Binary executable path")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			name, _ := req.RequireString("name")
			displayName, _ := req.RequireString("display_name")
			binPath, _ := req.RequireString("bin_path")
			if err := CreateService(name, displayName, binPath); err != nil {
				return mcp.NewToolResultError("Failed to create service: " + err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Service %q registered successfully", name)), nil
		},
	)

	w.registerTool("list_scheduled_tasks", "List system scheduled tasks (Windows Task Scheduler or Linux crontab)",
		[]mcp.ToolOption{
			mcp.WithString("query", mcp.Description("Optional search filter query")),
			mcp.WithNumber("limit", mcp.Description("Optional limit on returned items (default 100)")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			query := req.GetString("query", "")
			limitVal := req.GetFloat("limit", 100)
			limit := int(limitVal)

			tasks, err := ListScheduledTasks(query, limit)
			if err != nil {
				return mcp.NewToolResultError("Failed to list scheduled tasks: " + err.Error()), nil
			}
			data, _ := json.MarshalIndent(tasks, "", "  ")
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	w.registerTool("create_scheduled_task", "Create/schedule a new cron job or Windows scheduled task",
		[]mcp.ToolOption{
			mcp.WithString("name", mcp.Required(), mcp.Description("Task descriptor name")),
			mcp.WithString("task_run", mcp.Required(), mcp.Description("Executable/command path to run")),
			mcp.WithString("schedule", mcp.Required(), mcp.Description("Cron format schedule (Linux, e.g. '0 5 * * *') or Windows frequency (minute, hourly, daily)")),
			mcp.WithString("start_time", mcp.Description("Optional start time (Windows, e.g. 'HH:MM')")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			name, _ := req.RequireString("name")
			taskRun, _ := req.RequireString("task_run")
			schedule, _ := req.RequireString("schedule")
			startTime := req.GetString("start_time", "")

			if err := CreateScheduledTask(name, taskRun, schedule, startTime); err != nil {
				return mcp.NewToolResultError("Failed creating scheduled task: " + err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Scheduled task %q created successfully", name)), nil
		},
	)

	w.registerTool("delete_scheduled_task", "Delete a cron job or scheduled task by name",
		[]mcp.ToolOption{
			mcp.WithString("name", mcp.Required(), mcp.Description("Task name")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			name, _ := req.RequireString("name")
			if err := DeleteScheduledTask(name); err != nil {
				return mcp.NewToolResultError("Failed deleting scheduled task: " + err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Scheduled task %q deleted successfully", name)), nil
		},
	)

	// ==========================================
	// 7. Code Analysis & Parsing Tools
	// ==========================================
	w.registerTool("get_code_outline", "Parse a code file and extract an outline of functions, structs, types, etc.",
		[]mcp.ToolOption{
			mcp.WithString("file_path", mcp.Required(), mcp.Description("Path to code file")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			filePath, _ := req.RequireString("file_path")
			symbols, err := GetCodeOutline(filePath)
			if err != nil {
				return mcp.NewToolResultError("Failed to parse code outline: " + err.Error()), nil
			}
			data, _ := json.MarshalIndent(symbols, "", "  ")
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	// ==========================================
	// 8. System Log Inspection Tools
	// ==========================================
	w.registerTool("get_system_logs", "Read and parse syslog/system log entries line by line. Queries Windows Event Logs if file_path is empty/not found.",
		[]mcp.ToolOption{
			mcp.WithString("file_path", mcp.Description("Path to syslog file (optional, defaults to /var/log/syslog on Unix).")),
			mcp.WithString("query", mcp.Description("Optional query filter for messages/apps/hosts")),
			mcp.WithString("severities", mcp.Description("Optional comma-separated list of severities (e.g. info,error)")),
			mcp.WithNumber("limit", mcp.Description("Optional maximum logs returned (default 100)")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			filePath := req.GetString("file_path", "")
			query := req.GetString("query", "")
			severitiesStr := req.GetString("severities", "")
			limitVal := req.GetFloat("limit", 100)
			limit := int(limitVal)

			var entries []SyslogEntry
			var err error

			useWindowsLogs := false
			if runtime.GOOS == "windows" {
				if filePath == "" || filePath == "/var/log/syslog" {
					useWindowsLogs = true
				} else {
					if _, errStat := os.Stat(filePath); os.IsNotExist(errStat) {
						useWindowsLogs = true
					}
				}
			}

			if useWindowsLogs {
				fetchLimit := limit
				if query != "" || severitiesStr != "" {
					fetchLimit = limit * 3
					if fetchLimit > 1000 {
						fetchLimit = 1000
					}
				}
				entries, err = GetWindowsEventLogs(fetchLimit)
				if err != nil {
					return mcp.NewToolResultError("Failed querying Windows Event Logs: " + err.Error()), nil
				}
			} else {
				if filePath == "" {
					filePath = "/var/log/syslog"
				}
				file, err := os.Open(filePath)
				if err != nil {
					return mcp.NewToolResultError("Failed to open log file: " + err.Error()), nil
				}
				defer file.Close()

				entries, err = ParseSyslog(file)
				if err != nil {
					return mcp.NewToolResultError("Failed parsing logs: " + err.Error()), nil
				}
			}

			var sevs []LogSeverity
			if severitiesStr != "" {
				parts := strings.Split(severitiesStr, ",")
				for _, p := range parts {
					trimmed := strings.TrimSpace(p)
					if trimmed != "" {
						sevs = append(sevs, LogSeverity(trimmed))
					}
				}
			}

			filter := LogFilter{
				Severities: sevs,
				Query:      query,
				Limit:      limit,
			}

			filtered := FilterEntries(entries, filter)
			data, _ := json.MarshalIndent(filtered, "", "  ")
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	// ==========================================
	// 9. File Smart Editing Tools
	// ==========================================
	w.registerTool("smart_edit_file", "Apply search-and-replace, scoped, or line replaces to a file",
		[]mcp.ToolOption{
			mcp.WithString("file_path", mcp.Required(), mcp.Description("Path to the file to edit")),
			mcp.WithString("mode", mcp.Required(), mcp.Description("Edit mode: search_replace, scoped_replace, line_replace")),
			mcp.WithString("search_block", mcp.Description("Exact text block to search for (required for search_replace and scoped_replace)")),
			mcp.WithString("replace_with", mcp.Required(), mcp.Description("Replacement text block")),
			mcp.WithNumber("start_line", mcp.Description("Optional starting line number (1-based, inclusive)")),
			mcp.WithNumber("end_line", mcp.Description("Optional ending line number (1-based, inclusive)")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			filePath, _ := req.RequireString("file_path")
			modeStr, _ := req.RequireString("mode")
			searchBlock := req.GetString("search_block", "")
			replaceWith, _ := req.RequireString("replace_with")
			startLineVal := req.GetFloat("start_line", 0)
			endLineVal := req.GetFloat("end_line", 0)

			editReq := SmartEditRequest{
				FilePath:    filePath,
				Mode:        EditMode(modeStr),
				SearchBlock: searchBlock,
				ReplaceWith: replaceWith,
				StartLine:   int(startLineVal),
				EndLine:     int(endLineVal),
			}

			res, err := ApplySmartEdit(editReq)
			if err != nil {
				return mcp.NewToolResultError("Failed smart editing file: " + err.Error()), nil
			}

			data, _ := json.MarshalIndent(res, "", "  ")
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	// ==========================================
	// 10. Timer & Hook Scheduling Tools
	// ==========================================
	w.registerTool("timer_schedule_hook", "Schedule a MultiHook string event to trigger after a delay",
		[]mcp.ToolOption{
			mcp.WithString("id", mcp.Required(), mcp.Description("Unique identifier for this timer")),
			mcp.WithString("duration", mcp.Required(), mcp.Description("Delay duration before triggering (e.g. '10s', '5m', '2h')")),
			mcp.WithString("payload", mcp.Required(), mcp.Description("String payload text to pass to the hook")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			id, _ := req.RequireString("id")
			durStr, _ := req.RequireString("duration")
			payload, _ := req.RequireString("payload")

			dur, err := time.ParseDuration(durStr)
			if err != nil {
				return mcp.NewToolResultError("Invalid duration format: " + err.Error()), nil
			}

			if err := w.TimerChain.ScheduleTimerHook(id, dur, payload); err != nil {
				return mcp.NewToolResultError("Failed to schedule timer hook: " + err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Timer hook [%s] scheduled successfully to run in %s", id, durStr)), nil
		},
	)

	w.registerTool("timer_cancel", "Cancel an active scheduled timer or recurring task by ID",
		[]mcp.ToolOption{
			mcp.WithString("id", mcp.Required(), mcp.Description("Timer or task ID to cancel")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			id, _ := req.RequireString("id")
			w.TimerChain.CancelScheduledItem(id)
			return mcp.NewToolResultText(fmt.Sprintf("Scheduled item [%s] cancelled successfully", id)), nil
		},
	)

	// ==========================================
	// 11. Database Logs & Task History Tools
	// ==========================================
	w.registerTool("query_server_logs", "Query and filter server logs from persistent database",
		[]mcp.ToolOption{
			mcp.WithString("level", mcp.Description("Log level filter (INFO, WARN, ERROR, DEBUG)")),
			mcp.WithString("component", mcp.Description("Component filter")),
			mcp.WithString("query", mcp.Description("Text search query")),
			mcp.WithNumber("limit", mcp.Description("Max log entries to return (default 50)")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if w.Store == nil {
				return mcp.NewToolResultError("Database store not initialized"), nil
			}
			level := req.GetString("level", "")
			component := req.GetString("component", "")
			qStr := req.GetString("query", "")
			limitVal := req.GetFloat("limit", 50)

			logs, err := w.Store.QueryLogs(ctx, database.LogFilter{
				Level:     level,
				Component: component,
				Query:     qStr,
				Limit:     int(limitVal),
			})
			if err != nil {
				return mcp.NewToolResultError("Failed querying logs: " + err.Error()), nil
			}
			data, _ := json.MarshalIndent(logs, "", "  ")
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	w.registerTool("query_task_history", "Query background task execution history from database",
		[]mcp.ToolOption{
			mcp.WithString("status", mcp.Description("Filter by status (Pending, Running, Completed, Failed, Cancelled)")),
			mcp.WithString("query", mcp.Description("Search in task name or command")),
			mcp.WithNumber("limit", mcp.Description("Max tasks to return (default 50)")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if w.Store == nil {
				return mcp.NewToolResultError("Database store not initialized"), nil
			}
			st := req.GetString("status", "")
			qStr := req.GetString("query", "")
			limitVal := req.GetFloat("limit", 50)

			tasks, err := w.Store.QueryTasks(ctx, database.TaskFilter{
				Status: st,
				Query:  qStr,
				Limit:  int(limitVal),
			})
			if err != nil {
				return mcp.NewToolResultError("Failed querying tasks: " + err.Error()), nil
			}
			data, _ := json.MarshalIndent(tasks, "", "  ")
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	w.registerTool("run_persistent_command", "Run command in a persistent stateful shell session",
		[]mcp.ToolOption{
			mcp.WithString("session_id", mcp.Required(), mcp.Description("Unique session ID")),
			mcp.WithString("command", mcp.Required(), mcp.Description("Command string to execute")),
			mcp.WithString("shell", mcp.Description("Shell executable (e.g. bash, sh, pwsh, cmd.exe)")),
			mcp.WithNumber("timeout_seconds", mcp.Description("Timeout duration in seconds (default 60)")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			sessID, _ := req.RequireString("session_id")
			cmdStr, _ := req.RequireString("command")
			shell := req.GetString("shell", "")
			timeoutSec := req.GetFloat("timeout_seconds", 60)

			sess, err := StartPersistentSession(sessID, shell)
			if err != nil {
				return mcp.NewToolResultError("Failed launching shell session: " + err.Error()), nil
			}

			fut := sess.ExecuteAsync(CommandOptions{
				Command: cmdStr,
				Timeout: time.Duration(timeoutSec) * time.Second,
			})
			res := <-fut

			data, _ := json.MarshalIndent(res, "", "  ")
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	// ==========================================
	// SSH Native Management Tools
	// ==========================================
	w.registerTool("ssh_execute_command", "Executes a command on a remote server natively over SSH in Go (supports password & SSH key auth)",
		[]mcp.ToolOption{
			mcp.WithString("host", mcp.Required(), mcp.Description("Remote SSH hostname or IP address")),
			mcp.WithNumber("port", mcp.Description("SSH port (default 22)")),
			mcp.WithString("user", mcp.Required(), mcp.Description("SSH username")),
			mcp.WithString("password", mcp.Description("SSH password (optional if key_path/key_content provided)")),
			mcp.WithString("key_path", mcp.Description("Path to private key file on local system")),
			mcp.WithString("key_content", mcp.Description("Raw private key PEM string")),
			mcp.WithString("passphrase", mcp.Description("Passphrase for encrypted private key")),
			mcp.WithString("command", mcp.Required(), mcp.Description("Command string to run remotely")),
			mcp.WithNumber("timeout_seconds", mcp.Description("Timeout in seconds (default 60)")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			host, _ := req.RequireString("host")
			user, _ := req.RequireString("user")
			command, _ := req.RequireString("command")

			port := int(req.GetFloat("port", 22))
			password := req.GetString("password", "")
			keyPath := req.GetString("key_path", "")
			keyContent := req.GetString("key_content", "")
			passphrase := req.GetString("passphrase", "")
			timeoutSec := req.GetFloat("timeout_seconds", 60)

			sshMgr := NewSSHClientManager()
			authOpts := SSHAuthOptions{
				Password:   password,
				KeyPath:    keyPath,
				KeyContent: keyContent,
				Passphrase: passphrase,
			}

			res, err := sshMgr.ExecuteRemoteCommand(ctx, host, port, user, authOpts, command, time.Duration(timeoutSec)*time.Second)
			if err != nil {
				return mcp.NewToolResultError("SSH execution failed: " + err.Error()), nil
			}

			data, _ := json.MarshalIndent(res, "", "  ")
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	w.registerTool("ssh_test_connection", "Test SSH network reachability and credentials for a remote host",
		[]mcp.ToolOption{
			mcp.WithString("host", mcp.Required(), mcp.Description("Remote SSH hostname or IP address")),
			mcp.WithNumber("port", mcp.Description("SSH port (default 22)")),
			mcp.WithString("user", mcp.Required(), mcp.Description("SSH username")),
			mcp.WithString("password", mcp.Description("SSH password")),
			mcp.WithString("key_path", mcp.Description("Path to private key file")),
			mcp.WithString("key_content", mcp.Description("Raw private key PEM string")),
			mcp.WithString("passphrase", mcp.Description("Passphrase for encrypted private key")),
			mcp.WithNumber("timeout_seconds", mcp.Description("Timeout in seconds (default 10)")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			host, _ := req.RequireString("host")
			user, _ := req.RequireString("user")

			port := int(req.GetFloat("port", 22))
			password := req.GetString("password", "")
			keyPath := req.GetString("key_path", "")
			keyContent := req.GetString("key_content", "")
			passphrase := req.GetString("passphrase", "")
			timeoutSec := req.GetFloat("timeout_seconds", 10)

			sshMgr := NewSSHClientManager()
			authOpts := SSHAuthOptions{
				Password:   password,
				KeyPath:    keyPath,
				KeyContent: keyContent,
				Passphrase: passphrase,
			}

			err := sshMgr.TestConnection(ctx, host, port, user, authOpts, time.Duration(timeoutSec)*time.Second)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("SSH connection test to %s:%d failed: %v", host, port, err)), nil
			}

			return mcp.NewToolResultText(fmt.Sprintf("Successfully connected to SSH host %s:%d as user '%s'.", host, port, user)), nil
		},
	)
}

func (w *MCPServerWrapper) registerTool(name string, description string, options []mcp.ToolOption, handler func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error)) {
	opts := append([]mcp.ToolOption{mcp.WithDescription(description)}, options...)
	tool := mcp.NewTool(name, opts...)
	w.MCPServer.AddTool(tool, handler)
}

// StartServer starts the GoFrame HTTP/HTTPS server serving the streamable MCP endpoint.
func (w *MCPServerWrapper) StartServer(ctx context.Context) error {
	s := g.Server()
	s.SetPort(w.Cfg.Port)

	// Auth Middleware
	s.Use(func(r *ghttp.Request) {
		if !w.Cfg.ValidateAuthRequest(r.Request) {
			r.Response.WriteStatus(http.StatusForbidden, "Forbidden: Invalid authorization path or client IP")
			return
		}
		r.Middleware.Next()
	})

	// Streamable HTTP Handler for mcp-go
	httpSrv := server.NewStreamableHTTPServer(w.MCPServer)

	basePath := w.Cfg.CustomBasePath
	if !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}

	s.BindHandler(basePath, ghttp.WrapH(httpSrv))

	if w.Cfg.Protocol == "https" {
		if err := EnsureCertificates(&w.Cfg, ""); err != nil {
			return fmt.Errorf("failed provisioning TLS certificates: %w", err)
		}
		if w.Cfg.CertFile != "" && w.Cfg.KeyFile != "" {
			s.EnableHTTPS(w.Cfg.CertFile, w.Cfg.KeyFile)
		}
	}

	if w.Store != nil {
		_ = w.Store.InsertLog(ctx, "INFO", "Server", fmt.Sprintf("MyMCP Server listening on %s://%s:%d%s", w.Cfg.Protocol, w.Cfg.Host, w.Cfg.Port, w.Cfg.CustomBasePath), "")
	}

	w.gfServer = s
	return s.Start()
}

// StopServer gracefully stops the HTTP server and background components.
func (w *MCPServerWrapper) StopServer(ctx context.Context) error {
	if w.TimerChain != nil {
		w.TimerChain.Close()
	}
	if w.TaskManager != nil {
		w.TaskManager.Stop()
	}
	if w.gfServer != nil {
		return w.gfServer.Shutdown()
	}
	return nil
}
