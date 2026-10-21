package functions

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

const maxProfilePhotoBytes = 2 * 1024 * 1024

type profilePhotoStore interface {
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

func getProfilePhoto(ctx context.Context, app *App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	userID, ok := authenticatedUserID(req)
	if !ok {
		return fail(401, "UNAUTHORIZED", "No fue posible identificar al usuario")
	}
	body, contentType, err := readProfilePhoto(ctx, app, userID)
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NoSuchKey" || apiErr.ErrorCode() == "NotFound") {
			return fail(404, "PHOTO_NOT_FOUND", "El usuario no tiene una foto de perfil")
		}
		return fail(500, "INTERNAL_ERROR", "No fue posible cargar la foto de perfil")
	}
	return events.APIGatewayV2HTTPResponse{StatusCode: 200, Headers: map[string]string{"content-type": contentType, "cache-control": "private, max-age=300"}, Body: base64.StdEncoding.EncodeToString(body), IsBase64Encoded: true}, nil
}

func loadProfilePhoto(ctx context.Context, app *App, userID string) (string, error) {
	body, contentType, err := readProfilePhoto(ctx, app, userID)
	if err != nil {
		return "", err
	}
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(body), nil
}

func readProfilePhoto(ctx context.Context, app *App, userID string) ([]byte, string, error) {
	if app.ProfilePhotos == nil || app.PhotosBucket == "" {
		return nil, "", errors.New("almacenamiento de fotos no configurado")
	}
	result, err := app.ProfilePhotos.GetObject(ctx, &s3.GetObjectInput{Bucket: &app.PhotosBucket, Key: aws.String(profilePhotoKey(userID))})
	if err != nil {
		return nil, "", err
	}
	defer result.Body.Close()
	body, err := io.ReadAll(io.LimitReader(result.Body, maxProfilePhotoBytes+1))
	if err != nil || len(body) > maxProfilePhotoBytes {
		return nil, "", errors.New("foto de perfil inválida")
	}
	contentType := "image/jpeg"
	if result.ContentType != nil && allowedProfilePhotoType(*result.ContentType) {
		contentType = *result.ContentType
	}
	return body, contentType, nil
}

func putProfilePhoto(ctx context.Context, app *App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	userID, ok := authenticatedUserID(req)
	if !ok {
		return fail(401, "UNAUTHORIZED", "No fue posible identificar al usuario")
	}
	contentType := strings.ToLower(strings.TrimSpace(req.Headers["content-type"]))
	if !allowedProfilePhotoType(contentType) {
		return fail(415, "UNSUPPORTED_MEDIA_TYPE", "La foto debe ser JPEG, PNG o WebP")
	}
	body := []byte(req.Body)
	if req.IsBase64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(req.Body)
		if err != nil {
			return fail(400, "VALIDATION_ERROR", "La foto no es válida")
		}
		body = decoded
	}
	if len(body) == 0 || len(body) > maxProfilePhotoBytes {
		return fail(400, "VALIDATION_ERROR", "La foto debe pesar como máximo 2 MB")
	}
	_, err := app.ProfilePhotos.PutObject(ctx, &s3.PutObjectInput{Bucket: &app.PhotosBucket, Key: aws.String(profilePhotoKey(userID)), Body: bytes.NewReader(body), ContentType: aws.String(contentType), CacheControl: aws.String("private, max-age=300"), ServerSideEncryption: "AES256"})
	if err != nil {
		return fail(500, "INTERNAL_ERROR", "No fue posible guardar la foto de perfil")
	}
	return response(200, map[string]bool{"updated": true})
}

func deleteProfilePhoto(ctx context.Context, app *App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	userID, ok := authenticatedUserID(req)
	if !ok {
		return fail(401, "UNAUTHORIZED", "No fue posible identificar al usuario")
	}
	_, err := app.ProfilePhotos.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &app.PhotosBucket, Key: aws.String(profilePhotoKey(userID))})
	if err != nil {
		return fail(500, "INTERNAL_ERROR", "No fue posible eliminar la foto de perfil")
	}
	return response(200, map[string]bool{"deleted": true})
}

func authenticatedUserID(req events.APIGatewayV2HTTPRequest) (string, bool) {
	if req.RequestContext.Authorizer == nil {
		return "", false
	}
	userID, ok := req.RequestContext.Authorizer.Lambda["userId"].(string)
	userID = strings.TrimSpace(userID)
	return userID, ok && userID != "" && !strings.ContainsAny(userID, "/\\")
}

func profilePhotoKey(userID string) string { return "profile-photos/" + userID }

func allowedProfilePhotoType(contentType string) bool {
	switch contentType {
	case "image/jpeg", "image/png", "image/webp":
		return true
	default:
		return false
	}
}
