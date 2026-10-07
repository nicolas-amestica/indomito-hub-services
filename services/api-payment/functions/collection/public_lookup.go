package collection

import (
	"context"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/paymentaccess"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

// PublicApp consulta únicamente una participación; no comparte credenciales administrativas.
type PublicApp struct {
	Accounts      Service
	LookupSecret  string
	SessionSecret string
	// CheckoutEnabled se habilita únicamente desde configuración segura del backend.
	// El valor cero mantiene el portal en modo consulta ante despliegues incompletos.
	CheckoutEnabled bool
}

type publicLookupRequest struct {
	RUT      string `json:"rut"`
	TripCode string `json:"tripCode"`
}

// PublicInstallment excluye identidad, motivos internos de descuentos, banco y referencias de terceros.
type PublicInstallment struct {
	Status      string `json:"status"`
	Number      int    `json:"number"`
	DueDate     string `json:"dueDate"`
	Amount      int64  `json:"amount"`
	Paid        int64  `json:"paid"`
	Outstanding int64  `json:"outstanding"`
}

// PublicAccount es una vista de consulta; esta etapa todavía no habilita checkout.
type PublicAccount struct {
	ReviewRequired  bool                `json:"reviewRequired"`
	ReviewAttemptID string              `json:"reviewAttemptId,omitempty"`
	OpenAttemptID   string              `json:"openAttemptId,omitempty"`
	Session         *PassengerSession   `json:"session,omitempty"`
	Active          bool                `json:"active"`
	Free            bool                `json:"free"`
	CheckoutEnabled bool                `json:"checkoutEnabled"`
	Installments    []PublicInstallment `json:"installments"`
}

func publicAccountView(account collection.Account, checkoutEnabled bool) PublicAccount {
	view := PublicAccount{Active: account.Active, Free: account.Free, OpenAttemptID: account.OpenAttemptID, CheckoutEnabled: checkoutEnabled, Installments: []PublicInstallment{}}
	view.ReviewRequired = account.RequiresPaymentReview()
	view.ReviewAttemptID = account.ReviewAttemptID
	for i, quota := range account.Installments {
		status := "PENDING"
		if quota.Outstanding() == 0 {
			status = "ADJUSTED"
			if quota.Paid > 0 && quota.Cancelled == 0 {
				status = "PAID"
			}
		}
		view.Installments = append(view.Installments, PublicInstallment{Status: status, Number: i + 1, DueDate: quota.DueDate, Amount: quota.Original - quota.Discount - quota.Cancelled, Paid: quota.Paid, Outstanding: quota.Outstanding()})
	}
	return view
}

// Resolve comprueba código vigente, relación por RUT y publicación completa con cuatro GetItem.
func (a PublicApp) Resolve(ctx context.Context, code, rut string) (collection.Account, error) {
	codeKey, err := paymentaccess.CodeKey(a.LookupSecret, code)
	if err != nil {
		return collection.Account{}, ErrNotFound
	}
	rutKey, err := paymentaccess.RUTKey(a.LookupSecret, rut)
	if err != nil {
		return collection.Account{}, ErrNotFound
	}
	codeRow, err := a.Accounts.read(ctx, codeKey, "META")
	if err != nil {
		return collection.Account{}, err
	}
	if codeRow.Status != "RESERVED_APPROVED" || codeRow.TripID == "" {
		return collection.Account{}, ErrNotFound
	}
	link, err := a.Accounts.read(ctx, "TRIP#"+codeRow.TripID, rutKey)
	if err != nil {
		return collection.Account{}, err
	}
	if link.AccountID == "" || link.TripID != codeRow.TripID {
		return collection.Account{}, ErrNotFound
	}
	account, err := a.Accounts.GetAccount(ctx, link.AccountID)
	if err != nil {
		return collection.Account{}, err
	}
	if account.TripID != codeRow.TripID {
		return collection.Account{}, ErrNotFound
	}
	return account, nil
}

// consumeAttempt usa CAS por ventana fija. TTL limpia contadores, nunca libera un bloqueo financiero.
func (a PublicApp) consumeAttempt(ctx context.Context, purpose, value string, limit int64, now time.Time) error {
	digest, err := paymentaccess.Digest(a.LookupSecret, purpose, value)
	if err != nil {
		return err
	}
	window := now.Unix() / 60
	pk := "RATE#" + digest
	sk := strconv.FormatInt(window, 10)
	for retry := 0; retry < 3; retry++ {
		row, readErr := a.Accounts.read(ctx, pk, sk)
		condition := "attribute_not_exists(pk)"
		var values map[string]types.AttributeValue
		if errors.Is(readErr, ErrNotFound) {
			row = record{PK: pk, SK: sk, Version: 1, Count: 1, ExpiresAt: (window + 10) * 60}
		} else if readErr != nil {
			return readErr
		} else {
			if row.Count >= limit {
				return collection.ErrConflict
			}
			values = versionValue(row.Version)
			condition = "#version = :previous"
			row.Version++
			row.Count++
		}
		write, err := a.Accounts.put(row, condition, values)
		if err != nil {
			return err
		}
		_, err = a.Accounts.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{write}})
		if err == nil {
			return nil
		}
		var conflict *types.TransactionCanceledException
		if !errors.As(err, &conflict) {
			return err
		} // Fallar cerrado ante timeout, no duplicar el consumo a ciegas.
	}
	return collection.ErrConflict
}

