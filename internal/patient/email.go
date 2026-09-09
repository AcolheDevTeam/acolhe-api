package patient

import (
	"fmt"
	"net/smtp"
)

type EmailSender interface {
	Send(to, subject, body string) error
}

type SMTPMailer struct{ Host, Port, Username, Password, From string }

func (m SMTPMailer) Send(to, subject, body string) error {
	if m.Host == "" {
		return fmt.Errorf("provedor de e-mail não configurado")
	}
	msg := []byte("From: " + m.From + "\r\nTo: " + to + "\r\nSubject: " + subject + "\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + body)
	var auth smtp.Auth
	if m.Username != "" {
		auth = smtp.PlainAuth("", m.Username, m.Password, m.Host)
	}
	return smtp.SendMail(m.Host+":"+m.Port, auth, m.From, []string{to}, msg)
}
