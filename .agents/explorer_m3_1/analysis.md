# Technical Analysis: ServerConfig & Configuration Architecture (Milestone 3)

**Author**: Explorer 1 (Milestone 3 - CLI & Config Integration)  
**Target Path**: `d:\Documents\dca\utils\server_config.go`  
**Date**: 2026-07-28  
**Working Directory**: `d:\Documents\dca\.agents\explorer_m3_1`  

---

## 1. Executive Summary

Milestone 3 extends the DCA platform to seamlessly support King, Worker, and Pair operations via CLI commands (`dca king`, `dca worker`, `dca pair`) while preserving 100% backward compatibility for standalone server execution, Windows/Linux background services, and the interactive Bubbletea TUI.

This report provides a detailed examination of `utils/server_config.go`, analyzes the required struct extensions for King and Worker modes, defines the 3-tier configuration precedence model (**CLI Flag > Environment Variable > Config File > Default Fallback**), specifies validation logic and error checks, and outlines exact change points and risk mitigations.

---

## 2. Investigation Findings & Current State Audit

### 2.1 Existing `ServerConfig` Struct Definition (`utils/server_config.go:33-49`)

```go
type ServerConfig struct {
	Host           string   `json:"host"`
	Port           int      `json:"port"`
	Protocol       string   `json:"protocol"` // "http" or "https"
	CertType       CertType `json:"cert_type"`
	Domain         string   `json:"domain"`
	CertFile       string   `json:"cert_file"`
	KeyFile        string   `json:"key_file"`
	AuthMode       AuthMode `json:"auth_mode"`
	CustomBasePath string   `json:"custom_base_path"`
	AllowedIPs     []string `json:"allowed_ips"`
	AuthToken      string   `json:"auth_token,omitempty"`

	// Database fields
	DBType       string `json:"db_type"`        // "sqlite" or "postgres"
	DBConnString string `json:"db_conn_string"` // sqlite file path or postgres connection string
}
```

### 2.2 Tag Usage & Serialization
- **Serialization Format**: Standard JSON via `"encoding/json"`.
- **Struct Tags**: Standard `json:"<name>"` tags are used across all existing fields. `AuthToken` includes `omitempty`.
- **Save Operation (`SaveToFile`)**:
  - Automatically creates parent directory structure using `os.MkdirAll(dir, 0755)`.
  - Formats output JSON with two-space indentation (`json.MarshalIndent(cfg, "", "  ")`).
  - Writes to disk with file permissions `0644`.
- **Load Operation (`LoadServerConfig`)**:
  - Reads raw JSON bytes via `os.ReadFile(filePath)`.
  - Unmarshals into a clean `ServerConfig` struct. Missing fields default to Go zero-values (`""`, `0`, `false`, `nil`).
  - Post-unmarshal default fallbacks:
    - If `DBType == ""`, defaults to `"sqlite"`.
    - If `DBConnString == ""`, defaults to `"mymcp.db"`.

### 2.3 Existing Default Values (`DefaultServerConfig()`)

| Field | Type | Default Value | Notes |
|---|---|---|---|
| `Host` | `string` | `"0.0.0.0"` | Listens on all interfaces |
| `Port` | `int` | `8080` | Default HTTP port |
| `Protocol` | `string` | `"http"` | `"http"` or `"https"` |
| `CertType` | `CertType` | `"none"` | Options: `"none"`, `"selfsigned"`, `"acme"`, `"custom"` |
| `Domain` | `string` | `"localhost"` | Standard domain |
| `AuthMode` | `AuthMode` | `"open"` | Options: `"open"`, `"custom_path"`, `"custom_path_ip"`, `"ip_only"`, `"token"`, `"custom_path_token_ip"` |
| `CustomBasePath` | `string` | `"/mcp"` | Default HTTP endpoint path |
| `AllowedIPs` | `[]string` | `[]string{}` | Empty list permits all IPs when AuthMode permits |
| `AuthToken` | `string` | `""` | Optional token for Token AuthMode |
| `DBType` | `string` | `"sqlite"` | Default embedded database |
| `DBConnString` | `string` | `"mymcp.db"` | SQLite database file path |

