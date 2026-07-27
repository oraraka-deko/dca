package installer

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"dca/utils"
	tea "charm.land/bubbletea/v2"
)

type tuiState int

const (
	stateMenu tuiState = iota
	stateStatus
	stateSetupHost
	stateSetupPort
	stateSetupProtocol
	stateSetupCertType
	stateSetupDomain
	stateSetupBasePath
	stateSetupAuthMode
	stateSetupAllowedIPs
	stateSetupConfirm
	stateInstalling
	stateUninstalling
	stateMessage
	stateRunningForeground
)

type actionResultMsg struct {
	err  error
	info string
}

type statusResultMsg struct {
	status string
	err    error
}

type startSrvResultMsg struct {
	wrapper *utils.MCPServerWrapper
	cancel  context.CancelFunc
	err     error
}

type tuiModel struct {
	state        tuiState
	menuIndex    int
	inputBuffer  string
	errorMessage string
	infoMessage  string

	// Service status
	serviceStatusRaw string
	serviceStatusTag string // "RUNNING", "STOPPED", "NOT INSTALLED", "UNKNOWN"

	// Temporary configuration values
	setupHost           string
	setupPort           string
	setupProtocol       string
	setupCertType       string
	setupDomain         string
	setupBasePath       string
	setupAuthMode       string
	setupAllowedIPs     string

	// Choices indices for option selectors
	protocolIndex int
	certTypeIndex int
	authModeIndex int

	// Foreground server wrapper variables
	foregroundSrv    *utils.MCPServerWrapper
	foregroundCancel context.CancelFunc
}

var protocolOptions = []string{"http", "https"}
var certTypeOptions = []string{"selfsigned", "acme", "custom", "none"}
var authModeOptions = []string{"open", "custom_path", "custom_path_ip", "ip_only"}

func (m tuiModel) Init() tea.Cmd {
	return checkStatusCmd()
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Universal exit
		if msg.String() == "ctrl+c" {
			// Stop foreground server if running
			if m.foregroundCancel != nil {
				m.foregroundCancel()
			}
			return m, tea.Quit
		}

		switch m.state {
		case stateMenu:
			return m.updateMenu(msg)
		case stateStatus, stateMessage:
			if msg.Key().Code == tea.KeyEnter || msg.Key().Code == tea.KeyEsc {
				m.state = stateMenu
				return m, checkStatusCmd()
			}
		case stateRunningForeground:
			if msg.Key().Code == tea.KeyEnter || msg.Key().Code == tea.KeyEsc {
				srv := m.foregroundSrv
				cancel := m.foregroundCancel
				m.foregroundSrv = nil
				m.foregroundCancel = nil
				m.state = stateInstalling
				m.infoMessage = "Stopping foreground server..."
				m.errorMessage = ""
				return m, stopForegroundServerCmd(srv, cancel)
			}
		case stateSetupHost, stateSetupPort, stateSetupProtocol, stateSetupCertType, stateSetupDomain, stateSetupBasePath, stateSetupAuthMode, stateSetupAllowedIPs:
			return m.updateSetupWizard(msg)
		case stateSetupConfirm:
			return m.updateConfirmScreen(msg)
		}

	case statusResultMsg:
		m.serviceStatusRaw = msg.status
		m.serviceStatusTag = parseServiceStatus(msg.status)

	case actionResultMsg:
		m.state = stateMessage
		if msg.err != nil {
			m.errorMessage = fmt.Sprintf("Error: %v (%s)", msg.err, msg.info)
			m.infoMessage = ""
		} else {
			m.errorMessage = ""
			m.infoMessage = msg.info
		}

	case startSrvResultMsg:
		if msg.err != nil {
			m.state = stateMessage
			m.errorMessage = fmt.Sprintf("Failed to start foreground server: %v", msg.err)
			m.infoMessage = ""
		} else {
			m.foregroundSrv = msg.wrapper
			m.foregroundCancel = msg.cancel
			m.state = stateRunningForeground
		}
	}

	return m, nil
}

func (m tuiModel) updateMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Key().Code {
	case tea.KeyUp:
		m.menuIndex--
		if m.menuIndex < 0 {
			m.menuIndex = 7
		}
	case tea.KeyDown:
		m.menuIndex++
		if m.menuIndex > 7 {
			m.menuIndex = 0
		}
	case tea.KeyEnter:
		return m.handleMenuSelect()
	default:
		// Also allow key numbers 1-8
		s := msg.String()
		if len(s) == 1 && s[0] >= '1' && s[0] <= '8' {
			m.menuIndex = int(s[0] - '1')
			return m.handleMenuSelect()
		}
	}
	return m, nil
}

