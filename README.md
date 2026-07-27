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

### 1. Download & Installation

Download the binary for your platform from [Releases](https://github.com/oraraka-deko/dca/releases) and run:

```bash
# Install as a system background service
./mymcp install

# Check service status
./mymcp status

# Manage background service
./mymcp start
./mymcp stop
./mymcp restart
./mymcp uninstall
```

### 2. Run Directly in Foreground

```bash
./mymcp -config /path/to/config.json
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
