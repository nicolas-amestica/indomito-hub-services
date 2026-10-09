package functions

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	gomail "github.com/wneessen/go-mail"
	"ind-hub-api-gox-sls-pri-gh/services/api-identity/domain"
)

const (
	passwordResetLifetime    = 20 * time.Minute
	passwordResetMinLength   = 12
	passwordResetMaxAttempts = 5
)

type passwordResetEmailConfig struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	User      string `json:"user"`
	Password  string `json:"password"`
	From      string `json:"from"`
	PortalURL string `json:"portalUrl"`
}

func requestPasswordReset(ctx context.Context, a *App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	startedAt := time.Now()
	var input struct {
		Email string `json:"email"`
	}
	if json.Unmarshal([]byte(req.Body), &input) != nil || len(input.Email) > 254 {
		return fail(400, "VALIDATION_ERROR", "Ingresa un correo válido")
	}
	email := strings.ToLower(strings.TrimSpace(input.Email))
	parsedEmail, err := mail.ParseAddress(email)
	if err != nil || parsedEmail.Address != email || !strings.Contains(email, "@") {
		return fail(400, "VALIDATION_ERROR", "Ingresa un correo válido")
	}

	if !allowPasswordResetAttempt(ctx, a, req.RequestContext.HTTP.SourceIP) {
		return passwordResetRequested(ctx, startedAt)
	}

	var alias domain.LoginAlias
	if get(ctx, a, "LOGIN#"+domain.NormalizeLogin(email), "IDENTITY", &alias) != nil {
		return passwordResetRequested(ctx, startedAt)
	}
	var user domain.User
	if get(ctx, a, "USER#"+alias.UserID, "IDENTITY", &user) != nil || !user.Active || !strings.EqualFold(user.Email, email) {
		return passwordResetRequested(ctx, startedAt)
	}

	token, tokenHash, err := newPasswordResetToken(user.ID)
	if err != nil {
		return fail(500, "INTERNAL_ERROR", "No fue posible procesar la solicitud")
	}
	now := time.Now().UTC()
	_, err = a.DDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &a.Table,
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: "USER#" + user.ID},
			"sk": &types.AttributeValueMemberS{Value: "IDENTITY"},
		},
		UpdateExpression:    aws.String("SET passwordResetHash = :hash, passwordResetExpiresAt = :expires, passwordResetAttempts = :zero, passwordResetRequestedAt = :now"),
		ConditionExpression: aws.String("active = :active AND (attribute_not_exists(passwordResetRequestedAt) OR passwordResetRequestedAt < :cooldown)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":hash":     &types.AttributeValueMemberS{Value: tokenHash},
			":expires":  &types.AttributeValueMemberN{Value: fmt.Sprint(now.Add(passwordResetLifetime).Unix())},
			":zero":     &types.AttributeValueMemberN{Value: "0"},
			":now":      &types.AttributeValueMemberN{Value: fmt.Sprint(now.Unix())},
			":cooldown": &types.AttributeValueMemberN{Value: fmt.Sprint(now.Add(-time.Minute).Unix())},
			":active":   &types.AttributeValueMemberBOOL{Value: true},
		},
	})
	if err != nil {
		return passwordResetRequested(ctx, startedAt)
	}

	config, err := loadPasswordResetEmailConfig(ctx, a)
	if err != nil || sendPasswordResetEmail(ctx, config, user.Email, user.Name, token) != nil {
		log.Printf("password reset email delivery failed userId=%s", user.ID)
	}
	return passwordResetRequested(ctx, startedAt)
}