### 2.4 OS Default Configuration File Paths (`installer/installer.go:16-25`)
- **Windows**: `C:\ProgramData\mymcp\config.json` (evaluated via `os.Getenv("ProgramData")` with fallback to `C:\ProgramData`).
- **Linux/Unix**: `/etc/mymcp/config.json`.

---

## 3. ServerConfig King/Worker Extensions

To support King, Worker, and Pairing operations, `ServerConfig` must be extended with seven new parameters.

### 3.1 Field Specification

```go
type ServerConfig struct {
	// --- Existing Fields Preserved ---
	Host           string   `json:"host" yaml:"host"`
	Port           int      `json:"port" yaml:"port"`
	Protocol       string   `json:"protocol" yaml:"protocol"`
	CertType       CertType `json:"cert_type" yaml:"cert_type"`
	Domain         string   `json:"domain" yaml:"domain"`
	CertFile       string   `json:"cert_file" yaml:"cert_file"`
	KeyFile        string   `json:"key_file" yaml:"key_file"`
	AuthMode       AuthMode `json:"auth_mode" yaml:"auth_mode"`
	CustomBasePath string   `json:"custom_base_path" yaml:"custom_base_path"`
	AllowedIPs     []string `json:"allowed_ips" yaml:"allowed_ips"`
	AuthToken      string   `json:"auth_token,omitempty" yaml:"auth_token,omitempty"`

	DBType       string `json:"db_type" yaml:"db_type"`
	DBConnString string `json:"db_conn_string" yaml:"db_conn_string"`

	// --- Extended King / Worker Fields ---
	KingAddress string `json:"king_address,omitempty" yaml:"king_address,omitempty"`
	PairCode    string `json:"pair_code,omitempty" yaml:"pair_code,omitempty"`
	PairToken   string `json:"pair_token,omitempty" yaml:"pair_token,omitempty"`
	NodeID      string `json:"node_id,omitempty" yaml:"node_id,omitempty"`
	IngressPort int    `json:"ingress_port,omitempty" yaml:"ingress_port,omitempty"`
	WorkerMode  bool   `json:"worker_mode,omitempty" yaml:"worker_mode,omitempty"`
	KingMode    bool   `json:"king_mode,omitempty" yaml:"king_mode,omitempty"`
}
```

### 3.2 Detailed Field Definitions

1. **`KingAddress` (`string`)**:
   - **JSON / YAML Tag**: `json:"king_address,omitempty" yaml:"king_address,omitempty"`
   - **Purpose**: Target WebSocket / HTTP address of the central King server (e.g. `wss://king.example.com` or `ws://192.168.1.10:8080`). Used by Worker daemons to establish reverse tunnel connections (`/register`).
   - **Default**: `""`

2. **`PairCode` (`string`)**:
   - **JSON / YAML Tag**: `json:"pair_code,omitempty" yaml:"pair_code,omitempty"`
   - **Purpose**: Temporary 6-character uppercase alphanumeric string (`[A-Z0-9]{6}`) used during initial handshake/registration exchange between King and Worker.
   - **Default**: `""`

3. **`PairToken` (`string`)**:
   - **JSON / YAML Tag**: `json:"pair_token,omitempty" yaml:"pair_token,omitempty"`
   - **Purpose**: Persistent authentication token issued by King upon pairing completion. Sent in `Authorization: Bearer <pair_token>` headers on reverse tunnel connections.
   - **Default**: `""`

4. **`NodeID` (`string`)**:
   - **JSON / YAML Tag**: `json:"node_id,omitempty" yaml:"node_id,omitempty"`
   - **Purpose**: Unique node identification string (e.g., UUID or machine GUID). Sent in `X-Node-ID` HTTP headers during WebSocket handshake.
   - **Default**: `""`

5. **`IngressPort` (`int`)**:
   - **JSON / YAML Tag**: `json:"ingress_port,omitempty" yaml:"ingress_port,omitempty"`
   - **Purpose**: Dedicated HTTP/WSS ingress port on the King server for receiving incoming Worker reverse tunnel connections (e.g., `9090`).
   - **Default**: `0` (Falls back to `9090` when `KingMode` is `true` and `IngressPort == 0`).

