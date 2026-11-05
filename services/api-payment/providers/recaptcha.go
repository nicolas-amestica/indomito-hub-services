package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var recaptchaProjectID = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

const recaptchaPrivacyFallbackScore = 0.3

// RecaptchaConfig contiene la configuración server-side. APIKey nunca debe serializarse al cliente.
type RecaptchaConfig struct {
	ProjectID        string   `json:"projectId"`
	SiteKey          string   `json:"siteKey"`
	APIKey           string   `json:"apiKey"`
	AllowedHostnames []string `json:"allowedHostnames"`
	MinimumScore     float64  `json:"minimumScore"`
}

// Recaptcha valida tokens score-based con Google antes de crear un checkout.
type Recaptcha struct {
	config RecaptchaConfig
	client *http.Client
}

// RecaptchaAssessmentError clasifica rechazos sin exponer el token ni secretos.
type RecaptchaAssessmentError struct{ Code string }

func (e *RecaptchaAssessmentError) Error() string {
	return "evaluación reCAPTCHA rechazada: " + e.Code
}

func NewRecaptcha(config RecaptchaConfig) (*Recaptcha, error) {
	config.ProjectID = strings.TrimSpace(config.ProjectID)
	config.SiteKey = strings.TrimSpace(config.SiteKey)
	config.APIKey = strings.TrimSpace(config.APIKey)
	if !recaptchaProjectID.MatchString(config.ProjectID) || config.SiteKey == "" || config.APIKey == "" || config.MinimumScore <= 0 || config.MinimumScore > 1 || len(config.AllowedHostnames) == 0 {
		return nil, ErrVerification
	}
	hosts := make([]string, 0, len(config.AllowedHostnames))
	for _, hostname := range config.AllowedHostnames {
		hostname = strings.ToLower(strings.TrimSpace(hostname))
		if hostname == "" || strings.ContainsAny(hostname, "/: ") {
			return nil, ErrVerification
		}
		hosts = append(hosts, hostname)
	}
	config.AllowedHostnames = hosts
	return &Recaptcha{config: config, client: &http.Client{Timeout: 8 * time.Second}}, nil
}

func (r *Recaptcha) SiteKey() string { return r.config.SiteKey }

// Assess valida uso único, acción, hostname y puntaje. Falla cerrado ante errores de Google.
func (r *Recaptcha) Assess(ctx context.Context, token, action, sourceIP, userAgent string) error {
	if len(token) < 20 || len(token) > 8192 || action == "" || len(action) > 64 {
		return ErrVerification
	}
	payload, err := json.Marshal(map[string]any{"event": map[string]string{
		"token": token, "siteKey": r.config.SiteKey, "expectedAction": action,
		"userIpAddress": sourceIP, "userAgent": userAgent,
	}})
	if err != nil {
		return ErrVerification
	}
	endpoint := "https://recaptchaenterprise.googleapis.com/v1/projects/" + url.PathEscape(r.config.ProjectID) + "/assessments?key=" + url.QueryEscape(r.config.APIKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return ErrVerification
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return ErrProvider
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ErrProvider
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return ErrProvider
	}
	var assessment struct {
		TokenProperties struct {
			Valid         bool   `json:"valid"`
			InvalidReason string `json:"invalidReason"`
			Action        string `json:"action"`
			Hostname      string `json:"hostname"`
		} `json:"tokenProperties"`
		RiskAnalysis struct {
			Score   float64  `json:"score"`
			Reasons []string `json:"reasons"`
		} `json:"riskAnalysis"`
	}
	if json.Unmarshal(data, &assessment) != nil {
		log.Printf("recaptcha assessment outcome=rejected code=invalid_response")
		return &RecaptchaAssessmentError{Code: "INVALID_RESPONSE"}
	}
	properties := assessment.TokenProperties
	risk := assessment.RiskAnalysis
	if !properties.Valid {
		log.Printf("recaptcha assessment outcome=rejected code=invalid_token invalidReason=%q", properties.InvalidReason)
		return &RecaptchaAssessmentError{Code: "INVALID_TOKEN"}
	}
	if properties.Action != action {
		log.Printf("recaptcha assessment outcome=rejected code=unexpected_action action=%q", properties.Action)
		return &RecaptchaAssessmentError{Code: "UNEXPECTED_ACTION"}
	}
	hostname := strings.ToLower(properties.Hostname)
	for _, allowed := range r.config.AllowedHostnames {
		if hostname == allowed {
			if risk.Score < r.config.MinimumScore {
				if risk.Score < recaptchaPrivacyFallbackScore || explicitRecaptchaRisk(risk.Reasons) {
					log.Printf("recaptcha assessment outcome=rejected code=low_score action=%q hostname=%q score=%.2f minimumScore=%.2f reasons=%q", properties.Action, hostname, risk.Score, r.config.MinimumScore, risk.Reasons)
					return &RecaptchaAssessmentError{Code: "LOW_SCORE"}
				}
				// La banda 0.3–minimumScore tolera proxies de privacidad y baja
				// confianza, pero no señales explícitas de abuso. El checkout conserva
				// sesión breve, cuenta, idempotencia y un único intento abierto.
				log.Printf("recaptcha assessment outcome=accepted_privacy_fallback action=%q hostname=%q score=%.2f minimumScore=%.2f reasons=%q", properties.Action, hostname, risk.Score, r.config.MinimumScore, risk.Reasons)
			} else {
				log.Printf("recaptcha assessment outcome=accepted action=%q hostname=%q score=%.2f reasons=%q", properties.Action, hostname, risk.Score, risk.Reasons)
			}
			return nil
		}
	}
	log.Printf("recaptcha assessment outcome=rejected code=unexpected_hostname hostname=%q", hostname)
	return &RecaptchaAssessmentError{Code: "UNEXPECTED_HOSTNAME"}
}

func explicitRecaptchaRisk(reasons []string) bool {
	for _, reason := range reasons {
		switch reason {
		case "AUTOMATION", "TOO_MUCH_TRAFFIC", "UNEXPECTED_USAGE_PATTERNS":
			return true
		}
	}
	return false
}

// LoadRecaptchaConfig exige un único SecureString JSON por ambiente.
func LoadRecaptchaConfig(ctx context.Context, reader ParameterReader, stage string) (RecaptchaConfig, error) {
	var config RecaptchaConfig
	if reader == nil || (stage != "dev" && stage != "prd") {
		return config, errors.New("configuración reCAPTCHA inválida")
	}
	value, err := readSecureParameter(ctx, reader, "/indomito/"+stage+"/payments/recaptcha/config")
	if err != nil || json.Unmarshal([]byte(value), &config) != nil {
		return RecaptchaConfig{}, errors.New("configuración reCAPTCHA incompleta o inválida en SSM")
	}
	if _, err = NewRecaptcha(config); err != nil {
		return RecaptchaConfig{}, errors.New("configuración reCAPTCHA incompleta o inválida en SSM")
	}
	return config, nil
}
