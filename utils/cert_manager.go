package utils

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// GenerateSelfSignedCert creates an RSA 2048-bit self-signed TLS certificate with SAN extensions in PEM format.
func GenerateSelfSignedCert(domain string, certPath string, keyPath string) error {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("failed generating RSA private key: %w", err)
	}

	notBefore := time.Now()
	notAfter := notBefore.Add(365 * 24 * time.Hour)

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return fmt.Errorf("failed generating serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"MyMCP Server Self-Signed"},
			CommonName:   domain,
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	// Always add SAN entries (localhost, 127.0.0.1, ::1, and target domain/IP)
	template.DNSNames = append(template.DNSNames, "localhost")
	if localIP := net.ParseIP("127.0.0.1"); localIP != nil {
		template.IPAddresses = append(template.IPAddresses, localIP)
	}
	if v6IP := net.ParseIP("::1"); v6IP != nil {
		template.IPAddresses = append(template.IPAddresses, v6IP)
	}

	if ip := net.ParseIP(domain); ip != nil {
		template.IPAddresses = append(template.IPAddresses, ip)
	} else if domain != "" && domain != "localhost" {
		template.DNSNames = append(template.DNSNames, domain)
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("failed creating certificate DER: %w", err)
	}

	// Save Cert PEM
	certDir := filepath.Dir(certPath)
	if certDir != "." && certDir != "/" {
		_ = os.MkdirAll(certDir, 0755)
	}
	certOut, err := os.Create(certPath)
	if err != nil {
		return fmt.Errorf("failed creating cert file: %w", err)
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return fmt.Errorf("failed encoding cert PEM: %w", err)
	}

	// Save Key PEM
	keyDir := filepath.Dir(keyPath)
	if keyDir != "." && keyDir != "/" {
		_ = os.MkdirAll(keyDir, 0755)
	}
	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed creating key file: %w", err)
	}
	defer keyOut.Close()

	privBytes := x509.MarshalPKCS1PrivateKey(priv)
	if err := pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: privBytes}); err != nil {
		return fmt.Errorf("failed encoding key PEM: %w", err)
	}

	return nil
}

// ObtainAcmeCert uses acme.sh to obtain a TLS certificate on Linux.
func ObtainAcmeCert(domain string, email string, certPath string, keyPath string) error {
	acmeBin, err := exec.LookPath("acme.sh")
	if err != nil {
		home := os.Getenv("HOME")
		if home != "" {
			altPath := filepath.Join(home, ".acme.sh", "acme.sh")
			if _, errStat := os.Stat(altPath); errStat == nil {
				acmeBin = altPath
			}
		}
	}
	if acmeBin == "" {
		return fmt.Errorf("acme.sh command not found in PATH or ~/.acme.sh. Please install acme.sh first")
	}

	args := []string{"--issue", "-d", domain, "--standalone", "-k", "2048"}
	if email != "" {
		args = append(args, "--accountemail", email)
	}

	cmd := exec.Command(acmeBin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("acme.sh issue failed: %v\nOutput: %s", err, string(out))
	}

	certDir := filepath.Dir(certPath)
	if certDir != "." && certDir != "/" {
		_ = os.MkdirAll(certDir, 0755)
	}

	installArgs := []string{
		"--install-cert", "-d", domain,
		"--cert-file", certPath,
		"--key-file", keyPath,
	}
	installCmd := exec.Command(acmeBin, installArgs...)
	outInstall, errInstall := installCmd.CombinedOutput()
	if errInstall != nil {
		return fmt.Errorf("acme.sh install-cert failed: %v\nOutput: %s", errInstall, string(outInstall))
	}

	return nil
}

// ObtainWinAcmeCert uses win-acme (wacs.exe) to obtain a TLS certificate on Windows.
func ObtainWinAcmeCert(domain string, email string, certPath string, keyPath string) error {
	wacsBin, err := exec.LookPath("wacs.exe")
	if err != nil {
		candidate := `C:\win-acme\wacs.exe`
		if _, errStat := os.Stat(candidate); errStat == nil {
			wacsBin = candidate
		}
	}
	if wacsBin == "" {
		return fmt.Errorf("win-acme (wacs.exe) not found in PATH or C:\\win-acme\\. Please install win-acme first")
	}

	args := []string{
		"--source", "manual",
		"--host", domain,
		"--certificatestore", "My",
		"--accepttos",
	}
	if email != "" {
		args = append(args, "--emailaddress", email)
	}

	cmd := exec.Command(wacsBin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("win-acme wacs.exe execution failed: %v\nOutput: %s", err, string(out))
	}

	return nil
}

// EnsureCertificates verifies and provisions SSL certificates according to ServerConfig.
func EnsureCertificates(cfg *ServerConfig, baseDir string) error {
	if cfg.Protocol != "https" {
		return nil
	}

	if cfg.CertFile == "" {
		cfg.CertFile = filepath.Join(baseDir, "cert.pem")
	}
	if cfg.KeyFile == "" {
		cfg.KeyFile = filepath.Join(baseDir, "key.pem")
	}

	switch cfg.CertType {
	case CertTypeSelfSigned:
		return GenerateSelfSignedCert(cfg.Domain, cfg.CertFile, cfg.KeyFile)

	case CertTypeAcme:
		if runtime.GOOS == "windows" {
			err := ObtainWinAcmeCert(cfg.Domain, "", cfg.CertFile, cfg.KeyFile)
			if err != nil {
				// Fallback to self-signed if ACME client is missing on host
				return GenerateSelfSignedCert(cfg.Domain, cfg.CertFile, cfg.KeyFile)
			}
			return nil
		}
		err := ObtainAcmeCert(cfg.Domain, "", cfg.CertFile, cfg.KeyFile)
		if err != nil {
			// Fallback to self-signed if ACME client is missing on host
			return GenerateSelfSignedCert(cfg.Domain, cfg.CertFile, cfg.KeyFile)
		}
		return nil

	case CertTypeCustom:
		if _, err := os.Stat(cfg.CertFile); err != nil {
			return fmt.Errorf("custom cert file not found at %s: %w", cfg.CertFile, err)
		}
		if _, err := os.Stat(cfg.KeyFile); err != nil {
			return fmt.Errorf("custom key file not found at %s: %w", cfg.KeyFile, err)
		}
		return nil

	default:
		return nil
	}
}