6. **`WorkerMode` (`bool`)**:
   - **JSON / YAML Tag**: `json:"worker_mode,omitempty" yaml:"worker_mode,omitempty"`
   - **Purpose**: Operational mode toggle. When `true`, daemon launches `WorkerDaemon` reverse-tunnel loop instead of standalone HTTP server.
   - **Default**: `false`

7. **`KingMode` (`bool`)**:
   - **JSON / YAML Tag**: `json:"king_mode,omitempty" yaml:"king_mode,omitempty"`
   - **Purpose**: Operational mode toggle. When `true`, daemon launches `KingGateway` reverse-tunnel listener and management endpoint.
   - **Default**: `false`

---

## 4. Configuration Precedence & Environment Overrides

To provide flexible configuration across CLI, Docker, systemd, and CI environments, `ServerConfig` loading should follow a strict 4-tier hierarchy.

### 4.1 Hierarchy Matrix

$$\text{CLI Flags} > \text{Environment Variables} > \text{JSON/YAML Config File} > \text{Default ServerConfig Fallback}$$

### 4.2 Environment Variable Overrides

Environment variables prefixed with `DCA_` override values loaded from `config.json`:

| Environment Variable | Target Field | Parsing / Validation |
|---|---|---|
| `DCA_HOST` | `Host` | String |
| `DCA_PORT` | `Port` | `strconv.Atoi(v)` (must be 1..65535) |
| `DCA_PROTOCOL` | `Protocol` | String (`"http"` / `"https"`) |
| `DCA_AUTH_MODE` | `AuthMode` | `AuthMode(v)` |
| `DCA_AUTH_TOKEN` | `AuthToken` | String |
| `DCA_KING_ADDRESS` | `KingAddress` | String |
| `DCA_PAIR_CODE` | `PairCode` | String |
| `DCA_PAIR_TOKEN` | `PairToken` | String |
| `DCA_NODE_ID` | `NodeID` | String |
| `DCA_INGRESS_PORT` | `IngressPort` | `strconv.Atoi(v)` (must be 1..65535) |
| `DCA_WORKER_MODE` | `WorkerMode` | `strconv.ParseBool(v)` or `"1"`/`"true"` |
| `DCA_KING_MODE` | `KingMode` | `strconv.ParseBool(v)` or `"1"`/`"true"` |
| `DCA_DB_TYPE` | `DBType` | String (`"sqlite"` / `"postgres"`) |
| `DCA_DB_CONN_STRING` | `DBConnString` | String |

---

## 5. Configuration Validation Engine (`(cfg *ServerConfig) Validate() error`)

A comprehensive validation method should be added to `ServerConfig` to enforce semantic invariants before runtime initialization.

### 5.1 Validation Rules

1. **Mode Mutual Exclusivity**:
   - `WorkerMode` and `KingMode` CANNOT both be `true`.
   - *Error*: `fmt.Errorf("invalid configuration: WorkerMode and KingMode cannot both be true")`.

2. **King Mode Validation**:
   - If `KingMode == true`, `Port` and `IngressPort` must be valid ports (1..65535).
   - `Port` and `IngressPort` MUST NOT collide (e.g. both set to 8080).
   - *Error*: `fmt.Errorf("invalid king configuration: Port (%d) and IngressPort (%d) cannot be equal")`.

3. **Worker Mode Validation**:
   - If `WorkerMode == true` and `PairToken == ""` and `PairCode == ""`, `KingAddress` must be specified.
   - *Error*: `fmt.Errorf("invalid worker configuration: KingAddress must be specified when WorkerMode is enabled")`.

4. **Pair Code Validation**:
   - If `PairCode` is populated, it must pass `utils.ValidatePairingCode(PairCode)` (exactly 6 uppercase alphanumeric characters).
   - *Error*: `fmt.Errorf("invalid pair code: %q must be 6 alphanumeric characters", cfg.PairCode)`.

5. **Port Range Check**:
   - `Port` must be between 1 and 65535.
   - If `IngressPort != 0`, `IngressPort` must be between 1 and 65535.

