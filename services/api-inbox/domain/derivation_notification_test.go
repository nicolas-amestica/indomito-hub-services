package domain

import (
	"context"
	"errors"
	"testing"
)

type derivationNotifierFake struct {
	calls int
	err   error
}

func (f *derivationNotifierFake) NotifyDerivation(context.Context, DerivationNotification) error {
	f.calls++
	return f.err
}

type derivationTemplateSenderFake struct {
	ownerID         string
	phone           string
	clientMessageID string
	template        SendTemplate
}

func (f *derivationTemplateSenderFake) SendTemplate(_ context.Context, ownerID, phone, clientMessageID string, template SendTemplate) (SendState, error) {
	f.ownerID = ownerID
	f.phone = phone
	f.clientMessageID = clientMessageID
	f.template = template
	return SendState{State: SendCommitted}, nil
}

func TestWhatsAppDerivationNotifierSendsConfiguredTemplate(t *testing.T) {
	sender := &derivationTemplateSenderFake{}
	notifier, err := NewWhatsAppDerivationNotifier(sender, DerivationNotificationConfig{
		Enabled: true, RecipientPhone: "+56959139687", TemplateName: "alerta_derivacion_agente", Language: "es_CL",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = notifier.NotifyDerivation(context.Background(), DerivationNotification{
		SourcePhone: "56911112222", MessageID: "wamid.source", Detail: "Necesito ayuda con mi cotizacion", ReasonLabel: "Requiere atencion del equipo",
	}); err != nil {
		t.Fatal(err)
	}
	if sender.ownerID != "agent-derivation" || sender.phone != "56959139687" || sender.clientMessageID == "" {
		t.Fatalf("unexpected routing owner=%q phone=%q id=%q", sender.ownerID, sender.phone, sender.clientMessageID)
	}
	if sender.template.Name != "alerta_derivacion_agente" || sender.template.Language != "es_CL" || len(sender.template.Parameters) != 3 {
		t.Fatalf("unexpected template %#v", sender.template)
	}
	if sender.template.Parameters[0] != "https://wa.me/56911112222" || sender.template.Parameters[1] != "Necesito ayuda con mi cotizacion" || sender.template.Parameters[2] != "Requiere atencion del equipo" {
		t.Fatalf("unexpected parameters %#v", sender.template.Parameters)
	}
}

func TestWhatsAppContactURLRejectsUnsafePhone(t *testing.T) {
	if _, err := whatsappContactURL("56911112222?text=unexpected"); err == nil {
		t.Fatal("expected unsafe phone to be rejected")
	}
	link, err := whatsappContactURL("+56911112222")
	if err != nil || link != "https://wa.me/56911112222" {
		t.Fatalf("unexpected link=%q err=%v", link, err)
	}
}

func TestWhatsAppDerivationNotifierRejectsInvalidConfig(t *testing.T) {
	_, err := NewWhatsAppDerivationNotifier(&derivationTemplateSenderFake{}, DerivationNotificationConfig{
		Enabled: true, RecipientPhone: "pending", TemplateName: "alerta_derivacion_agente", Language: "es_CL",
	})
	if err == nil {
		t.Fatal("expected invalid configuration error")
	}
}

func TestMultiDerivationNotifierAttemptsEveryChannel(t *testing.T) {
	first := &derivationNotifierFake{err: errors.New("whatsapp unavailable")}
	second := &derivationNotifierFake{}
	err := (MultiDerivationNotifier{first, second}).NotifyDerivation(context.Background(), DerivationNotification{})
	if err == nil || first.calls != 1 || second.calls != 1 {
		t.Fatalf("expected both channels to run: err=%v first=%d second=%d", err, first.calls, second.calls)
	}
}

func TestEmailDerivationNotifierValidatesConfiguration(t *testing.T) {
	_, err := NewEmailDerivationNotifier(DerivationSMTPConfig{Host: "smtp.example.com", Port: 587, User: "user", Password: "secret", From: "Giras <no-reply@girasindomito.cl>"}, "info@girasindomito.cl")
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewEmailDerivationNotifier(DerivationSMTPConfig{Host: "smtp.example.com", Port: 25, User: "user", Password: "secret", From: "no-reply@girasindomito.cl"}, "info@girasindomito.cl")
	if err == nil {
		t.Fatal("expected invalid SMTP port")
	}
}
