// Package brandedemail comparte la identidad visual de los correos transaccionales.
package brandedemail

import (
	"bytes"
	"html/template"
)

// Detail representa un dato destacado del correo.
type Detail struct {
	Label string
	Value string
}

// Content define el contenido escapado del correo.
type Content struct {
	Preheader        string
	Eyebrow          string
	Title            string
	Lead             string
	Body             string
	Notice           string
	Details          []Detail
	Stage            string
	AttachmentNotice bool
	Actions          []Action
	Messages         []Message
	HistoryNotice    string
	Reference        string
}

// Action representa un enlace de acción del correo.
type Action struct {
	Label string
	URL   string
}

// Message conserva autor, fecha y contenido de un mensaje del historial.
type Message struct {
	Author    string
	Timestamp string
	Text      string
}

var brandedEmailTemplate = template.Must(template.New("branded-email").Parse(`<!doctype html>
<html lang="es">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light dark"><meta name="supported-color-schemes" content="light dark"><title>{{.Title}}</title>
<style>
@media (prefers-color-scheme: dark) {
  .email-canvas { background:#111214 !important; }
  .email-card { background:#1b1c1f !important; border-color:#2f2f34 !important; }
  .email-soft { background:#242528 !important; border-color:#2f2f34 !important; }
  .email-text { color:#f4f4f5 !important; }
  .email-muted { color:#a1a1aa !important; }
  .email-lead { color:#d4d4d8 !important; }
  .email-line { border-color:#2f2f34 !important; }
}
</style></head>
<body class="email-canvas" style="margin:0;background:#f9fafb;color:#1c1c1c;font-family:Arial,Helvetica,sans-serif;">
  <div style="display:none;max-height:0;overflow:hidden;opacity:0;color:transparent;">{{.Preheader}}</div>
  <table class="email-canvas" role="presentation" width="100%" cellspacing="0" cellpadding="0" style="background:#f9fafb;padding:28px 12px;">
    <tr><td align="center">
      <table class="email-card" role="presentation" width="100%" cellspacing="0" cellpadding="0" style="max-width:620px;background:#ffffff;border:1px solid #e9eaec;border-radius:18px;overflow:hidden;box-shadow:0 10px 30px rgba(17,24,39,.08);">
        <tr><td style="height:7px;background:#ebff00;font-size:0;line-height:0;">&nbsp;</td></tr>
        <tr><td style="background:#1c1c1c;padding:28px 34px;color:#ffffff;">
          <div style="font-size:20px;font-weight:800;letter-spacing:.2px;">Giras Indómito</div>
          <div style="margin-top:5px;color:#d1d5db;font-size:13px;">Dale una vuelta a la aventura</div>
        </td></tr>
        <tr><td style="padding:34px;">
          <div class="email-lead" style="color:#3a3a3a;font-size:12px;font-weight:800;letter-spacing:1.4px;text-transform:uppercase;">{{.Eyebrow}}</div>
          <h1 class="email-text" style="margin:10px 0 14px;color:#1c1c1c;font-size:28px;line-height:1.2;">{{.Title}}</h1>
          <p class="email-lead" style="margin:0;color:#3a3a3a;font-size:17px;line-height:1.6;">{{.Lead}}</p>
          {{if .Details}}
          <table class="email-soft" role="presentation" width="100%" cellspacing="0" cellpadding="0" style="margin-top:26px;background:#f9fafb;border:1px solid #e9eaec;border-radius:12px;">
            {{range .Details}}<tr>
              <td class="email-muted email-line" style="padding:12px 16px;border-bottom:1px solid #e9eaec;color:#616161;font-size:13px;width:34%;">{{.Label}}</td>
              <td class="email-text email-line" style="padding:12px 16px;border-bottom:1px solid #e9eaec;color:#1c1c1c;font-size:14px;font-weight:700;word-break:break-word;">{{.Value}}</td>
            </tr>{{end}}
          </table>
          {{end}}
          <p class="email-lead" style="margin:26px 0 0;color:#3a3a3a;font-size:15px;line-height:1.65;">{{.Body}}</p>
          {{if .Notice}}<div style="margin-top:22px;padding:16px 18px;border-left:4px solid #ebff00;background:#f8fbcf;color:#3a3a3a;font-size:14px;line-height:1.55;">{{.Notice}}</div>{{end}}
          {{range .Actions}}<p style="margin:22px 0;"><a href="{{.URL}}" style="display:inline-block;padding:12px 18px;background:#1c1c1c;color:#ffffff;border-radius:8px;text-decoration:none;">{{.Label}}</a></p>{{end}}
          {{if .Messages}}<h2 class="email-text" style="margin:28px 0 16px;font-size:20px;">Últimos mensajes de la conversación</h2>{{range .Messages}}<div class="email-soft" style="margin:12px 0;padding:16px;background:#f9fafb;border:1px solid #e9eaec;border-radius:10px;"><p class="email-muted" style="margin:0 0 8px;color:#4b5563;font-size:12px;"><strong>{{.Author}}</strong> · {{.Timestamp}}</p><div class="email-text" style="color:#1c1c1c;white-space:pre-wrap;overflow-wrap:anywhere;font-size:14px;line-height:1.6;">{{.Text}}</div></div>{{end}}{{end}}
          {{if .HistoryNotice}}<p class="email-muted" style="color:#616161;font-size:13px;">{{.HistoryNotice}}</p>{{end}}
          {{if .Reference}}<p class="email-muted" style="margin:22px 0 0;color:#616161;font-size:12px;overflow-wrap:anywhere;">Referencia técnica: {{.Reference}}</p>{{end}}
          {{if .AttachmentNotice}}<p class="email-muted" style="margin:26px 0 0;color:#616161;font-size:13px;line-height:1.55;">El documento correspondiente se encuentra adjunto a este correo.</p>{{end}}
        </td></tr>
        <tr><td class="email-soft email-muted" style="background:#f9fafb;border-top:1px solid #e9eaec;padding:20px 34px;color:#616161;font-size:12px;line-height:1.5;">
          Correo automático de Giras Indómito. Por favor, no respondas a este mensaje.{{if .Stage}}<br><strong>{{.Stage}}</strong>{{end}}
        </td></tr>
      </table>
    </td></tr>
  </table>
</body>
</html>`))

// Render genera HTML escapando todo el contenido dinámico.
func Render(content Content) (string, error) {
	var output bytes.Buffer
	if err := brandedEmailTemplate.Execute(&output, content); err != nil {
		return "", err
	}
	return output.String(), nil
}
