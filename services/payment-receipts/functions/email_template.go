package functions

import (
	"bytes"
	"html/template"
)

type emailDetail struct {
	Label string
	Value string
}

type brandedEmail struct {
	Preheader string
	Eyebrow   string
	Title     string
	Lead      string
	Body      string
	Notice    string
	Details   []emailDetail
	Stage     string
}

var brandedEmailTemplate = template.Must(template.New("branded-email").Parse(`<!doctype html>
<html lang="es">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title></head>
<body style="margin:0;background:#f3f4f6;color:#171717;font-family:Arial,Helvetica,sans-serif;">
  <div style="display:none;max-height:0;overflow:hidden;opacity:0;color:transparent;">{{.Preheader}}</div>
  <table role="presentation" width="100%" cellspacing="0" cellpadding="0" style="background:#f3f4f6;padding:28px 12px;">
    <tr><td align="center">
      <table role="presentation" width="100%" cellspacing="0" cellpadding="0" style="max-width:620px;background:#ffffff;border:1px solid #e5e7eb;border-radius:18px;overflow:hidden;box-shadow:0 10px 30px rgba(17,24,39,.08);">
        <tr><td style="height:7px;background:#d9f20c;font-size:0;line-height:0;">&nbsp;</td></tr>
        <tr><td style="background:#111827;padding:28px 34px;color:#ffffff;">
          <div style="font-size:20px;font-weight:800;letter-spacing:.2px;">Giras Indómito</div>
          <div style="margin-top:5px;color:#d1d5db;font-size:13px;">Dale una vuelta a la aventura</div>
        </td></tr>
        <tr><td style="padding:34px;">
          <div style="color:#667100;font-size:12px;font-weight:800;letter-spacing:1.4px;text-transform:uppercase;">{{.Eyebrow}}</div>
          <h1 style="margin:10px 0 14px;color:#111827;font-size:28px;line-height:1.2;">{{.Title}}</h1>
          <p style="margin:0;color:#374151;font-size:17px;line-height:1.6;">{{.Lead}}</p>
          {{if .Details}}
          <table role="presentation" width="100%" cellspacing="0" cellpadding="0" style="margin-top:26px;background:#f9fafb;border:1px solid #e5e7eb;border-radius:12px;">
            {{range .Details}}<tr>
              <td style="padding:12px 16px;border-bottom:1px solid #e5e7eb;color:#6b7280;font-size:13px;width:34%;">{{.Label}}</td>
              <td style="padding:12px 16px;border-bottom:1px solid #e5e7eb;color:#111827;font-size:14px;font-weight:700;word-break:break-word;">{{.Value}}</td>
            </tr>{{end}}
          </table>
          {{end}}
          <p style="margin:26px 0 0;color:#374151;font-size:15px;line-height:1.65;">{{.Body}}</p>
          {{if .Notice}}<div style="margin-top:22px;padding:16px 18px;border-left:4px solid #d9f20c;background:#f7fbe8;color:#374151;font-size:14px;line-height:1.55;">{{.Notice}}</div>{{end}}
          <p style="margin:26px 0 0;color:#6b7280;font-size:13px;line-height:1.55;">El documento correspondiente se encuentra adjunto a este correo.</p>
        </td></tr>
        <tr><td style="background:#f9fafb;border-top:1px solid #e5e7eb;padding:20px 34px;color:#6b7280;font-size:12px;line-height:1.5;">
          Correo automático de Giras Indómito. Por favor, no respondas a este mensaje.{{if .Stage}}<br><strong>{{.Stage}}</strong>{{end}}
        </td></tr>
      </table>
    </td></tr>
  </table>
</body>
</html>`))

func renderBrandedEmail(content brandedEmail) (string, error) {
	var output bytes.Buffer
	if err := brandedEmailTemplate.Execute(&output, content); err != nil {
		return "", err
	}
	return output.String(), nil
}