6. **Authentication & Protocol Validation**:
   - `Protocol` must be `"http"` or `"https"`.
   - `AuthMode` must be one of known enum constants (`AuthModeOpen`, `AuthModeCustomPath`, `AuthModeCustomPathIP`, `AuthModeIPOnly`, `AuthModeToken`, `AuthModeCustomPathTokenIP`).

---

## 6. Proposed Code Changes (Diff Preview)

### 6.1 `utils/server_config.go` Updates

```go
// Add King and Worker fields to ServerConfig struct:
type ServerConfig struct {
	Host           string   `json:"host" yaml:"host"`
	Port           int      `json:"port" yaml:"port"`
	Protocol       string   `json:"protocol" yaml:"protocol"`
	CertType       CertType `json:"cert_type" yaml:"cert_type"`
	Domain         string   `json:"domain" yaml:"domain"`
	CertFile       string   `json:"cert_file" yaml:"cert_file"`
	KeyFile        string   `json:"key_file" yaml:"key_file"`
	AuthMode       AuthMode `json:"auth_mode" yaml:"auth_mode"`
	CustomBasePath string   `json:"custom_base_path" yaml:"custom_base_path"`
	AllowedIPs     []string `json:"allowed_ips" yaml:"allowed_ips"`
	AuthToken      string   `json:"auth_token,omitempty" yaml:"auth_token,omitempty"`

	DBType       string `json:"db_type" yaml:"db_type"`
	DBConnString string `json:"db_conn_string" yaml:"db_conn_string"`

	// King / Worker Extensions
	KingAddress string `json:"king_address,omitempty" yaml:"king_address,omitempty"`
	PairCode    string `json:"pair_code,omitempty" yaml:"pair_code,omitempty"`
	PairToken   string `json:"pair_token,omitempty" yaml:"pair_token,omitempty"`
	NodeID      string `json:"node_id,omitempty" yaml:"node_id,omitempty"`
	IngressPort int    `json:"ingress_port,omitempty" yaml:"ingress_port,omitempty"`
	WorkerMode  bool   `json:"worker_mode,omitempty" yaml:"worker_mode,omitempty"`
	KingMode    bool   `json:"king_mode,omitempty" yaml:"king_mode,omitempty"`
}

// Update DefaultServerConfig():
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Host:           "0.0.0.0",
		Port:           8080,
		Protocol:       "http",
		CertType:       CertTypeNone,
		Domain:         "localhost",
		AuthMode:       AuthModeOpen,
		CustomBasePath: "/mcp",
		AllowedIPs:     []string{},
		AuthToken:      "",
		DBType:         "sqlite",
		DBConnString:   "mymcp.db",
		KingAddress:    "",
		PairCode:       "",
		PairToken:      "",
		NodeID:         "",
		IngressPort:    0,
		WorkerMode:     false,
		KingMode:       false,
	}
}

// Update LoadServerConfig():
func LoadServerConfig(filePath string) (*ServerConfig, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	var cfg ServerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.DBType == "" {
		cfg.DBType = "sqlite"
	}
	if cfg.DBConnString == "" {
		cfg.DBConnString = "mymcp.db"
	}
	if cfg.KingMode && cfg.IngressPort == 0 {
		cfg.IngressPort = 9090
	}
	cfg.ApplyEnvOverrides()
	return &cfg, nil
}

// Add ApplyEnvOverrides():
func (cfg *ServerConfig) ApplyEnvOverrides() {
	if v := os.Getenv("DCA_HOST"); v != "" {
		cfg.Host = v
	}
	if v := os.Getenv("DCA_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Port = p
		}
	}
	if v := os.Getenv("DCA_KING_ADDRESS"); v != "" {
		cfg.KingAddress = v
	}
	if v := os.Getenv("DCA_PAIR_CODE"); v != "" {
		cfg.PairCode = v
	}
	if v := os.Getenv("DCA_PAIR_TOKEN"); v != "" {
		cfg.PairToken = v
	}
	if v := os.Getenv("DCA_NODE_ID"); v != "" {
		cfg.NodeID = v
	}
	if v := os.Getenv("DCA_INGRESS_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.IngressPort = p
		}
	}
	if v := os.Getenv("DCA_WORKER_MODE"); v != "" {
		cfg.WorkerMode = (v == "true" || v == "1")
	}
	if v := os.Getenv("DCA_KING_MODE"); v != "" {
		cfg.KingMode = (v == "true" || v == "1")
	}
}

// Add Validate():
func (cfg *ServerConfig) Validate() error {
	if cfg.WorkerMode && cfg.KingMode {
		return fmt.Errorf("invalid configuration: WorkerMode and KingMode cannot both be true")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("invalid Port: %d (must be 1-65535)", cfg.Port)
	}
	if cfg.KingMode {
		if cfg.IngressPort < 1 || cfg.IngressPort > 65535 {
			return fmt.Errorf("invalid IngressPort: %d (must be 1-65535)", cfg.IngressPort)
		}
		if cfg.Port == cfg.IngressPort {
			return fmt.Errorf("invalid king configuration: Port (%d) and IngressPort (%d) cannot be equal", cfg.Port, cfg.IngressPort)
		}
	}
	if cfg.PairCode != "" && !ValidatePairingCode(cfg.PairCode) {
		return fmt.Errorf("invalid pair code %q: must be 6 alphanumeric characters", cfg.PairCode)
	}
	return nil
}
```