func networkIdentity(source string) (string, error) {
	ip, err := netip.ParseAddr(source)
	if err != nil {
		return "", err
	}
	ip = ip.Unmap()
	if ip.Is6() {
		return netip.PrefixFrom(ip, 64).Masked().String(), nil
	}
	return ip.String(), nil
}

// HandleLookup limita intentos antes de buscar y responde igual ante RUT/código inexistente o inválido.
func (a PublicApp) HandleLookup(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if req.RequestContext.HTTP.Method != "POST" {
		return lookupFailure(req, apperr.CodeForbidden)
	}
	ip, err := networkIdentity(req.RequestContext.HTTP.SourceIP)
	if err != nil {
		return lookupFailure(req, apperr.CodeForbidden)
	}
	if err = a.consumeAttempt(ctx, "lookup-ip:v1", ip, 10, time.Now().UTC()); err != nil {
		return lookupFailure(req, apperr.CodeForbidden)
	}
	var body publicLookupRequest
	if len(req.Body) > 2048 || decodeAdmin(req, &body) != nil || len(body.RUT) > 20 || len(body.TripCode) > 12 {
		return lookupFailure(req, apperr.CodeResourceNotFound)
	}
	// Normalizar separadores también para los límites: cambiar el formato no reinicia el contador.
	identity := strings.ToUpper(strings.NewReplacer(".", "", "-", "", " ", "").Replace(body.RUT)) + ":" + strings.ToUpper(strings.TrimSpace(body.TripCode))
	if err = a.consumeAttempt(ctx, "lookup-pair:v1", identity, 5, time.Now().UTC()); err != nil {
		return lookupFailure(req, apperr.CodeForbidden)
	}
	account, err := a.Resolve(ctx, body.TripCode, body.RUT)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return lookupFailure(req, apperr.CodeUpstreamServiceError)
		}
		return lookupFailure(req, apperr.CodeResourceNotFound)
	}
	view := publicAccountView(account, a.CheckoutEnabled)
	if a.SessionSecret != "" {
		codeKey, keyErr := paymentaccess.CodeKey(a.LookupSecret, body.TripCode)
		if keyErr != nil {
			return lookupFailure(req, apperr.CodeUpstreamServiceError)
		}
		session, sessionErr := issuePassengerSession(a.SessionSecret, account.ID, account.TripID, codeKey, time.Now().UTC())
		if sessionErr != nil {
			return lookupFailure(req, apperr.CodeUpstreamServiceError)
		}
		view.Session = &session
	}
	return lambdautil.SuccessResponseWithHeaders(200, view, map[string]string{"cache-control": "no-store", "referrer-policy": "no-referrer"})
}

func lookupFailure(req events.APIGatewayV2HTTPRequest, code string) (events.APIGatewayV2HTTPResponse, error) {
	response, err := lambdautil.ErrorResponse(req, apperr.New(code, "No fue posible consultar las cuotas. Verifique los datos o intente más tarde."))
	if response.Headers == nil {
		response.Headers = map[string]string{}
	}
	response.Headers["cache-control"] = "no-store"
	response.Headers["referrer-policy"] = "no-referrer"
	return response, err
}