func (m *tuiModel) startSetupWizard() {
	m.setupHost = "0.0.0.0"
	m.setupPort = "8080"
	m.setupProtocol = "http"
	m.setupCertType = "none"
	m.setupDomain = "localhost"
	m.setupBasePath = "/mcp"
	m.setupAuthMode = "open"
	m.setupAllowedIPs = ""

	m.protocolIndex = 0
	m.certTypeIndex = 3
	m.authModeIndex = 0

	m.inputBuffer = m.setupHost
	m.state = stateSetupHost
	m.errorMessage = ""
}

func (m tuiModel) handleMenuSelect() (tea.Model, tea.Cmd) {
	switch m.menuIndex {
	case 0: // View Status Details
		m.state = stateStatus
		return m, checkStatusCmd()
	case 1: // Start Service
		m.infoMessage = "Starting service..."
		m.errorMessage = ""
		return m, startServiceCmd()
	case 2: // Stop Service
		m.infoMessage = "Stopping service..."
		m.errorMessage = ""
		return m, stopServiceCmd()
	case 3: // Restart Service
		m.infoMessage = "Restarting service..."
		m.errorMessage = ""
		return m, restartServiceCmd()
	case 4: // Run Server (Foreground Mode)
		cPath := GetDefaultConfigPath()
		cfg, err := utils.LoadServerConfig(cPath)
		if err != nil {
			defaultCfg := utils.DefaultServerConfig()
			cfg = &defaultCfg
		}
		m.state = stateInstalling
		m.infoMessage = "Starting foreground server..."
		m.errorMessage = ""
		return m, startForegroundServerCmd(*cfg)
	case 5: // Interactive Setup
		m.startSetupWizard()
		return m, nil
	case 6: // Uninstall Service
		m.state = stateUninstalling
		return m, uninstallServiceCmd()
	case 7: // Exit
		return m, tea.Quit
	}
	return m, nil
}

func (m tuiModel) updateSetupWizard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Key().Code == tea.KeyEsc {
		m.state = stateMenu
		return m, checkStatusCmd()
	}

	isSelector := m.state == stateSetupProtocol || m.state == stateSetupCertType || m.state == stateSetupAuthMode

	if isSelector {
		switch msg.Key().Code {
		case tea.KeyLeft:
			m.decrementSelector()
			return m, nil
		case tea.KeyRight, tea.KeyTab:
			m.incrementSelector()
			return m, nil
		}
	} else {
		switch msg.Key().Code {
		case tea.KeyBackspace, tea.KeyDelete:
			if len(m.inputBuffer) > 0 {
				m.inputBuffer = m.inputBuffer[:len(m.inputBuffer)-1]
			}
			return m, nil
		case tea.KeySpace:
			m.inputBuffer += " "
			return m, nil
		default:
			txt := msg.Key().Text
			if txt != "" {
				m.inputBuffer += txt
				return m, nil
			}
		}
	}

	if msg.Key().Code == tea.KeyEnter {
		m.errorMessage = ""
		switch m.state {
		case stateSetupHost:
			m.setupHost = strings.TrimSpace(m.inputBuffer)
			m.inputBuffer = m.setupPort
			m.state = stateSetupPort

		case stateSetupPort:
			portStr := strings.TrimSpace(m.inputBuffer)
			p, err := strconv.Atoi(portStr)
			if err != nil || p < 1 || p > 65535 {
				m.errorMessage = "Port must be a valid number between 1 and 65535."
				return m, nil
			}
			m.setupPort = portStr
			m.state = stateSetupProtocol

		case stateSetupProtocol:
			m.setupProtocol = protocolOptions[m.protocolIndex]
			if m.setupProtocol == "https" {
				m.certTypeIndex = 0
				m.setupCertType = certTypeOptions[m.certTypeIndex]
				m.state = stateSetupCertType
			} else {
				m.setupCertType = "none"
				m.state = stateSetupAuthMode
			}

		case stateSetupCertType:
			m.setupCertType = certTypeOptions[m.certTypeIndex]
			if m.setupCertType == "selfsigned" || m.setupCertType == "acme" {
				m.inputBuffer = m.setupDomain
				m.state = stateSetupDomain
			} else {
				m.state = stateSetupAuthMode
			}

		case stateSetupDomain:
			dom := strings.TrimSpace(m.inputBuffer)
			if dom == "" {
				m.errorMessage = "Domain is required for SSL certification."
				return m, nil
			}
			m.setupDomain = dom
			m.state = stateSetupAuthMode

		case stateSetupAuthMode:
			m.setupAuthMode = authModeOptions[m.authModeIndex]
			m.inputBuffer = m.setupBasePath
			m.state = stateSetupBasePath

		case stateSetupBasePath:
			bp := strings.TrimSpace(m.inputBuffer)
			if bp == "" || !strings.HasPrefix(bp, "/") {
				m.errorMessage = "Base path must start with / (e.g. /mcp)."
				return m, nil
			}
			m.setupBasePath = bp
			if m.setupAuthMode == "custom_path_ip" || m.setupAuthMode == "ip_only" {
				m.inputBuffer = m.setupAllowedIPs
				m.state = stateSetupAllowedIPs
			} else {
				m.state = stateSetupConfirm
			}

		case stateSetupAllowedIPs:
			ips := strings.TrimSpace(m.inputBuffer)
			if m.setupAuthMode == "ip_only" && ips == "" {
				m.errorMessage = "Allowed IPs are required for IP_ONLY authentication."
				return m, nil
			}
			m.setupAllowedIPs = ips
			m.state = stateSetupConfirm
		}
	}

	return m, nil
}

