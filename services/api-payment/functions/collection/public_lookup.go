package collection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
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
	ConfigurationsTable string
	// CheckoutEnabled se habilita únicamente desde configuración segura del backend.
	// El valor cero mantiene el portal en modo consulta ante despliegues incompletos.
	CheckoutEnabled  bool
	RecaptchaSiteKey string
}

type publicLookupRequest struct {
	RUT      string `json:"rut"`
	TripCode string `json:"tripCode"`
	AccountID string `json:"accountId,omitempty"`
}

type MasterAccountOption struct { AccountID string `json:"accountId"`; TripID string `json:"tripId"`; Name string `json:"name"` }

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
	ReviewRequired   bool                `json:"reviewRequired"`
	ReviewAttemptID  string              `json:"reviewAttemptId,omitempty"`
	OpenAttemptID    string              `json:"openAttemptId,omitempty"`
	Session          *PassengerSession   `json:"session,omitempty"`
	Active           bool                `json:"active"`
	Free             bool                `json:"free"`
	CheckoutEnabled  bool                `json:"checkoutEnabled"`
	RecaptchaSiteKey string              `json:"recaptchaSiteKey,omitempty"`
	Installments     []PublicInstallment `json:"installments"`
	Accounts        []MasterAccountOption `json:"accounts,omitempty"`
}

type masterAccessConfig struct { Digest string `dynamodbav:"digest"`; Active bool `dynamodbav:"active"` }

func (a PublicApp) masterOptions(ctx context.Context, code, rut string) ([]MasterAccountOption, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) < 16 || len(code) > 64 || a.ConfigurationsTable == "" { return nil, ErrNotFound }
	out, err := a.Accounts.DB.GetItem(ctx,&dynamodb.GetItemInput{TableName:aws.String(a.ConfigurationsTable),Key:key("CONFIGURATION","PAYMENT_CODE"),ConsistentRead:aws.Bool(true)})
	if err != nil || len(out.Item)==0 { return nil, ErrNotFound }
	var config masterAccessConfig
	if attributevalue.UnmarshalMap(out.Item,&config)!=nil || !config.Active { return nil, ErrNotFound }
	digest:=sha256.Sum256([]byte(code)); if config.Digest != hex.EncodeToString(digest[:]) { return nil, ErrNotFound }
	rutKey,err:=paymentaccess.RUTKey(a.LookupSecret,rut);if err!=nil{return nil,ErrNotFound}
	db,ok:=a.Accounts.DB.(queryDatabase);if !ok{return nil,errors.New("consulta no disponible")}
	result,err:=db.Query(ctx,&dynamodb.QueryInput{TableName:&a.Accounts.Table,KeyConditionExpression:aws.String("pk = :pk AND begins_with(sk, :prefix)"),ExpressionAttributeValues:map[string]types.AttributeValue{":pk":&types.AttributeValueMemberS{Value:"ADMIN_"+rutKey},":prefix":&types.AttributeValueMemberS{Value:"ACCOUNT#"}},ConsistentRead:aws.Bool(true),Limit:aws.Int32(25)})
	if err!=nil{return nil,err}
	options:=make([]MasterAccountOption,0,len(result.Items));for _,raw:=range result.Items{var row record;if attributevalue.UnmarshalMap(raw,&row)==nil&&row.AccountID!=""&&row.TripID!=""{options=append(options,MasterAccountOption{AccountID:row.AccountID,TripID:row.TripID,Name:row.PassengerName})}}
	if len(options)==0{return nil,ErrNotFound};return options,nil
}

func publicAccountView(account collection.Account, checkoutEnabled bool, recaptchaSiteKey ...string) PublicAccount {
	view := PublicAccount{Active: account.Active, Free: account.Free, OpenAttemptID: account.OpenAttemptID, CheckoutEnabled: checkoutEnabled, Installments: []PublicInstallment{}}
	if checkoutEnabled && len(recaptchaSiteKey) == 1 {
		view.RecaptchaSiteKey = recaptchaSiteKey[0]
	}
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
	if len(req.Body) > 2048 || decodeAdmin(req, &body) != nil || len(body.RUT) > 20 || len(body.TripCode) > 64 {
		return lookupFailure(req, apperr.CodeResourceNotFound)
	}
	// Normalizar separadores también para los límites: cambiar el formato no reinicia el contador.
	identity := strings.ToUpper(strings.NewReplacer(".", "", "-", "", " ", "").Replace(body.RUT)) + ":" + strings.ToUpper(strings.TrimSpace(body.TripCode))
	if err = a.consumeAttempt(ctx, "lookup-pair:v1", identity, 5, time.Now().UTC()); err != nil {
		return lookupFailure(req, apperr.CodeForbidden)
	}
	var account collection.Account
	masterOptions, masterErr := a.masterOptions(ctx, body.TripCode, body.RUT)
	if masterErr == nil {
		if body.AccountID == "" && len(masterOptions) > 1 { return lambdautil.SuccessResponseWithHeaders(200, PublicAccount{Accounts:masterOptions,Installments:[]PublicInstallment{}}, map[string]string{"cache-control":"no-store","referrer-policy":"no-referrer"}) }
		selected:=body.AccountID;if selected==""{selected=masterOptions[0].AccountID};allowed:=false;for _,option:=range masterOptions{if option.AccountID==selected{allowed=true;break}};if !allowed{return lookupFailure(req,apperr.CodeResourceNotFound)}
		account,err=a.Accounts.GetAccount(ctx,selected)
	} else {
		account, err = a.Resolve(ctx, body.TripCode, body.RUT)
	}
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return lookupFailure(req, apperr.CodeUpstreamServiceError)
		}
		return lookupFailure(req, apperr.CodeResourceNotFound)
	}
	view := publicAccountView(account, a.CheckoutEnabled, a.RecaptchaSiteKey)
	if a.SessionSecret != "" {
		codeKey, keyErr := paymentaccess.CodeKey(a.LookupSecret, body.TripCode)
		if keyErr != nil {
			approval, approvalErr := a.Accounts.read(ctx, "TRIP#"+account.TripID, "APPROVAL")
			if approvalErr != nil || !passengerCodeKey.MatchString(approval.CodeKey) { return lookupFailure(req, apperr.CodeUpstreamServiceError) }
			codeKey = approval.CodeKey
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
