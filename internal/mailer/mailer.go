// Package mailer envia e-mails transacionais por SMTP com STARTTLS obrigatório.
//
// É o ponto único de saída de e-mail da API para fluxos de produto (convites,
// futuros lembretes). A exportação LGPD mantém o seu próprio remetente em
// internal/exporter por ter requisitos específicos (link HTTPS, texto fixo).
package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

// Message é um e-mail em texto puro, UTF-8.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Mailer é a interface consumida pelos domínios. Implementações devem ser
// seguras para uso concorrente.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// Config descreve o servidor SMTP. Address é host:porta (ex.: smtp-relay.brevo.com:587).
type Config struct {
	Address  string
	Username string
	Password string
	From     string
}

// sender abstrai a conexão SMTP para permitir testes sem rede.
type sender func(ctx context.Context, address string, auth smtp.Auth, from string, to []string, message []byte) error

// SMTP envia mensagens via SMTP + STARTTLS.
type SMTP struct {
	config Config
	send   sender
}

// NewSMTP valida a configuração e devolve um remetente pronto para uso.
func NewSMTP(config Config) (*SMTP, error) {
	return newSMTP(config, sendStartTLS)
}

func newSMTP(config Config, send sender) (*SMTP, error) {
	if config.Address == "" || config.From == "" || send == nil {
		return nil, errors.New("configuração SMTP incompleta")
	}
	if _, _, err := net.SplitHostPort(config.Address); err != nil {
		return nil, fmt.Errorf("endereço SMTP deve ser host:porta: %w", err)
	}
	from, err := mail.ParseAddress(config.From)
	if err != nil || from.Address != config.From {
		return nil, errors.New("remetente SMTP inválido")
	}
	if (config.Username == "") != (config.Password == "") {
		return nil, errors.New("usuário e senha SMTP devem ser configurados juntos")
	}
	return &SMTP{config: config, send: send}, nil
}

// Send monta os cabeçalhos e entrega a mensagem. Nunca registra o corpo em log:
// ele pode conter links com token.
func (m *SMTP) Send(ctx context.Context, msg Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	to, err := mail.ParseAddress(msg.To)
	if err != nil || to.Address != msg.To {
		return errors.New("destinatário inválido")
	}
	if strings.TrimSpace(msg.Subject) == "" || strings.TrimSpace(msg.Body) == "" {
		return errors.New("assunto e corpo são obrigatórios")
	}
	if strings.ContainsAny(msg.To+msg.Subject+m.config.From, "\r\n") {
		return errors.New("cabeçalho de e-mail inválido")
	}

	raw := strings.Join([]string{
		"From: " + m.config.From,
		"To: " + msg.To,
		"Subject: " + mime.QEncoding.Encode("utf-8", msg.Subject),
		"Date: " + time.Now().UTC().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		strings.ReplaceAll(strings.ReplaceAll(msg.Body, "\r\n", "\n"), "\n", "\r\n"),
		"",
	}, "\r\n")

	var auth smtp.Auth
	if m.config.Username != "" {
		host, _, _ := net.SplitHostPort(m.config.Address)
		auth = smtp.PlainAuth("", m.config.Username, m.config.Password, host)
	}
	if err := m.send(ctx, m.config.Address, auth, m.config.From, []string{msg.To}, []byte(raw)); err != nil {
		return fmt.Errorf("envio SMTP: %w", err)
	}
	return nil
}

func sendStartTLS(ctx context.Context, address string, auth smtp.Auth, from string, to []string, message []byte) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("endereço SMTP: %w", err)
	}
	connection, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	defer func() { _ = connection.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}

	client, err := smtp.NewClient(connection, host)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	if supported, _ := client.Extension("STARTTLS"); !supported {
		return errors.New("servidor SMTP não oferece STARTTLS")
	}
	if err := client.StartTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}); err != nil {
		return err
	}
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	body, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := io.Copy(body, strings.NewReader(string(message))); err != nil {
		_ = body.Close()
		return err
	}
	if err := body.Close(); err != nil {
		return err
	}
	return client.Quit()
}
