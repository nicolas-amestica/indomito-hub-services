package dteinbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/oklog/ulid/v2"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/sii"
)

// Mailbox permite simular Gmail y sus fallas sin acceder a mensajes reales.
type Mailbox interface {
	Connect(context.Context, GmailConfig) error
	List(context.Context, time.Time, string) (MessagePage, error)
	Raw(context.Context, string) (string, error)
}

// Objects conserva originales antes de confirmar un mensaje.
type Objects interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// Worker sincroniza una casilla con paginación retomable por ambiente.
type Worker struct {
	Mail        Mailbox
	Store       Store
	Objects     Objects
	Bucket      string
	Stage       string
	Config      GmailConfig
	Credentials sii.Credentials
	Now         func() time.Time
	Registry    func(context.Context, Document) (sii.ReceivedResult, error)
}

// ErrSyncBusy indica que otra lectura conserva el bloqueo del cursor.
var ErrSyncBusy = errors.New("lectura de correo en curso")

// ErrSyncIncomplete solicita retomar el cursor sin declarar el barrido terminado.
var ErrSyncIncomplete = errors.New("lectura de correo parcialmente procesada")

func (w Worker) put(ctx context.Context, key, contentType string, raw []byte) error {
	_, err := w.Objects.PutObject(ctx, &s3.PutObjectInput{Bucket: &w.Bucket, Key: &key, ContentType: &contentType, Body: bytes.NewReader(raw), ServerSideEncryption: "AES256"})
	return err
}

