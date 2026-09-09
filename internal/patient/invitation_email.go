package patient

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"
	_ "time/tzdata" // fuso de Brasília disponível mesmo em imagens sem tzdata (distroless)

	"github.com/google/uuid"

	"github.com/joycesilva/acolhe-api/internal/mailer"
)

// Estados de entrega do convite devolvidos ao frontend. O paciente e o convite
// existem independentemente do resultado do e-mail; o link copiável é o fallback.
const (
	DeliverySent     = "sent"     // e-mail aceito pelo provedor
	DeliveryFailed   = "failed"   // provedor recusou ou não respondeu
	DeliveryDisabled = "disabled" // SMTP não configurado neste ambiente
)

const invitationEmailTimeout = 20 * time.Second

// Option configura o Service sem quebrar a assinatura de NewService.
type Option func(*Service)

// WithInvitationMailer habilita o envio de convites por e-mail. frontendURL é a
// origem pública do acolhe-web usada para montar o link (ex.: https://app.acolhe.com.br).
func WithInvitationMailer(m mailer.Mailer, frontendURL string) Option {
	return func(s *Service) {
		s.mailer = m
		s.frontendURL = strings.TrimRight(strings.TrimSpace(frontendURL), "/")
	}
}

// deliverInvitation tenta enviar o convite e registra o resultado em
// invitation.DeliveryStatus. Nunca devolve erro: a criação do paciente não
// depende do provedor de e-mail. O token nunca vai para o log.
func (s *Service) deliverInvitation(ctx context.Context, patientID uuid.UUID, invitation *Invitation, patientName, psychologistName string) {
	if s.mailer == nil || s.frontendURL == "" {
		invitation.DeliveryStatus = DeliveryDisabled
		return
	}
	msg, err := invitationMessage(s.frontendURL, invitation, patientName, psychologistName)
	if err != nil {
		log.Printf("convite do paciente %s: mensagem inválida: %v", patientID, err)
		invitation.DeliveryStatus = DeliveryFailed
		return
	}
	sendCtx, cancel := context.WithTimeout(ctx, invitationEmailTimeout)
	defer cancel()
	if err := s.mailer.Send(sendCtx, msg); err != nil {
		log.Printf("convite do paciente %s: e-mail não enviado: %v", patientID, err)
		invitation.DeliveryStatus = DeliveryFailed
		return
	}
	invitation.DeliveryStatus = DeliverySent
}

// InvitationLink monta a URL pública de aceite a partir da origem do frontend.
func InvitationLink(frontendURL, token string) string {
	return strings.TrimRight(frontendURL, "/") + "/invite/" + url.PathEscape(token)
}

// invitationMessage é o texto do e-mail de convite, em português, sem HTML.
func invitationMessage(frontendURL string, invitation *Invitation, patientName, psychologistName string) (mailer.Message, error) {
	if invitation == nil || invitation.Token == "" || invitation.Email == "" {
		return mailer.Message{}, fmt.Errorf("convite sem token ou e-mail")
	}
	firstName := strings.TrimSpace(patientName)
	if parts := strings.Fields(firstName); len(parts) > 0 {
		firstName = parts[0]
	}
	if firstName == "" {
		firstName = "olá"
	}
	psychologist := strings.TrimSpace(psychologistName)
	if psychologist == "" {
		psychologist = "Sua psicóloga"
	}

	location, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		location = time.UTC
	}
	validity := invitation.ExpiresAt.In(location).Format("02/01/2006 às 15:04")

	body := strings.Join([]string{
		"Olá, " + firstName + ".",
		"",
		psychologist + " convidou você para o Acolhe, o espaço onde vocês vão acompanhar o processo entre as sessões.",
		"",
		"Para aceitar, leia os termos de consentimento e crie sua senha pelo link abaixo:",
		InvitationLink(frontendURL, invitation.Token),
		"",
		"O link vale até " + validity + " (horário de Brasília) e só pode ser usado uma vez.",
		"",
		"Se você não esperava este convite, pode ignorar este e-mail. Nenhuma conta será criada sem o seu aceite.",
		"",
		"Acolhe",
	}, "\n")

	return mailer.Message{
		To:      invitation.Email,
		Subject: "Convite para o Acolhe · " + psychologist,
		Body:    body,
	}, nil
}
