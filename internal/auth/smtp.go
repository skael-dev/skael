package auth

import (
	"context"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"

	"github.com/alternayte/auth-all/email"
)

// NewSMTPSender returns the sender behind self-service password reset. net/smtp
// uses STARTTLS when the server offers it, and refuses to send auth in the clear.
func NewSMTPSender(host string, port int, username, password, from string) email.Sender {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	var auth smtp.Auth
	if username != "" {
		auth = smtp.PlainAuth("", username, password, host)
	}
	return email.SenderFunc(func(_ context.Context, m email.Message) error {
		if strings.ContainsAny(m.To, "\r\n") {
			return fmt.Errorf("auth.smtp: invalid recipient")
		}
		body := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n",
			from, m.To, subjectFor(m.Intent), m.URL)
		if err := smtp.SendMail(addr, auth, from, []string{m.To}, []byte(body)); err != nil {
			return fmt.Errorf("auth.smtp: send: %w", err)
		}
		return nil
	})
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