---

## 7. Backwards Compatibility Guarantee

1. **Existing JSON Files**:
   - Legacy `config.json` files generated prior to Milestone 3 do NOT contain `king_address`, `worker_mode`, `king_mode`, etc.
   - Go's `json.Unmarshal` ignores missing keys and initializes them to Go zero values (`""`, `0`, `false`).
   - Standard unmarshaling of legacy JSON files results in `WorkerMode = false` and `KingMode = false`, running the server in standalone mode automatically.

2. **Existing Function Signatures**:
   - `DefaultServerConfig() ServerConfig` signature is unchanged.
   - `SaveToFile(filePath string) error` signature is unchanged.
   - `LoadServerConfig(filePath string) (*ServerConfig, error)` signature is unchanged.
   - `ValidateAuthRequest(r *http.Request) bool` signature is unchanged.

3. **Service Management Invariant**:
   - Background Windows Services (`mymcp.exe -config C:\ProgramData\mymcp\config.json`) and Linux systemd services (`ExecStart=/usr/local/bin/mymcp -config /etc/mymcp/config.json`) will continue reading `config.json` via `LoadServerConfig`.
   - If `KingMode` or `WorkerMode` is set in `config.json`, the background service daemon will start in King or Worker mode respectively. If neither is set, it executes in standalone server mode.

---

## 8. Potential Risks & Mitigation Strategies

| Risk | Impact | Mitigation Strategy |
|---|---|---|
| **Secret Leakage (`PairCode`/`PairToken`)** | High | `PairCode` is single-use and temporary. Clear `PairCode` after pairing completion before persisting `ServerConfig` to disk. `PairToken` files saved with `0644` (or `0600`). |
| **Conflicting Modes (`WorkerMode && KingMode`)** | High | Enforce strict validation error in `cfg.Validate()`. |
| **Port Collision (`Port == IngressPort`)** | High | Enforce check in `cfg.Validate()` rejecting identical `Port` and `IngressPort` when `KingMode` is active. |
| **Malformed Environment Override** | Medium | Handle string conversions gracefully in `ApplyEnvOverrides()`, falling back to file/default values if env string is invalid. |

---

## 9. Verification & Test Plan

1. **Unit Tests in `utils/server_config_test.go`**:
   - `TestServerConfig_SaveAndLoad_KingWorkerFields`: Test full roundtrip serialization/deserialization with all 7 extended fields populated.
   - `TestServerConfig_BackwardCompatibility`: Load legacy JSON payload without King/Worker fields; verify defaults (`WorkerMode=false`, `KingMode=false`).
   - `TestServerConfig_Validate`: Test validation rules (mode conflict, port range, port collision, pairing code regex).
   - `TestServerConfig_EnvOverrides`: Test `ApplyEnvOverrides()` with `DCA_` env vars.

2. **Integration Test Suite**:
   - Run `go test ./utils ./installer ./tests/e2e` to ensure all existing and new tests pass.
