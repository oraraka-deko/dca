# MyMCP Server 🚀

**MyMCP Server** is an automated, secure Model Context Protocol (MCP) server designed to give AI Agents full management capabilities over VPS servers and local environments.

---

## ✨ Features

- **Queue-based Task Manager**: Run background shell commands and long-running jobs with worker concurrency limits and progress tracking.
- **Resource Protection Watchdog**: Automatic timeout monitor that cancels runaway tasks before server resources explode.
- **Enhanced VFS Sandboxes**: Create isolated virtual workspace sandboxes (`MemFs`, `BasePathFs`, `CopyOnWriteFs`) with built-in search & replace, batch operations, and file editing.
- **Security & Authorization**:
  - Flexible auth modes: `open`, `custom_path`, `custom_path_ip`, and `ip_only` (CIDR IP whitelist support).
  - HTTPS support with auto-generated self-signed certificates or automated `acme.sh` Let's Encrypt certificates.
- **System Monitoring**: Inspect real-time CPU, memory, uptime, process lists, network status, and syslog entries.
- **Background Service Manager**: Native Linux `systemd` and Windows Service support.

---

## 📦 Quick Start

### 1. Interactive Manager (TUI)

Running the binary without arguments in any interactive terminal (on Windows or Linux/Unix) launches the built-in **Terminal User Interface (TUI)** manager:

```bash
./mymcp
```

Through the TUI, you can:
- View current service status.
- Start, stop, or restart the background service.
- Run the interactive **Setup Wizard** to configure the host, port, protocol (HTTP/HTTPS), certificates, custom base path, and IP whitelists.
- Install or uninstall the service.

When you install the service, the installer automatically copies and renames the executable to a system-wide path (`/usr/local/bin/mymcp` on Unix/Linux, `C:\ProgramData\mymcp\mymcp.exe` on Windows) and adds it to your environment `PATH` (on Windows). Once installed, you can run the `mymcp` command from any folder in your terminal!

### 2. CLI Administration

If you prefer to administer the service directly from the command line, you can use the following commands:

```bash
# Start background service
mymcp start

# Stop background service
mymcp stop

# Restart background service
mymcp restart

# Check service status details
mymcp status

# Uninstall background service
mymcp uninstall
```

### 3. Run Directly in Foreground

If you want to run the server directly in the foreground without registering it as a background service:

```bash
mymcp -config /path/to/config.json
```

---

## 🛠️ MCP Endpoint Configuration

By default, MyMCP Server listens on `http://0.0.0.0:8080/mcp`. You can connect any MCP-compatible client (e.g. Claude Desktop, Antigravity, or custom agents) using the HTTP streamable endpoint:

```text
http://<your-server-ip>:8080/mcp
```

If custom secret path authorization is configured:
```text
http://<your-server-ip>:8080/secret-token-xyz/mcp
```

---

## 🔨 Building from Source

Requirements: Go 1.22+

```bash
# Clone repository
git clone https://github.com/oraraka-deko/dca.git
cd dca

# Run tests
make test

# Build executable
make build

# Cross-compile release packages
make package
```

---

## 📄 License

MIT License.
