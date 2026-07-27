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
	"time"
)

// GenerateSelfSignedCert creates an RSA self-signed TLS certificate and private key in PEM format.
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

	if ip := net.ParseIP(domain); ip != nil {
		template.IPAddresses = append(template.IPAddresses, ip)
	} else if domain != "" {
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

// ObtainAcmeCert uses acme.sh to obtain a TLS certificate for the domain.
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

	// 1. Issue cert via standalone on port 80
	args := []string{"--issue", "-d", domain, "--standalone", "-k", "2048"}
	if email != "" {
		args = append(args, "--accountemail", email)
	}

	cmd := exec.Command(acmeBin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("acme.sh issue failed: %v\nOutput: %s", err, string(out))
	}

	// 2. Install cert files
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
