package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/herdifirdausss/seev/internal/notify/channel"
)

type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	ReplyTo  string
	TLSMode  string
	Timeout  time.Duration
}

type Sender struct{ cfg Config }

func NewSender(cfg Config) *Sender { return &Sender{cfg: cfg} }

func (s *Sender) Send(ctx context.Context, message channel.EmailMessage) (channel.ProviderResult, error) {
	if err := validateMessage(message); err != nil {
		return channel.ProviderResult{Permanent: true, ErrorCode: "email_message_invalid"}, err
	}
	if s.cfg.Host == "" || s.cfg.Port <= 0 {
		return channel.ProviderResult{ErrorCode: "smtp_unconfigured"}, errors.New("smtp is not configured")
	}
	if _, err := mail.ParseAddress(s.cfg.From); err != nil {
		return channel.ProviderResult{Permanent: true, ErrorCode: "smtp_sender_invalid"}, errors.New("email sender is invalid")
	}
	if strings.ContainsAny(s.cfg.ReplyTo, "\r\n") {
		return channel.ProviderResult{Permanent: true, ErrorCode: "smtp_reply_to_invalid"}, errors.New("email reply-to contains header control characters")
	}
	if s.cfg.ReplyTo != "" {
		if _, err := mail.ParseAddress(s.cfg.ReplyTo); err != nil {
			return channel.ProviderResult{Permanent: true, ErrorCode: "smtp_reply_to_invalid"}, errors.New("email reply-to is invalid")
		}
	}
	deadline := s.cfg.Timeout
	if deadline <= 0 {
		deadline = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	addr := net.JoinHostPort(s.cfg.Host, fmt.Sprint(s.cfg.Port))
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return channel.ProviderResult{ErrorCode: "smtp_connect"}, err
	}
	defer conn.Close()
	if strings.EqualFold(s.cfg.TLSMode, "tls") {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return classifySMTP(err)
		}
		conn = tlsConn
	}
	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return channel.ProviderResult{ErrorCode: "smtp_client"}, err
	}
	defer client.Close()
	if strings.EqualFold(s.cfg.TLSMode, "starttls") {
		if err := client.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return classifySMTP(err)
		}
	}
	if s.cfg.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return classifySMTP(err)
		}
	}
	if err := client.Mail(s.cfg.From); err != nil {
		return classifySMTP(err)
	}
	if err := client.Rcpt(message.To); err != nil {
		return classifySMTP(err)
	}
	writer, err := client.Data()
	if err != nil {
		return classifySMTP(err)
	}
	if _, err := writer.Write([]byte(mimeMessage(s.cfg, message))); err != nil {
		_ = writer.Close()
		return classifySMTP(err)
	}
	if err := writer.Close(); err != nil {
		return classifySMTP(err)
	}
	if err := client.Quit(); err != nil {
		return classifySMTP(err)
	}
	return channel.ProviderResult{Accepted: true, ProviderMessageID: message.MessageID}, nil
}

func validateMessage(message channel.EmailMessage) error {
	if message.DeliveryID == "" || message.MessageID == "" || message.Subject == "" || message.Text == "" {
		return errors.New("email message is incomplete")
	}
	if strings.ContainsAny(message.Subject, "\r\n") {
		return errors.New("email subject contains CR/LF")
	}
	if _, err := mail.ParseAddress(message.To); err != nil {
		return errors.New("email recipient is invalid")
	}
	return nil
}

func mimeMessage(cfg Config, message channel.EmailMessage) string {
	// A simple multipart/alternative boundary is deterministic for one
	// delivery and contains no user-controlled header names.
	boundary := "seev-" + message.DeliveryID
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", cfg.From)
	if cfg.ReplyTo != "" {
		fmt.Fprintf(&b, "Reply-To: %s\r\n", cfg.ReplyTo)
	}
	fmt.Fprintf(&b, "To: %s\r\nSubject: %s\r\nMessage-ID: <%s>\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=\"%s\"\r\nX-Seev-Delivery-ID: %s\r\nX-Seev-Notification-ID: %s\r\n\r\n", message.To, message.Subject, message.MessageID, time.Now().UTC().Format(time.RFC1123Z), boundary, message.DeliveryID, message.NotificationID)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n", boundary, message.Text)
	if message.HTML != "" {
		fmt.Fprintf(&b, "--%s\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n", boundary, message.HTML)
	}
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.String()
}

func classifySMTP(err error) (channel.ProviderResult, error) {
	message := err.Error()
	result := channel.ProviderResult{ErrorCode: "smtp_error", ResponseExcerpt: truncate(message)}
	// net/smtp exposes SMTP errors through text; classify explicit 4xx as
	// transient and 5xx as permanent, while transport errors remain retryable.
	if strings.Contains(message, " 5") || strings.Contains(message, "550") || strings.Contains(message, "551") || strings.Contains(message, "553") {
		result.Permanent = true
		result.ErrorCode = "smtp_permanent"
	}
	return result, err
}
func truncate(value string) string {
	if len(value) > 512 {
		return value[:512]
	}
	return value
}
