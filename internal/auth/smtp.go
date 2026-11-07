package auth

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/alternayte/auth-all/email"
)

// NewSMTPSender returns the sender behind self-service password reset. On port
// 465 it speaks TLS from the first byte. On any other port net/smtp uses
// STARTTLS when the server offers it, and refuses to send auth in the clear.
// from may carry a display name, such as "Skael <skael@example.com>".
func NewSMTPSender(host string, port int, username, password, from string) email.Sender {
	return email.SenderFunc(func(ctx context.Context, m email.Message) error {
		return sendSMTP(ctx, host, port, username, password, from, m.To, subjectFor(m.Intent), m.URL)
	})
}

// SendTestSMTP sends one message to to, so an owner can check the settings.
func SendTestSMTP(ctx context.Context, host string, port int, username, password, from, to string) error {
	return sendSMTP(ctx, host, port, username, password, from, to, "Skael test email",
		"This message confirms that Skael can send email with these settings.")
}

func sendSMTP(ctx context.Context, host string, port int, username, password, from, to, subject, text string) error {
	if strings.ContainsAny(to, "\r\n") || strings.ContainsAny(from, "\r\n") {
		return fmt.Errorf("auth.smtp: invalid address")
	}
	sender, err := mail.ParseAddress(from)
	if err != nil {
		return fmt.Errorf("auth.smtp: the from address %q is not an email address", from)
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	body := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n",
		sender.String(), to, subject, text)
	var auth smtp.Auth
	if username != "" {
		auth = smtp.PlainAuth("", username, password, host)
	}
	if port != 465 {
		if err := smtp.SendMail(addr, auth, sender.Address, []string{to}, []byte(body)); err != nil {
			return fmt.Errorf("auth.smtp: send: %w", err)
		}
		return nil
	}
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("auth.smtp: connect: %w", err)
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("auth.smtp: connect: %w", err)
	}
	defer c.Close() //nolint:errcheck
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("auth.smtp: authenticate: %w", err)
		}
	}
	if err := c.Mail(sender.Address); err != nil {
		return fmt.Errorf("auth.smtp: send: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("auth.smtp: send: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("auth.smtp: send: %w", err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		return fmt.Errorf("auth.smtp: send: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("auth.smtp: send: %w", err)
	}
	return c.Quit()
}

func subjectFor(i email.Intent) string {
	switch i {
	case email.IntentResetPassword:
		return "Reset your Skael password"
	case email.IntentVerifyEmail:
		return "Verify your Skael email address"
	case email.IntentEmailChange:
		return "Confirm your new Skael email address"
	case email.IntentDeleteAccount:
		return "Confirm your Skael account deletion"
	default:
		return "Skael account notice"
	}
}
