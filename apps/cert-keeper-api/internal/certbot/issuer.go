package certbot

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// IssueResult holds the output of a certbot issuance.
type IssueResult struct {
	CertPEM   string
	KeyPEM    string
	ExpiresAt time.Time
}

// Issue runs certbot certonly --webroot to obtain a certificate for domain.
func Issue(ctx context.Context, domain, email, webroot string) (IssueResult, error) {
	certDir := filepath.Join(os.TempDir(), "ck-certbot-"+domain)
	os.RemoveAll(certDir)

	// Ensure ACME challenge directory
	acmeDir := filepath.Join(webroot, ".well-known", "acme-challenge")
	os.MkdirAll(acmeDir, 0755)

	cmd := exec.CommandContext(ctx,
		"certbot", "certonly",
		"--non-interactive",
		"--agree-tos",
		"--email", email,
		"-d", domain,
		"--webroot",
		"--webroot-path", webroot,
		"--cert-name", domain,
		"--config-dir", certDir,
		"--work-dir", filepath.Join(certDir, "work"),
		"--logs-dir", filepath.Join(certDir, "logs"),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(certDir)
		return IssueResult{}, fmt.Errorf("certbot failed: %s", truncateError(string(output)))
	}

	result, err := readCertFiles(certDir, domain)
	if err != nil {
		return IssueResult{}, err
	}
	os.RemoveAll(certDir)
	return result, nil
}

// Renew runs certbot certonly --force-renewal.
func Renew(ctx context.Context, domain, email, webroot string) (IssueResult, error) {
	certDir := filepath.Join(os.TempDir(), "ck-certbot-"+domain)
	os.RemoveAll(certDir)

	acmeDir := filepath.Join(webroot, ".well-known", "acme-challenge")
	os.MkdirAll(acmeDir, 0755)

	cmd := exec.CommandContext(ctx,
		"certbot", "certonly",
		"--non-interactive",
		"--agree-tos",
		"--email", email,
		"-d", domain,
		"--webroot",
		"--webroot-path", webroot,
		"--cert-name", domain,
		"--force-renewal",
		"--config-dir", certDir,
		"--work-dir", filepath.Join(certDir, "work"),
		"--logs-dir", filepath.Join(certDir, "logs"),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(certDir)
		return IssueResult{}, fmt.Errorf("certbot renew failed: %s", truncateError(string(output)))
	}

	result, err := readCertFiles(certDir, domain)
	if err != nil {
		return IssueResult{}, err
	}
	os.RemoveAll(certDir)
	return result, nil
}

func readCertFiles(certDir, domain string) (IssueResult, error) {
	certPath := filepath.Join(certDir, "live", domain, "fullchain.pem")
	keyPath := filepath.Join(certDir, "live", domain, "privkey.pem")

	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		os.RemoveAll(certDir)
		return IssueResult{}, fmt.Errorf("读取签发证书失败")
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		os.RemoveAll(certDir)
		return IssueResult{}, fmt.Errorf("读取签发私钥失败")
	}

	var expiresAt time.Time
	if block, _ := pem.Decode(certPEM); block != nil {
		if cert, err := x509.ParseCertificate(block.Bytes); err == nil && !cert.NotAfter.IsZero() {
			expiresAt = cert.NotAfter.UTC()
		}
	}

	return IssueResult{
		CertPEM:   string(certPEM),
		KeyPEM:    string(keyPEM),
		ExpiresAt: expiresAt,
	}, nil
}

func truncateError(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 500 {
		return s[:500] + "..."
	}
	return s
}
