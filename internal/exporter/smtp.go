package exporter

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

type SMTPConfig struct {
	Address  string
	Username string
	Password string
	From     string
}

type SMTPSender func(
	ctx context.Context,
	address string,
	auth smtp.Auth,
	from string,
	to []string,
	message []byte,
) error

type SMTPMailer struct {
	config SMTPConfig
	send   SMTPSender
}

func NewSMTPMailer(config SMTPConfig) (*SMTPMailer, error) {
	return newSMTPMailer(config, sendSMTPStartTLS)
}

func newSMTPMailer(config SMTPConfig, send SMTPSender) (*SMTPMailer, error) {
	if config.Address == "" || config.From == "" || send == nil {
		return nil, errors.New("configuração SMTP incompleta")
	}
	from, err := mail.ParseAddress(config.From)
	if err != nil || from.Address != config.From {
		return nil, errors.New("remetente SMTP inválido")
	}
	if (config.Username == "") != (config.Password == "") {
		return nil, errors.New("usuário e senha SMTP devem ser configurados juntos")
	}
	return &SMTPMailer{config: config, send: send}, nil
}

func (mailer *SMTPMailer) SendExportReady(
	ctx context.Context,
	recipient string,
	_ string,
	downloadURL string,
	expiresAt time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	address, err := mail.ParseAddress(recipient)
	if err != nil || address.Address != recipient {
		return errors.New("destinatário SMTP inválido")
	}
	link, err := url.Parse(downloadURL)
	if err != nil || link.Scheme != "https" || link.Host == "" {
		return errors.New("link de exportação deve usar HTTPS")
	}
	if strings.ContainsAny(recipient+mailer.config.From, "\r\n") {
		return errors.New("cabeçalho SMTP inválido")
	}

	message := strings.Join([]string{
		"From: " + mailer.config.From,
		"To: " + recipient,
		"Subject: Sua exportacao de dados do Acolhe esta pronta",
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		"Sua exportacao de dados pessoais esta pronta.",
		"",
		"Baixe o arquivo ZIP pelo link privado abaixo:",
		downloadURL,
		"",
		"O link expira em " + expiresAt.UTC().Format(time.RFC3339) + ".",
		"Se voce nao solicitou esta exportacao, entre em contato com o suporte.",
		"",
	}, "\r\n")

	var auth smtp.Auth
	if mailer.config.Username != "" {
		host := mailer.config.Address
		if separator := strings.LastIndex(host, ":"); separator > 0 {
			host = host[:separator]
		}
		auth = smtp.PlainAuth("", mailer.config.Username, mailer.config.Password, host)
	}
	if err := mailer.send(
		ctx,
		mailer.config.Address,
		auth,
		mailer.config.From,
		[]string{recipient},
		[]byte(message),
	); err != nil {
		return fmt.Errorf("envio SMTP: %w", err)
	}
	return nil
}

func sendSMTPStartTLS(
	ctx context.Context,
	address string,
	auth smtp.Auth,
	from string,
	to []string,
	message []byte,
) error {
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
	if err := client.StartTLS(&tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: host,
	}); err != nil {
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
