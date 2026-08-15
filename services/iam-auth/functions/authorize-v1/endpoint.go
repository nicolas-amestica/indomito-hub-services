package authorizev1

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/services/iam-auth/functions"
)

// Handle procesa la solicitud de autorización del API Gateway HTTP API v2.
// Retorna una policy IAM permitiendo o denegando el acceso.
func Handle(ctx context.Context, req events.APIGatewayV2CustomAuthorizerV1Request) (events.APIGatewayCustomAuthorizerResponse, error) {
	app, err := functions.GetApp(ctx)
	if err != nil {
		return denyResponse(req.MethodArn, "internal_error"), nil
	}

	log := functions.GetLogger()
	cfg := functions.LoadConfig(app)

	token := extractToken(req.AuthorizationToken)
	if token == "" {
		log.Warn("authorize_missing_token")
		return denyResponse(req.MethodArn, "missing_token"), nil
	}

	claims, err := validateJWT(token, cfg.JWTSecret)
	if err != nil {
		log.Warn("authorize_invalid_token", zap.Error(err))
		return denyResponse(req.MethodArn, "invalid_token"), nil
	}

	sub, _ := claims["sub"].(string)
	if sub == "" {
		log.Warn("authorize_missing_sub")
		return denyResponse(req.MethodArn, "missing_subject"), nil
	}

	log.Info("authorize_success", zap.String("sub", sub))
	return allowResponse(req.MethodArn, sub, claims), nil
}

// extractToken extrae el token del header Authorization (Bearer scheme).
func extractToken(authHeader string) string {
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}

	return strings.TrimSpace(authHeader)
}

// validateJWT valida un JWT con firma HMAC-SHA256 y retorna los claims del payload.
func validateJWT(token string, secret string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errInvalidToken
	}

	// Verificar firma HMAC-SHA256
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(parts[2]), []byte(expectedSig)) {
		return nil, errInvalidSignature
	}

	// Decodificar payload
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errInvalidPayload
	}

	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, errInvalidPayload
	}

	return claims, nil
}

// allowResponse genera una policy IAM que permite el acceso.
func allowResponse(methodArn string, principalID string, claims map[string]any) events.APIGatewayCustomAuthorizerResponse {
	ctx := map[string]interface{}{}
	if sub, ok := claims["sub"].(string); ok {
		ctx["sub"] = sub
	}
	if email, ok := claims["email"].(string); ok {
		ctx["email"] = email
	}
	if role, ok := claims["role"].(string); ok {
		ctx["role"] = role
	}

	return events.APIGatewayCustomAuthorizerResponse{
		PrincipalID: principalID,
		PolicyDocument: events.APIGatewayCustomAuthorizerPolicy{
			Version: "2012-10-17",
			Statement: []events.IAMPolicyStatement{
				{
					Action:   []string{"execute-api:Invoke"},
					Effect:   "Allow",
					Resource: []string{buildResourceArn(methodArn)},
				},
			},
		},
		Context: ctx,
	}
}

// denyResponse genera una policy IAM que deniega el acceso.
func denyResponse(methodArn string, reason string) events.APIGatewayCustomAuthorizerResponse {
	return events.APIGatewayCustomAuthorizerResponse{
		PrincipalID: "unauthorized",
		PolicyDocument: events.APIGatewayCustomAuthorizerPolicy{
			Version: "2012-10-17",
			Statement: []events.IAMPolicyStatement{
				{
					Action:   []string{"execute-api:Invoke"},
					Effect:   "Deny",
					Resource: []string{buildResourceArn(methodArn)},
				},
			},
		},
		Context: map[string]interface{}{
			"reason": reason,
		},
	}
}

// buildResourceArn genera el ARN wildcard para cubrir todos los endpoints del API.
func buildResourceArn(methodArn string) string {
	parts := strings.Split(methodArn, "/")
	if len(parts) >= 2 {
		return strings.Join(parts[:2], "/") + "/*"
	}

	return methodArn
}

var (
	errInvalidToken     = &authError{msg: "invalid token format"}
	errInvalidSignature = &authError{msg: "invalid signature"}
	errInvalidPayload   = &authError{msg: "invalid payload"}
)

type authError struct {
	msg string
}

func (e *authError) Error() string {
	return e.msg
}