// Run usa un watermark durable, solapamiento de 48 h y reconciliación completa diaria.
// El cursor solo avanza cuando todos los originales de la página están respaldados.
func (w Worker) Run(ctx context.Context) (runErr error) {
	if !w.Config.Enabled {
		return nil
	}
	expected := map[string]string{"dev": "sii-dev@work.girasindomito.cl", "prd": "sii@work.girasindomito.cl"}
	if w.Config.Mailbox != "agente@work.girasindomito.cl" || w.Config.Alias != expected[w.Stage] || expected[w.Stage] == "" {
		return errors.New("destino Gmail no autorizado")
	}
	if (w.Stage == "prd") != (w.Credentials.Environment == sii.EnvironmentProduction) {
		return errors.New("ambiente SII no coincide con Lambda")
	}
	start, err := time.Parse(time.DateOnly, w.Config.StartDate)
	if err != nil || start.After(w.Now()) {
		return errors.New("startDate inválido")
	}
	owner := ulid.Make().String()
	acquired, err := w.Store.AcquireLease(ctx, owner, w.Now().UTC())
	if err != nil {
		return err
	}
	if !acquired {
		return ErrSyncBusy
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if err := w.Store.ReleaseLease(releaseCtx, owner); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}()
	if err = w.Mail.Connect(ctx, w.Config); err != nil {
		return err
	}
	state, err := w.Store.State(ctx)
	if err != nil {
		return err
	}
	now := w.Now().UTC()
	if state.Page == "" {
		after := start
		last, _ := time.Parse(time.RFC3339, state.LastSuccess)
		reconciled, _ := time.Parse(time.RFC3339, state.ReconciledAt)
		if !last.IsZero() && now.Sub(reconciled) < 24*time.Hour {
			after = last.Add(-48 * time.Hour)
			if after.Before(start) {
				after = start
			}
		} else {
			state.ReconciledAt = now.Format(time.RFC3339)
		}
		state.After = after.Format(time.RFC3339)
		state.StartedAt = now.Format(time.RFC3339)
	}
	after, err := time.Parse(time.RFC3339, state.After)
	if err != nil {
		return err
	}
	for pages := 0; pages < 10; pages++ {
		page, err := w.Mail.List(ctx, after, state.Page)
		if err != nil {
			if errors.Is(err, ErrInvalidPage) && state.Page != "" {
				state.Page = ""
				if e := w.Store.SaveState(ctx, state); e != nil {
					return e
				}
			}
			return err
		}
		for _, message := range page.Messages {
			seen, e := w.Store.Seen(ctx, message.ID)
			if e != nil {
				return e
			}
			if seen {
				continue
			}
			encoded, e := w.Mail.Raw(ctx, message.ID)
			if e != nil {
				if errors.Is(e, ErrMailSize) {
					if e = w.Store.Mark(ctx, message.ID, "OVERSIZED", "correo demasiado grande; revisar y reenviar solo XML"); e != nil {
						return e
					}
					continue
				}
				if errors.Is(e, ErrMailGone) {
					if e = w.Store.Mark(ctx, message.ID, "GONE", "eliminado antes de descargar"); e != nil {
						return e
					}
					continue
				}
				return e
			}
			raw, e := base64.RawURLEncoding.DecodeString(encoded)
			if e != nil {
				raw, e = base64.URLEncoding.DecodeString(encoded)
			}
			if e != nil {
				return errors.New("correo Gmail base64 inválido")
			}
			other := expected["prd"]
			if w.Stage == "prd" {
				other = expected["dev"]
			}
			attachments, e := Extract(raw, w.Config.Alias, other)
			// Un correo de otro alias nunca se copia al ambiente incorrecto.
			if errors.Is(e, ErrAmbiguousAlias) {
				if e = w.Store.Mark(ctx, message.ID, "QUARANTINED", "ambos alias presentes; reenviar por separado"); e != nil {
					return e
				}
				continue
			}
			if errors.Is(e, ErrAlias) {
				if e = w.Store.Mark(ctx, message.ID, "SKIPPED", "alias ausente, ambiguo o MIME inválido"); e != nil {
					return e
				}
				continue
			}
			if e != nil {
				sum := sha256.Sum256(raw)
				if e = w.put(ctx, "tax-documents/received/quarantine/"+hex.EncodeToString(sum[:])+".eml", "message/rfc822", raw); e != nil {
					return e
				}
				if e = w.Store.Mark(ctx, message.ID, "QUARANTINED", "MIME inválido o límites excedidos"); e != nil {
					return e
				}
				continue
			}
			if len(attachments) == 0 {
				if e = w.Store.Mark(ctx, message.ID, "SKIPPED", "sin XML"); e != nil {
					return e
				}
				continue
			}
			mailSum := sha256.Sum256(raw)
			mailKey := "tax-documents/received/mail/" + hex.EncodeToString(mailSum[:]) + ".eml"
			if e = w.put(ctx, mailKey, "message/rfc822", raw); e != nil {
				return e
			}
			status, detail := "PROCESSED", ""
			ignored := 0
			for _, attachment := range attachments {
				sum := sha256.Sum256(attachment.Raw)
				xmlKey := "tax-documents/received/xml/" + hex.EncodeToString(sum[:]) + ".xml"
				if e = w.put(ctx, xmlKey, "application/xml", attachment.Raw); e != nil {
					return e
				}
				docs, parseErr := ParseDocuments(attachment.Raw, w.Credentials.IssuerRUT, now)
				if errors.Is(parseErr, ErrSIIResponse) {
					ignored++
					continue
				}
				if parseErr != nil {
					status, detail = "QUARANTINED", "XML o firma inválidos; revisar original"
					if errors.Is(parseErr, ErrHonorariumXML) {
						detail = "Boleta de honorarios recibida; este formato aún no está soportado por el módulo de DTE"
					}
					if errors.Is(parseErr, ErrConsumptionResponse) {
						detail = "Respuesta del SII sobre consumo de folios; revisar el resultado del reporte, no corresponde a una factura recibida"
					}
					continue
				}
				for _, document := range docs {
					document.XMLKey = xmlKey
					document.MailKey = mailKey
					document.MessageID = message.ID
					if e = w.Store.SaveDocument(ctx, document); e != nil {
						if errors.Is(e, ErrDocumentConflict) {
							status, detail = "QUARANTINED", "XML conflictivo para identidad existente"
							continue
						}
						return fmt.Errorf("persistencia DTE: %w", e)
					}
					if w.Registry != nil && (document.Kind == 33 || document.Kind == 34 || document.Kind == 43) {
						persisted, readErr := w.Store.Get(ctx, document.IssueDate[:7], document.ID)
						if readErr != nil {
							return readErr
						}
						document = persisted
						if document.Registry != nil {
							continue
						}
						result, queryErr := w.Registry(ctx, document)
						if queryErr == nil {
							document.Registry = &result
							document.LastCheckedAt = w.Now().UTC().Format(time.RFC3339)
							if e = w.Store.Update(ctx, document); e != nil {
								return e
							}
						}
					}
				}
			}
			if ignored == len(attachments) {
				status, detail = "IGNORED", "Respuesta RESULTADO_ENVIO del SII; no contiene DTE recibido"
			}
			if e = w.Store.Mark(ctx, message.ID, status, detail); e != nil {
				return e
			}
		}
		state.Page = page.Next
		if page.Next == "" {
			state.LastSuccess = state.StartedAt
		}
		if err = w.Store.SaveState(ctx, state); err != nil {
			return err
		}
		if page.Next == "" {
			return nil
		}
	}
	return ErrSyncIncomplete
}