func (m *tuiModel) incrementSelector() {
	switch m.state {
	case stateSetupProtocol:
		m.protocolIndex = (m.protocolIndex + 1) % len(protocolOptions)
	case stateSetupCertType:
		m.certTypeIndex = (m.certTypeIndex + 1) % len(certTypeOptions)
	case stateSetupAuthMode:
		m.authModeIndex = (m.authModeIndex + 1) % len(authModeOptions)
	}
}

func (m *tuiModel) decrementSelector() {
	switch m.state {
	case stateSetupProtocol:
		m.protocolIndex = (m.protocolIndex - 1 + len(protocolOptions)) % len(protocolOptions)
	case stateSetupCertType:
		m.certTypeIndex = (m.certTypeIndex - 1 + len(certTypeOptions)) % len(certTypeOptions)
	case stateSetupAuthMode:
		m.authModeIndex = (m.authModeIndex - 1 + len(authModeOptions)) % len(authModeOptions)
	}
}

func (m tuiModel) updateConfirmScreen(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Key().Code == tea.KeyEsc {
		m.state = stateMenu
		return m, checkStatusCmd()
	}

	choice := strings.ToLower(msg.String())
	if choice == "y" || msg.Key().Code == tea.KeyEnter {
		m.state = stateInstalling
		portVal, _ := strconv.Atoi(m.setupPort)
		cfg := utils.ServerConfig{
			Host:           m.setupHost,
			Port:           portVal,
			Protocol:       m.setupProtocol,
			CertType:       utils.CertType(m.setupCertType),
			Domain:         m.setupDomain,
			AuthMode:       utils.AuthMode(m.setupAuthMode),
			CustomBasePath: m.setupBasePath,
			AllowedIPs:     parseIPList(m.setupAllowedIPs),
		}

		if cfg.CertType == utils.CertTypeSelfSigned {
			cfg.CertFile = filepath.Join(filepath.Dir(GetDefaultConfigPath()), "cert.pem")
			cfg.KeyFile = filepath.Join(filepath.Dir(GetDefaultConfigPath()), "key.pem")
			_ = utils.GenerateSelfSignedCert(cfg.Domain, cfg.CertFile, cfg.KeyFile)
		}

		return m, installServiceCmd(cfg)
	} else if choice == "n" {
		m.state = stateMenu
		return m, checkStatusCmd()
	}

	return m, nil
}

func parseIPList(ipStr string) []string {
	if ipStr == "" {
		return []string{}
	}
	parts := strings.Split(ipStr, ",")
	var list []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			list = append(list, trimmed)
		}
	}
	return list
}

