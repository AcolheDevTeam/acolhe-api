package mailer

import (
	"context"
	"net/smtp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type captured struct {
	address string
	auth    smtp.Auth
	from    string
	to      []string
	message string
}

func fakeSender(store *captured) sender {
	return func(_ context.Context, address string, auth smtp.Auth, from string, to []string, message []byte) error {
		*store = captured{address: address, auth: auth, from: from, to: to, message: string(message)}
		return nil
	}
}

func TestNewSMTPValidatesConfig(t *testing.T) {
	_, err := newSMTP(Config{From: "a@b.test"}, fakeSender(&captured{}))
	assert.Error(t, err, "sem endereço")

	_, err = newSMTP(Config{Address: "smtp.test", From: "a@b.test"}, fakeSender(&captured{}))
	assert.Error(t, err, "endereço sem porta")

	_, err = newSMTP(Config{Address: "smtp.test:587", From: "Nome <a@b.test>"}, fakeSender(&captured{}))
	assert.Error(t, err, "remetente com display name")

	_, err = newSMTP(Config{Address: "smtp.test:587", From: "a@b.test", Username: "u"}, fakeSender(&captured{}))
	assert.Error(t, err, "usuário sem senha")

	_, err = newSMTP(Config{Address: "smtp.test:587", From: "a@b.test"}, fakeSender(&captured{}))
	assert.NoError(t, err)
}

func TestSendBuildsHeadersAndUsesAuth(t *testing.T) {
	var got captured
	m, err := newSMTP(Config{
		Address: "smtp-relay.example:587", Username: "login", Password: "chave", From: "acolhe@example.test",
	}, fakeSender(&got))
	require.NoError(t, err)

	err = m.Send(context.Background(), Message{
		To:      "paciente@example.test",
		Subject: "Convite para o Acolhe — Dra. Ana",
		Body:    "Olá.\nLinha 2.",
	})
	require.NoError(t, err)

	assert.Equal(t, "smtp-relay.example:587", got.address)
	assert.NotNil(t, got.auth, "PLAIN auth deve ser configurada quando há usuário")
	assert.Equal(t, "acolhe@example.test", got.from)
	assert.Equal(t, []string{"paciente@example.test"}, got.to)
	assert.Contains(t, got.message, "From: acolhe@example.test\r\n")
	assert.Contains(t, got.message, "To: paciente@example.test\r\n")
	assert.Contains(t, got.message, "Subject: =?utf-8?q?", "assunto com acento deve ser codificado")
	assert.Contains(t, got.message, "Content-Type: text/plain; charset=UTF-8\r\n")
	assert.True(t, strings.HasSuffix(got.message, "Olá.\r\nLinha 2.\r\n"), "corpo normalizado para CRLF")
}

func TestSendRejectsHeaderInjectionAndInvalidRecipient(t *testing.T) {
	var got captured
	m, err := newSMTP(Config{Address: "smtp.test:587", From: "a@b.test"}, fakeSender(&got))
	require.NoError(t, err)

	err = m.Send(context.Background(), Message{To: "x@y.test\r\nBcc: z@w.test", Subject: "s", Body: "b"})
	assert.Error(t, err)
	err = m.Send(context.Background(), Message{To: "x@y.test", Subject: "s\r\nBcc: z@w.test", Body: "b"})
	assert.Error(t, err)
	err = m.Send(context.Background(), Message{To: "Nome <x@y.test>", Subject: "s", Body: "b"})
	assert.Error(t, err)
	err = m.Send(context.Background(), Message{To: "x@y.test", Subject: "", Body: "b"})
	assert.Error(t, err)
	assert.Empty(t, got.message, "nada deve ser enviado quando a validação falha")
}