func resetPassword(ctx context.Context, a *App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	var input struct {
		Token       string `json:"token"`
		NewPassword string `json:"newPassword"`
	}
	if json.Unmarshal([]byte(req.Body), &input) != nil || len(input.NewPassword) < passwordResetMinLength || len(input.NewPassword) > 128 {
		return fail(400, "VALIDATION_ERROR", "La nueva clave debe tener entre 12 y 128 caracteres")
	}
	if !allowPasswordResetAttempt(ctx, a, req.RequestContext.HTTP.SourceIP) {
		return fail(429, "TOO_MANY_REQUESTS", "Espera unos minutos antes de volver a intentarlo")
	}

	userID, ok := passwordResetUserID(input.Token)
	if !ok {
		return fail(400, "INVALID_OR_EXPIRED_TOKEN", "El enlace no es válido o ya expiró")
	}
	var user domain.User
	if get(ctx, a, "USER#"+userID, "IDENTITY", &user) != nil || !validPasswordResetToken(user, input.Token, time.Now().Unix()) {
		incrementPasswordResetAttempts(ctx, a, userID)
		return fail(400, "INVALID_OR_EXPIRED_TOKEN", "El enlace no es válido o ya expiró")
	}
	if verifyPassword(user.PasswordHash, input.NewPassword) {
		return fail(400, "PASSWORD_REUSED", "La nueva clave debe ser distinta de la actual")
	}
	hash, err := protectPassword(input.NewPassword)
	if err != nil {
		return fail(500, "INTERNAL_ERROR", "No fue posible proteger la nueva clave")
	}
	tokenHash := hashPasswordResetToken(input.Token)
	_, err = a.DDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &a.Table,
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: "USER#" + userID},
			"sk": &types.AttributeValueMemberS{Value: "IDENTITY"},
		},
		UpdateExpression:    aws.String("SET passwordHash = :password REMOVE passwordResetHash, passwordResetExpiresAt, passwordResetAttempts, passwordResetRequestedAt"),
		ConditionExpression: aws.String("passwordResetHash = :tokenHash AND passwordResetExpiresAt >= :now AND passwordResetAttempts < :limit AND active = :active"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":password":  &types.AttributeValueMemberS{Value: hash},
			":tokenHash": &types.AttributeValueMemberS{Value: tokenHash},
			":now":       &types.AttributeValueMemberN{Value: fmt.Sprint(time.Now().Unix())},
			":limit":     &types.AttributeValueMemberN{Value: fmt.Sprint(passwordResetMaxAttempts)},
			":active":    &types.AttributeValueMemberBOOL{Value: true},
		},
	})
	if err != nil {
		return fail(400, "INVALID_OR_EXPIRED_TOKEN", "El enlace no es válido o ya expiró")
	}
	return response(200, map[string]bool{"passwordReset": true})
}

func passwordResetRequested(ctx context.Context, startedAt time.Time) (events.APIGatewayV2HTTPResponse, error) {
	if remaining := 2*time.Second - time.Since(startedAt); remaining > 0 {
		timer := time.NewTimer(remaining)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
	}
	return response(202, map[string]string{"message": "Si el correo está registrado, enviaremos un enlace para restablecer la clave."})
}

func newPasswordResetToken(userID string) (string, string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", "", fmt.Errorf("generar token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString([]byte(userID)) + "." + base64.RawURLEncoding.EncodeToString(secret)
	return token, hashPasswordResetToken(token), nil
}

func passwordResetUserID(token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || len(parts[1]) != 43 {
		return "", false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(decoded) == 0 || len(decoded) > 64 {
		return "", false
	}
	return string(decoded), true
}

func hashPasswordResetToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func validPasswordResetToken(user domain.User, token string, now int64) bool {
	want, err := hex.DecodeString(user.PasswordResetHash)
	got, gotErr := hex.DecodeString(hashPasswordResetToken(token))
	return err == nil && gotErr == nil && len(want) == len(got) && subtle.ConstantTimeCompare(want, got) == 1 && user.Active && user.PasswordResetExpiresAt >= now && user.PasswordResetAttempts < passwordResetMaxAttempts
}

func incrementPasswordResetAttempts(ctx context.Context, a *App, userID string) {
	if userID == "" {
		return
	}
	_, _ = a.DDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 &a.Table,
		Key:                       map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "USER#" + userID}, "sk": &types.AttributeValueMemberS{Value: "IDENTITY"}},
		UpdateExpression:          aws.String("ADD passwordResetAttempts :one"),
		ConditionExpression:       aws.String("attribute_exists(passwordResetHash) AND passwordResetAttempts < :limit"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":one": &types.AttributeValueMemberN{Value: "1"}, ":limit": &types.AttributeValueMemberN{Value: fmt.Sprint(passwordResetMaxAttempts)}},
	})
}