func (m tuiModel) View() tea.View {
	var sb strings.Builder

	sb.WriteString("\x1b[1;36m")
	sb.WriteString("╔══════════════════════════════════════════════════════════════╗\n")
	sb.WriteString("║                     MyMCP Server Manager                     ║\n")
	sb.WriteString("╚══════════════════════════════════════════════════════════════╝\n")
	sb.WriteString("\x1b[0m")

	switch m.state {
	case stateMenu:
		sb.WriteString(" Service Status: ")
		switch m.serviceStatusTag {
		case "RUNNING":
			sb.WriteString("\x1b[1;32m● RUNNING\x1b[0m")
		case "STOPPED":
			sb.WriteString("\x1b[1;33m○ STOPPED\x1b[0m")
		case "NOT INSTALLED":
			sb.WriteString("\x1b[1;90m◌ NOT INSTALLED\x1b[0m")
		default:
			sb.WriteString("\x1b[1;31m? UNKNOWN\x1b[0m")
		}
		sb.WriteString("\n\n")

		options := []string{
			"View Service Status Details",
			"Start Background Service",
			"Stop Background Service",
			"Restart Background Service",
			"Run Server (Foreground Mode)",
			"Interactive Configuration & Setup",
			"Uninstall Background Service",
			"Exit",
		}

		for i, opt := range options {
			if i == m.menuIndex {
				sb.WriteString(fmt.Sprintf(" \x1b[1;36m▶  %d. %s\x1b[0m\n", i+1, opt))
			} else {
				sb.WriteString(fmt.Sprintf("    %d. %s\n", i+1, opt))
			}
		}
		sb.WriteString("\n\x1b[90m(Use Arrow keys or 1-8 to select, Enter to run)\x1b[0m\n")

	case stateStatus:
		sb.WriteString(" \x1b[1;36m--- Service Status & Output ---\x1b[0m\n\n")
		if m.serviceStatusRaw != "" {
			sb.WriteString(m.serviceStatusRaw)
		} else {
			sb.WriteString("Fetching status...\n")
		}
		sb.WriteString("\n\n\x1b[90mPress Enter to return to main menu...\x1b[0m\n")

	case stateSetupHost, stateSetupPort, stateSetupProtocol, stateSetupCertType, stateSetupDomain, stateSetupBasePath, stateSetupAuthMode, stateSetupAllowedIPs:
		sb.WriteString(" \x1b[1;33m--- Interactive Setup Wizard ---\x1b[0m\n\n")
		m.renderWizardStep(&sb)

		if m.errorMessage != "" {
			sb.WriteString(fmt.Sprintf("\n\x1b[1;31m%s\x1b[0m\n", m.errorMessage))
		}

		sb.WriteString("\n\x1b[90m(Press Enter to continue, Esc to return to main menu)\x1b[0m\n")

	case stateSetupConfirm:
		sb.WriteString(" \x1b[1;33m--- Confirm Configuration ---\x1b[0m\n\n")
		sb.WriteString(fmt.Sprintf("  Host:           %s\n", m.setupHost))
		sb.WriteString(fmt.Sprintf("  Port:           %s\n", m.setupPort))
		sb.WriteString(fmt.Sprintf("  Protocol:       %s\n", m.setupProtocol))
		sb.WriteString(fmt.Sprintf("  Cert Type:      %s\n", m.setupCertType))
		if m.setupCertType == "selfsigned" || m.setupCertType == "acme" {
			sb.WriteString(fmt.Sprintf("  SSL Domain:     %s\n", m.setupDomain))
		}
		sb.WriteString(fmt.Sprintf("  Auth Mode:      %s\n", m.setupAuthMode))
		sb.WriteString(fmt.Sprintf("  Base Path:      %s\n", m.setupBasePath))
		if m.setupAllowedIPs != "" {
			sb.WriteString(fmt.Sprintf("  Allowed IPs:    %s\n", m.setupAllowedIPs))
		}
		sb.WriteString("\n  \x1b[1;36mDo you want to write config and install this service? [Y/n]: \x1b[0m")

	case stateInstalling:
		if m.infoMessage != "" {
			sb.WriteString(fmt.Sprintf("\n \x1b[1;36m%s\x1b[0m\n", m.infoMessage))
		} else {
			sb.WriteString("\n \x1b[1;36mInstalling service, copying executable, and setting up PATH...\x1b[0m\n")
		}
		sb.WriteString(" Please wait, this may take a moment...\n")

	case stateUninstalling:
		sb.WriteString("\n \x1b[1;31mUninstalling and removing service...\x1b[0m\n")
		sb.WriteString(" Please wait...\n")

	case stateMessage:
		sb.WriteString(" \x1b[1;36m--- Action Outcome ---\x1b[0m\n\n")
		if m.errorMessage != "" {
			sb.WriteString(fmt.Sprintf("  \x1b[1;31m%s\x1b[0m\n", m.errorMessage))
		} else {
			sb.WriteString(fmt.Sprintf("  \x1b[1;32m%s\x1b[0m\n", m.infoMessage))
		}
		sb.WriteString("\n\n\x1b[90mPress Enter to return to main menu...\x1b[0m\n")

	case stateRunningForeground:
		sb.WriteString(" \x1b[1;32m● RUNNING (Foreground Mode)\x1b[0m\n\n")
		
		host := "0.0.0.0"
		port := 8080
		proto := "http"
		path := "/mcp"
		if m.foregroundSrv != nil {
			host = m.foregroundSrv.Cfg.Host
			port = m.foregroundSrv.Cfg.Port
			proto = m.foregroundSrv.Cfg.Protocol
			path = m.foregroundSrv.Cfg.CustomBasePath
		}
		
		sb.WriteString("  Server is actively listening at:\n")
		sb.WriteString(fmt.Sprintf("  \x1b[1;36m%s://%s:%d%s\x1b[0m\n\n", proto, host, port, path))
		sb.WriteString("  AI agents and clients can connect to this endpoint now.\n")
		sb.WriteString("  Ensure this terminal remains open.\n\n")
		sb.WriteString("\x1b[90mPress Esc or Enter to stop the server and return to menu...\x1b[0m\n")
	}

	return tea.NewView(sb.String())
}

