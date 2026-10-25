package domain

import (
	"context"
	"testing"
)

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
	if sender.template.Parameters[0] != "56911112222" || sender.template.Parameters[1] != "Necesito ayuda con mi cotizacion" || sender.template.Parameters[2] != "Requiere atencion del equipo" {
		t.Fatalf("unexpected parameters %#v", sender.template.Parameters)
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
