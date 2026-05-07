package notifier

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"time"
)

type Config struct {
	SMTPHost     string
	SMTPPort     string
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
}

type Service struct {
	cfg      Config
	cooldown map[string]time.Time
	mu       sync.Mutex
}

func New(cfg Config) *Service {
	if cfg.SMTPPort == "" {
		cfg.SMTPPort = "587"
	}
	if cfg.SMTPFrom == "" {
		cfg.SMTPFrom = cfg.SMTPUsername
	}
	return &Service{
		cfg:      cfg,
		cooldown: map[string]time.Time{},
	}
}

func (s *Service) Enabled() bool {
	return s.cfg.SMTPHost != "" && s.cfg.SMTPUsername != "" && s.cfg.SMTPPassword != ""
}

func (s *Service) SendOfflineAlert(to []string, machineName, machineID string, lastSeen time.Time) error {
	if !s.Enabled() || len(to) == 0 {
		return nil
	}

	// 5-minute cooldown per machine
	s.mu.Lock()
	if last, ok := s.cooldown[machineID]; ok && time.Since(last) < 5*time.Minute {
		s.mu.Unlock()
		return nil
	}
	s.cooldown[machineID] = time.Now()
	s.mu.Unlock()

	subject := fmt.Sprintf("[SSH Relay] Machine offline: %s", machineName)
	body := fmt.Sprintf("Machine %s (%s) has gone offline.\r\nLast seen: %s\r\n\r\nThis is an automated alert from SSH Relay.",
		machineName, machineID, lastSeen.Format(time.RFC3339))

	for _, recipient := range to {
		recipient = strings.TrimSpace(recipient)
		if recipient == "" {
			continue
		}
		if err := sendMail(s.cfg, recipient, subject, body); err != nil {
			return fmt.Errorf("send to %s: %w", recipient, err)
		}
	}
	return nil
}

func sendMail(cfg Config, to, subject, body string) error {
	addr := net.JoinHostPort(cfg.SMTPHost, cfg.SMTPPort)
	return SendMail(cfg.SMTPHost, addr, cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPFrom, to, subject, body)
}

func SendMail(smtpHost, addr, username, password, from, to, subject, body string) error {
	client, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: smtpHost}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if ok, _ := client.Extension("AUTH"); ok {
		if err := client.Auth(smtp.PlainAuth("", username, password, smtpHost)); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		from, to, subject, body)
	if _, err := w.Write([]byte(msg)); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}