func (m tuiModel) renderWizardStep(sb *strings.Builder) {
	switch m.state {
	case stateSetupHost:
		sb.WriteString("  [Step 1 of 8] Listen Host address:\n")
		sb.WriteString("  Defines which interfaces the server binds to.\n\n")
		sb.WriteString(fmt.Sprintf("  Host: \x1b[1m%s\x1b[0m█\n", m.inputBuffer))
		sb.WriteString("  (Press Enter for default: 0.0.0.0)\n")

	case stateSetupPort:
		sb.WriteString("  [Step 2 of 8] Port number:\n")
		sb.WriteString("  Defines which port the server listens on.\n\n")
		sb.WriteString(fmt.Sprintf("  Port: \x1b[1m%s\x1b[0m█\n", m.inputBuffer))

	case stateSetupProtocol:
		sb.WriteString("  [Step 3 of 8] Protocol Mode:\n")
		sb.WriteString("  HTTP for normal connection, HTTPS for secure SSL tunnel.\n\n")
		sb.WriteString("  Protocol: ")
		for i, opt := range protocolOptions {
			if i == m.protocolIndex {
				sb.WriteString(fmt.Sprintf("\x1b[1;36m< %s >\x1b[0m  ", opt))
			} else {
				sb.WriteString(fmt.Sprintf("  %s    ", opt))
			}
		}
		sb.WriteString("\n\n  (Use Left/Right arrows or Tab to select, Enter to confirm)\n")

	case stateSetupCertType:
		sb.WriteString("  [Step 4 of 8] SSL Certification Type:\n")
		sb.WriteString("  selfsigned: Auto-generate locally trusted cert.\n")
		sb.WriteString("  acme: Use Let's Encrypt automated cert.\n")
		sb.WriteString("  custom: Supply own SSL cert files.\n\n")
		sb.WriteString("  Cert Type: ")
		for i, opt := range certTypeOptions {
			if i == m.certTypeIndex {
				sb.WriteString(fmt.Sprintf("\x1b[1;36m< %s >\x1b[0m  ", opt))
			} else {
				sb.WriteString(fmt.Sprintf("  %s    ", opt))
			}
		}
		sb.WriteString("\n\n  (Use Left/Right arrows or Tab to select, Enter to confirm)\n")

	case stateSetupDomain:
		sb.WriteString("  [Step 5 of 8] SSL Domain Name:\n")
		sb.WriteString("  Domain used for certificate registration or hosting.\n\n")
		sb.WriteString(fmt.Sprintf("  Domain: \x1b[1m%s\x1b[0m█\n", m.inputBuffer))

	case stateSetupAuthMode:
		sb.WriteString("  [Step 6 of 8] Authentication Mode:\n")
		sb.WriteString("  open: No restrictions (not recommended for public VPS).\n")
		sb.WriteString("  custom_path: Server requires secret subfolder path.\n")
		sb.WriteString("  custom_path_ip: Subfolder path AND IP whitelist check.\n")
		sb.WriteString("  ip_only: Restricts access only to specific client IP addresses.\n\n")
		sb.WriteString("  Auth Mode: ")
		for i, opt := range authModeOptions {
			if i == m.authModeIndex {
				sb.WriteString(fmt.Sprintf("\x1b[1;36m< %s >\x1b[0m  ", opt))
			} else {
				sb.WriteString(fmt.Sprintf("  %s    ", opt))
			}
		}
		sb.WriteString("\n\n  (Use Left/Right arrows or Tab to select, Enter to confirm)\n")

	case stateSetupBasePath:
		sb.WriteString("  [Step 7 of 8] MCP Endpoint Base Path:\n")
		sb.WriteString("  Must start with a slash. If custom auth path selected, this will be your secret route.\n\n")
		sb.WriteString(fmt.Sprintf("  Base Path: \x1b[1m%s\x1b[0m█\n", m.inputBuffer))

	case stateSetupAllowedIPs:
		sb.WriteString("  [Step 8 of 8] Allowed IPs whitelist:\n")
		sb.WriteString("  Comma-separated list of IP addresses or CIDR blocks (e.g. 192.168.1.100, 10.0.0.0/24).\n\n")
		sb.WriteString(fmt.Sprintf("  Allowed IPs: \x1b[1m%s\x1b[0m█\n", m.inputBuffer))
	}
}

