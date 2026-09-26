package notify

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http/httptest"
	"net/mail"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
	"time"
)

type smtpTranscript struct {
	commands []string
	body     []byte
	auth     []byte
	err      error
}

func smtpTestServer(t *testing.T, mode, auth string, finalReply string) (*SMTPMailer, <-chan smtpTranscript) {
	t.Helper()
	https := httptest.NewTLSServer(nil)
	certificate, serverTLS := https.Certificate(), https.TLS.Clone()
	https.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if mode == "tls" {
		listener = tls.NewListener(listener, serverTLS)
	}
	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	security := mode
	if security == "downgrade" {
		security = "starttls"
	}
	mailer, err := NewSMTPMailer(SMTPConfig{SenderConfig: SenderConfig{From: "sender@example.org", FromName: "测试发件人"}, Host: host, Port: port, Security: security, Username: "fixture-user", Password: "fixture-password"})
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	mailer.tlsConfig = &tls.Config{RootCAs: roots}
	result := make(chan smtpTranscript, 1)
	go func() {
		var transcript smtpTranscript
		defer func() { result <- transcript }()
		conn, err := listener.Accept()
		if err != nil {
			transcript.err = err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		reader := bufio.NewReader(conn)
		secure := mode == "tls"
		_, _ = fmt.Fprint(conn, "220 fixture SMTP\r\n")
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					transcript.err = err
				}
				return
			}
			line = strings.TrimSpace(line)
			transcript.commands = append(transcript.commands, line)
			switch {
			case strings.HasPrefix(line, "EHLO "):
				if !secure && mode == "starttls" {
					_, _ = fmt.Fprint(conn, "250-fixture\r\n250 STARTTLS\r\n")
				} else {
					_, _ = fmt.Fprintf(conn, "250-fixture\r\n250 AUTH %s\r\n", auth)
				}
			case line == "STARTTLS":
				_, _ = fmt.Fprint(conn, "220 start TLS\r\n")
				conn = tls.Server(conn, serverTLS)
				reader = bufio.NewReader(conn)
				secure = true
			case strings.HasPrefix(line, "AUTH PLAIN "):
				transcript.auth, _ = base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "AUTH PLAIN "))
				_, _ = fmt.Fprint(conn, "235 accepted\r\n")
			case line == "AUTH LOGIN":
				for index := 0; index < 2; index++ {
					_, _ = fmt.Fprint(conn, "334 Y2hhbGxlbmdl\r\n")
					value, readErr := reader.ReadString('\n')
					if readErr != nil {
						transcript.err = readErr
						return
					}
					decoded, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
					transcript.auth = append(transcript.auth, 0)
					transcript.auth = append(transcript.auth, decoded...)
				}
				_, _ = fmt.Fprint(conn, "235 accepted\r\n")
			case strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
				_, _ = fmt.Fprint(conn, "250 accepted\r\n")
			case line == "DATA":
				_, _ = fmt.Fprint(conn, "354 send data\r\n")
				transcript.body, transcript.err = textproto.NewReader(reader).ReadDotBytes()
				if transcript.err != nil || finalReply == "disconnect" {
					return
				}
				_, _ = fmt.Fprintf(conn, "%s\r\n", finalReply)
			case line == "QUIT":
				_, _ = fmt.Fprint(conn, "221 bye\r\n")
				return
			default:
				transcript.err = fmt.Errorf("unexpected command %q", line)
				return
			}
		}
	}()
	return mailer, result
}

func TestSMTPVerifiedTLSAuthenticationAndUTF8Message(t *testing.T) {
	for _, mode := range []string{"starttls", "tls"} {
		for _, authentication := range []string{"PLAIN", "LOGIN"} {
			t.Run(mode+"/"+authentication, func(t *testing.T) {
				mailer, result := smtpTestServer(t, mode, authentication, "250 queued")
				id, err := mailer.Send(t.Context(), Message{To: "student@example.org", Subject: "邮件测试", Text: "正文\n第二行", HTML: "<p>正文</p>", Template: TemplateMailTest})
				if err != nil || !strings.HasPrefix(id, "smtp:") {
					t.Fatalf("send result = %q, %v", id, err)
				}
				transcript := <-result
				if transcript.err != nil || string(transcript.auth) != "\x00fixture-user\x00fixture-password" {
					t.Fatalf("SMTP authentication failed: %v", transcript.err)
				}
				message, err := mail.ReadMessage(bytes.NewReader(transcript.body))
				if err != nil {
					t.Fatal(err)
				}
				subject, err := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
				if err != nil || subject != "邮件测试" {
					t.Fatalf("subject = %q, %v", subject, err)
				}
				_, parameters, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
				if err != nil {
					t.Fatal(err)
				}
				parts := multipart.NewReader(message.Body, parameters["boundary"])
				for _, expected := range []string{"正文\n第二行", "<p>正文</p>"} {
					part, err := parts.NextPart()
					if err != nil {
						t.Fatal(err)
					}
					body, _ := io.ReadAll(part)
					if string(body) != expected {
						t.Fatalf("body = %q, want %q", body, expected)
					}
				}
			})
		}
	}
}

func TestSMTPRefusesSTARTTLSDowngradeBeforeCredentials(t *testing.T) {
	mailer, result := smtpTestServer(t, "downgrade", "PLAIN", "250 accepted")
	_, err := mailer.Send(t.Context(), Message{To: "student@example.org", Subject: "test", Text: "test", Template: TemplateMailTest})
	if err == nil || !definitelyNotSubmitted(err) {
		t.Fatalf("unencrypted server accepted: %v", err)
	}
	transcript := <-result
	if len(transcript.auth) != 0 || len(transcript.body) != 0 {
		t.Fatal("credentials or message sent without TLS")
	}
}

func TestSMTPPreservesUncertainFinalAcknowledgement(t *testing.T) {
	for _, reply := range []string{"disconnect", "550 mailbox unavailable"} {
		t.Run(reply, func(t *testing.T) {
			mailer, result := smtpTestServer(t, "tls", "PLAIN", reply)
			_, err := mailer.Send(t.Context(), Message{To: "student@example.org", Subject: "test", Text: "test", Template: TemplateMailTest})
			if err == nil || definitelyNotSubmitted(err) != (reply != "disconnect") {
				t.Fatalf("acknowledgement classification = %v", err)
			}
			<-result
		})
	}
}
