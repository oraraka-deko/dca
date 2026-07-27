package utils

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// MCPServerWrapper encapsulates the GoFrame HTTP server and mcp-go tools.
type MCPServerWrapper struct {
	Cfg            ServerConfig
	MCPServer      *server.MCPServer
	TaskManager    *TaskManager
	SandboxManager *VFSSandboxManager
	TimerChain     *TimerChainManager
	Hook           *MultiHook
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

	mcpSrv := server.NewMCPServer("dca-mcp-server", "1.0.0")

	wrapper := &MCPServerWrapper{
		Cfg:            cfg,
		MCPServer:      mcpSrv,
		TaskManager:    tm,
		SandboxManager: vfsMgr,
		TimerChain:     tcm,
		Hook:           hook,
	}

	wrapper.RegisterAllTools()
	return wrapper
}

// RegisterAllTools registers TaskManager, VFS Sandbox, System Monitor, and Git tools with mcp-go.
func (w *MCPServerWrapper) RegisterAllTools() {
	// 1. TaskManager Tools
	w.registerTool("submit_command", "Submit an OS shell or binary command as a background queued task",
		[]mcp.ToolOption{
			mcp.WithString("name", mcp.Required(), mcp.Description("Task descriptive name")),
			mcp.WithString("command", mcp.Required(), mcp.Description("Executable command")),
			mcp.WithString("args", mcp.Description("Space-separated command arguments")),
		},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			name, _ := req.RequireString("name")
			command, _ := req.RequireString("command")
			argsStr := req.GetString("args", "")

			var args []string
			if strings.TrimSpace(argsStr) != "" {
				args = strings.Fields(argsStr)
			}

			task, err := w.TaskManager.SubmitCommand(name, command, args...)
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

	// 2. VFS Sandbox Tools
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

	// 3. System & Monitor Tools
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

	if w.Cfg.Protocol == "https" && w.Cfg.CertFile != "" && w.Cfg.KeyFile != "" {
		s.EnableHTTPS(w.Cfg.CertFile, w.Cfg.KeyFile)
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