func parseServiceStatus(raw string) string {
	lower := strings.ToLower(raw)
	if strings.Contains(lower, "running") || strings.Contains(lower, "active (running)") {
		return "RUNNING"
	}
	if strings.Contains(lower, "stopped") || strings.Contains(lower, "inactive (dead)") || strings.Contains(lower, "1062") {
		return "STOPPED"
	}
	if strings.Contains(lower, "1060") || strings.Contains(lower, "does not exist") || strings.Contains(lower, "could not be found") || strings.Contains(lower, "not-found") {
		return "NOT INSTALLED"
	}
	return "UNKNOWN"
}

func checkStatusCmd() tea.Cmd {
	return func() tea.Msg {
		stat, _ := GetServiceStatus()
		return statusResultMsg{status: stat}
	}
}

func startServiceCmd() tea.Cmd {
	return func() tea.Msg {
		err := StartService()
		return actionResultMsg{err: err, info: "Service started successfully."}
	}
}

func stopServiceCmd() tea.Cmd {
	return func() tea.Msg {
		err := StopService()
		return actionResultMsg{err: err, info: "Service stopped successfully."}
	}
}

func restartServiceCmd() tea.Cmd {
	return func() tea.Msg {
		err := RestartService()
		return actionResultMsg{err: err, info: "Service restarted successfully."}
	}
}

func installServiceCmd(cfg utils.ServerConfig) tea.Cmd {
	return func() tea.Msg {
		err := InstallService(cfg, "")
		return actionResultMsg{err: err, info: "Service installed, binary copied, and PATH registered successfully."}
	}
}

func uninstallServiceCmd() tea.Cmd {
	return func() tea.Msg {
		err := UninstallService()
		return actionResultMsg{err: err, info: "Service uninstalled and binaries removed successfully."}
	}
}

func startForegroundServerCmd(cfg utils.ServerConfig) tea.Cmd {
	return func() tea.Msg {
		wrapper := utils.NewMCPServerWrapper(cfg)
		ctx, cancel := context.WithCancel(context.Background())
		err := wrapper.StartServer(ctx)
		return startSrvResultMsg{wrapper: wrapper, cancel: cancel, err: err}
	}
}

func stopForegroundServerCmd(wrapper *utils.MCPServerWrapper, cancel context.CancelFunc) tea.Cmd {
	return func() tea.Msg {
		if wrapper != nil {
			_ = wrapper.StopServer(context.Background())
		}
		if cancel != nil {
			cancel()
		}
		return actionResultMsg{info: "Foreground server stopped successfully."}
	}
}

// RunTUI launches the Bubbletea interactive manager
func RunTUI() error {
	p := tea.NewProgram(tuiModel{
		state:            stateMenu,
		menuIndex:        0,
		serviceStatusTag: "UNKNOWN",
	})
	_, err := p.Run()
	return err
}
