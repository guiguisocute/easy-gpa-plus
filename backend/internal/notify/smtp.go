package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"easygpa/backend/internal/opsconfig"
	"github.com/google/uuid"
)

type SMTPConfig struct {
	SenderConfig
	Host     string
	Port     int
	Security string
	Username string
	Password string
}

func (c SMTPConfig) Validate() error {
	if err := c.SenderConfig.Validate(); err != nil {
		return err
	}
	if c.Host == "" || len(c.Host) > 253 || strings.ContainsAny(c.Host, " /\\@?#\r\n\t") {
		return errors.New("SMTP 主机必须是域名或 IP，不含协议、端口或路径")
	}
	if net.ParseIP(c.Host) == nil {
		for _, r := range c.Host {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-') {
				return errors.New("SMTP 主机格式不正确")
			}
		}
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("SMTP 端口必须是 1—65535 的整数")
	}
	if c.Security != "starttls" && c.Security != "tls" {
		return errors.New("SMTP 必须使用 STARTTLS 或隐式 TLS")
	}
	if (c.Username == "") != (c.Password == "") {
		return errors.New("SMTP 用户名和密码必须同时配置或同时留空")
	}
	return nil
}

func SMTPConfigFromSettings(settings opsconfig.Mail, cipher *opsconfig.Cipher) (SMTPConfig, error) {
	cfg := SMTPConfig{SenderConfig: senderFromSettings(settings), Host: settings.SMTPHost, Port: settings.SMTPPort, Security: settings.SMTPSecurity}
	var err error
	if cfg.Username, err = openMailSecret("SMTP 用户名", settings.SMTPUsername, cipher); err != nil {
		return SMTPConfig{}, err
	}
	if cfg.Password, err = openMailSecret("SMTP 密码", settings.SMTPPassword, cipher); err != nil {
		return SMTPConfig{}, err
	}
	return cfg, cfg.Validate()
}

type SMTPMailer struct {
	cfg       SMTPConfig
	tlsConfig *tls.Config
}

func NewSMTPMailer(cfg SMTPConfig) (*SMTPMailer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &SMTPMailer{cfg: cfg}, nil
}

func (m *SMTPMailer) Ready(context.Context) error { return m.cfg.Validate() }

func (m *SMTPMailer) Send(ctx context.Context, msg Message) (string, error) {
	from, name, err := m.cfg.forMessage(msg)
	if err != nil {
		return "", &NotSubmittedError{Err: err}
	}
	messageID := uuid.NewString() + "@" + strings.SplitN(from, "@", 2)[1]
	content, err := smtpMessage(msg, from, name, m.cfg.ReplyTo, messageID)
	if err != nil {
		return "", &NotSubmittedError{Err: err}
	}
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port)))
	if err != nil {
		return "", smtpFailure("ConnectFailed", err, false)
	}
	defer conn.Close()
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stop()
	deadline := time.Now().Add(30 * time.Second)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return "", smtpFailure("ConnectFailed", err, false)
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: m.cfg.Host}
	if m.tlsConfig != nil {
		tlsConfig = m.tlsConfig.Clone()
		tlsConfig.ServerName = m.cfg.Host
		tlsConfig.MinVersion = max(tlsConfig.MinVersion, tls.VersionTLS12)
	}
	if m.cfg.Security == "tls" {
		secure := tls.Client(conn, tlsConfig)
		if err := secure.HandshakeContext(ctx); err != nil {
			return "", smtpFailure("TLSFailed", err, false)
		}
		conn = secure
	}
	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return "", smtpFailure("GreetingFailed", err, false)
	}
	defer client.Close()
	if m.cfg.Security == "starttls" {
		// Never silently downgrade to cleartext, even on a loopback relay.
		if supported, _ := client.Extension("STARTTLS"); !supported {
			return "", smtpFailure("STARTTLSRequired", nil, false)
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return "", smtpFailure("TLSFailed", err, false)
		}
	}
	if m.cfg.Username != "" {
		_, mechanisms := client.Extension("AUTH")
		var authentication smtp.Auth
		for _, mechanism := range strings.Fields(strings.ToUpper(mechanisms)) {
			if mechanism == "PLAIN" {
				authentication = smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)
				break
			}
			if mechanism == "LOGIN" {
				authentication = &smtpLogin{host: m.cfg.Host, username: m.cfg.Username, password: m.cfg.Password}
			}
		}
		if authentication == nil {
			return "", smtpFailure("AuthenticationUnsupported", nil, false)
		}
		if err := client.Auth(authentication); err != nil {
			return "", smtpFailure("AuthenticationFailed", err, false)
		}
	}
	if err := client.Mail(from); err != nil {
		return "", smtpFailure("SenderRejected", err, false)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return "", smtpFailure("RecipientRejected", err, false)
	}
	writer, err := client.Data()
	if err != nil {
		return "", smtpFailure("DataRejected", err, false)
	}
	if _, err := writer.Write(content); err != nil {
		return "", smtpFailure("DataWriteFailed", err, false)
	}
	if err := writer.Close(); err != nil {
		// A lost acknowledgement after DATA may mean delivery succeeded.
		// Preserve the ledger's uncertain outcome instead of resending blindly.
		return "", smtpFailure("OutcomeUnknown", err, true)
	}
	_ = client.Quit() // DATA's 250 acknowledgement is authoritative.
	return "smtp:" + messageID, nil
}

func smtpFailure(stage string, err error, submitted bool) error {
	failure := &ProviderFailure{Provider: "SMTP", Code: stage, Rejected: !submitted}
	var reply *textproto.Error
	if errors.As(err, &reply) {
		failure.Code = strconv.Itoa(reply.Code)
		failure.Rejected = reply.Code >= 400 && reply.Code <= 599
	}
	if !submitted {
		return &NotSubmittedError{Err: failure}
	}
	return failure
}

type smtpLogin struct {
	host, username, password string
	step                     int
}

func (a *smtpLogin) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS || server.Name != a.host {
		return "", nil, errors.New("SMTP LOGIN requires verified TLS")
	}
	return "LOGIN", nil, nil
}

func (a *smtpLogin) Next(_ []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	a.step++
	switch a.step {
	case 1:
		return []byte(a.username), nil
	case 2:
		return []byte(a.password), nil
	default:
		return nil, errors.New("unexpected SMTP LOGIN challenge")
	}
}

func smtpMessage(msg Message, from, name, replyTo, messageID string) ([]byte, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, part := range []struct{ kind, value string }{{"text/plain", msg.Text}, {"text/html", msg.HTML}} {
		if part.value == "" {
			continue
		}
		headers := textproto.MIMEHeader{"Content-Type": {part.kind + "; charset=UTF-8"}, "Content-Transfer-Encoding": {"quoted-printable"}}
		out, err := writer.CreatePart(headers)
		if err != nil {
			return nil, err
		}
		encoded := quotedprintable.NewWriter(out)
		if _, err := encoded.Write([]byte(part.value)); err != nil {
			return nil, err
		}
		if err := encoded.Close(); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	var header bytes.Buffer
	fmt.Fprintf(&header, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s>\r\nMIME-Version: 1.0\r\n", (&mail.Address{Name: name, Address: from}).String(), (&mail.Address{Address: msg.To}).String(), mime.QEncoding.Encode("UTF-8", msg.Subject), time.Now().Format(time.RFC1123Z), messageID)
	if replyTo != "" {
		fmt.Fprintf(&header, "Reply-To: %s\r\n", (&mail.Address{Address: replyTo}).String())
	}
	fmt.Fprintf(&header, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", writer.Boundary())
	header.Write(body.Bytes())
	return header.Bytes(), nil
}
