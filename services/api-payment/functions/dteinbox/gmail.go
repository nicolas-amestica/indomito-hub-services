// Package dteinbox recibe XML tributarios desde Gmail sin modificar el buzón.
package dteinbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxMessageBytes = 20 * 1024 * 1024

var ErrMailGone = errors.New("mensaje Gmail eliminado")
var ErrInvalidPage = errors.New("paginación Gmail vencida")
var ErrMailSize = errors.New("correo Gmail supera límite de lectura")

// GmailConfig contiene OAuth y el filtro de recepción autorizado por ambiente.
type GmailConfig struct {
	Enabled      bool   `json:"enabled"`
	Mailbox      string `json:"mailbox"`
	Alias        string `json:"alias"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	RefreshToken string `json:"refreshToken"`
	StartDate    string `json:"startDate"`
}

// Gmail mantiene un token de acceso solo durante una ejecución.
type Gmail struct {
	HTTP  *http.Client
	token string
}

// Connect valida la identidad real del buzón autorizado.
func (g *Gmail) Connect(ctx context.Context, cfg GmailConfig) error {
	if cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RefreshToken == "" {
		return errors.New("OAuth Gmail no configurado")
	}
	form := url.Values{"client_id": {cfg.ClientID}, "client_secret": {cfg.ClientSecret}, "refresh_token": {cfg.RefreshToken}, "grant_type": {"refresh_token"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var payload struct {
		Token string `json:"access_token"`
	}
	if err = g.request(req, &payload); err != nil {
		return err
	}
	if payload.Token == "" {
		return errors.New("token Gmail ausente")
	}
	g.token = payload.Token
	var profile struct {
		Email string `json:"emailAddress"`
	}
	if err = g.get(ctx, "profile", nil, &profile); err != nil {
		return err
	}
	if !strings.EqualFold(profile.Email, cfg.Mailbox) {
		return errors.New("OAuth corresponde a otro buzón")
	}
	return nil
}

func (g *Gmail) request(req *http.Request, out any) error {
	response, err := g.HTTP.Do(req)
	if err != nil {
		return errors.New("Gmail no disponible")
	}
	defer func() { _ = response.Body.Close() }() // El cuerpo ya se consume y verifica antes de retornar.
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == 404 {
			return ErrMailGone
		}
		if response.StatusCode == 400 {
			return ErrInvalidPage
		}
		return fmt.Errorf("Gmail HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxMessageBytes+1))
	if err != nil {
		return errors.New("error al leer respuesta Gmail")
	}
	if len(raw) > maxMessageBytes {
		return ErrMailSize
	}
	return json.Unmarshal(raw, out)
}
func (g *Gmail) get(ctx context.Context, path string, query url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://gmail.googleapis.com/gmail/v1/users/me/"+path+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	return g.request(req, out)
}

// MessagePage pagina los candidatos sin depender de etiquetas ni estado leído.
type MessagePage struct {
	Messages []struct {
		ID string `json:"id"`
	} `json:"messages"`
	Next string `json:"nextPageToken"`
}

// List incluye archivados, spam y papelera; el destinatario se valida al leer MIME.
func (g *Gmail) List(ctx context.Context, after time.Time, page string) (MessagePage, error) {
	query := url.Values{"q": {"after:" + fmt.Sprint(after.Unix()) + " has:attachment"}, "includeSpamTrash": {"true"}, "maxResults": {"50"}}
	if page != "" {
		query.Set("pageToken", page)
	}
	var result MessagePage
	err := g.get(ctx, "messages", query, &result)
	return result, err
}

// Raw obtiene el correo original incluyendo encabezados y todos los adjuntos MIME.
func (g *Gmail) Raw(ctx context.Context, id string) (string, error) {
	var result struct {
		Raw string `json:"raw"`
	}
	err := g.get(ctx, "messages/"+url.PathEscape(id), url.Values{"format": {"raw"}}, &result)
	return result.Raw, err
}