func allowPasswordResetAttempt(ctx context.Context, a *App, sourceIP string) bool {
	window := time.Now().UTC().Truncate(10 * time.Minute)
	key := sha256.Sum256([]byte(strings.TrimSpace(sourceIP)))
	_, err := a.DDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 &a.Table,
		Key:                       map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "AUTH_RATE#PASSWORD_RESET#" + hex.EncodeToString(key[:])}, "sk": &types.AttributeValueMemberS{Value: window.Format(time.RFC3339)}},
		UpdateExpression:          aws.String("ADD attempts :one SET expiresAt = :expires"),
		ConditionExpression:       aws.String("attribute_not_exists(attempts) OR attempts < :limit"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":one": &types.AttributeValueMemberN{Value: "1"}, ":limit": &types.AttributeValueMemberN{Value: "10"}, ":expires": &types.AttributeValueMemberN{Value: fmt.Sprint(window.Add(20 * time.Minute).Unix())}},
	})
	return err == nil
}

func loadPasswordResetEmailConfig(ctx context.Context, a *App) (passwordResetEmailConfig, error) {
	output, err := a.SSM.GetParameter(ctx, &ssm.GetParameterInput{Name: &a.ResetEmailParam, WithDecryption: aws.Bool(true)})
	if err != nil {
		return passwordResetEmailConfig{}, fmt.Errorf("leer configuración de correo: %w", err)
	}
	if output.Parameter == nil || output.Parameter.Value == nil {
		return passwordResetEmailConfig{}, errors.New("configuración de correo vacía")
	}
	var config passwordResetEmailConfig
	if json.Unmarshal([]byte(*output.Parameter.Value), &config) != nil || config.Host == "" || (config.Port != 465 && config.Port != 587) || config.User == "" || config.Password == "" || config.From == "" || strings.ContainsAny(config.Host+config.User+config.From, "\r\n") {
		return passwordResetEmailConfig{}, errors.New("configuración de correo inválida")
	}
	from, err := mail.ParseAddress(config.From)
	portal, portalErr := url.Parse(config.PortalURL)
	if err != nil || from.Address == "" || portalErr != nil || portal.Scheme != "https" || portal.Host == "" || portal.RawQuery != "" || portal.Fragment != "" {
		return passwordResetEmailConfig{}, errors.New("configuración de correo inválida")
	}
	return config, nil
}

func sendPasswordResetEmail(ctx context.Context, config passwordResetEmailConfig, to, name, token string) error {
	toAddress, err := mail.ParseAddress(to)
	if err != nil || toAddress.Address != to || strings.ContainsAny(to, "\r\n") {
		return errors.New("correo de destino inválido")
	}
	query := url.Values{}
	query.Set("token", token)
	resetURL := strings.TrimRight(config.PortalURL, "/") + "/restablecer-clave?" + query.Encode()
	message := gomail.NewMsg(gomail.WithNoDefaultUserAgent())
	if err = message.From(config.From); err != nil {
		return fmt.Errorf("configurar remitente: %w", err)
	}
	if err = message.To(to); err != nil {
		return fmt.Errorf("configurar destinatario: %w", err)
	}
	message.Subject("Giras Indómito: restablece tu clave")
	message.SetBodyString(gomail.TypeTextPlain, fmt.Sprintf("Hola %s,\n\nRecibimos una solicitud para restablecer tu clave de Indómito Hub. El enlace es válido por 20 minutos y solo puede usarse una vez:\n\n%s\n\nSi no solicitaste este cambio, ignora este correo. Tu clave actual seguirá vigente.", name, resetURL))
	options := []gomail.Option{gomail.WithPort(config.Port), gomail.WithUsername(config.User), gomail.WithPassword(config.Password), gomail.WithSMTPAuth(gomail.SMTPAuthPlain), gomail.WithTLSConfig(&tls.Config{ServerName: config.Host, MinVersion: tls.VersionTLS12}), gomail.WithTimeout(15 * time.Second)}
	if config.Port == 465 {
		options = append(options, gomail.WithSSL())
	} else {
		options = append(options, gomail.WithTLSPolicy(gomail.TLSMandatory))
	}
	client, err := gomail.NewClient(config.Host, options...)
	if err != nil {
		return fmt.Errorf("configurar SMTP: %w", err)
	}
	if err = client.DialAndSendWithContext(ctx, message); err != nil {
		return fmt.Errorf("enviar SMTP: %w", err)
	}
	return nil
}
