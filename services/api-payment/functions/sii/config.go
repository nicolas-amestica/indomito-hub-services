// Package sii implementa la emision de boletas electronicas contra el SII.
package sii

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/beevik/etree"
	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

const documentType = 39

var ErrInvalidConfiguration = errors.New("invalid SII configuration")

type Environment string

const (
	EnvironmentCertification Environment = "CERTIFICATION"
	EnvironmentProduction    Environment = "PRODUCTION"
)

// Config contiene material tributario y criptografico almacenado como un unico SecureString.
type Config struct {
	Enabled              bool        `json:"enabled"`
	Environment          Environment `json:"environment"`
	IssuerActivity       string      `json:"issuerActivity"`
	IssuerAddress        string      `json:"issuerAddress"`
	IssuerCommune        string      `json:"issuerCommune"`
	NotificationEmail    string      `json:"notificationEmail,omitempty"`
	SenderRUT            string      `json:"senderRut"`
	ResolutionDate       string      `json:"resolutionDate"`
	ResolutionNumber     int64       `json:"resolutionNumber"`
	CertificatePFXBase64 string      `json:"certificatePfxBase64"`
	CertificatePassword  string      `json:"certificatePassword"`
	CAFXMLBase64         string      `json:"cafXmlBase64"`
}

type Credentials struct {
	Config
	IssuerRUT       string
	IssuerName      string
	FolioFrom       int64
	FolioTo         int64
	AuthorizationAt string
	CAF             *etree.Element
	CAFPrivateKey   *rsa.PrivateKey
	SigningKey      *rsa.PrivateKey
	Certificate     *x509.Certificate
}

// CAFInfo contains the non-sensitive range extracted from a CAF DTE 39.
type CAFInfo struct {
	From, To  int64
	IssuerRUT string
}

// ValidateCAF validates the embedded RSA key and DTE 39 range without retaining XML.
func ValidateCAF(raw []byte) (CAFInfo, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(raw); err != nil {
		return CAFInfo{}, ErrInvalidConfiguration
	}
	caf, key := doc.FindElement("//CAF"), doc.FindElement("//RSASK")
	if caf == nil || key == nil {
		return CAFInfo{}, ErrInvalidConfiguration
	}
	block, _ := pem.Decode([]byte(strings.TrimSpace(key.Text())))
	if block == nil {
		return CAFInfo{}, ErrInvalidConfiguration
	}
	if _, err := x509.ParsePKCS1PrivateKey(block.Bytes); err != nil {
		return CAFInfo{}, ErrInvalidConfiguration
	}
	read := func(path string) string {
		element := caf.FindElement(path)
		if element == nil {
			return ""
		}
		return strings.TrimSpace(element.Text())
	}
	from, fromErr := strconv.ParseInt(read("DA/RNG/D"), 10, 64)
	to, toErr := strconv.ParseInt(read("DA/RNG/H"), 10, 64)
	if read("DA/TD") != strconv.Itoa(documentType) || fromErr != nil || toErr != nil || from < 1 || to < from || read("DA/RE") == "" || read("DA/RS") == "" || read("DA/FA") == "" {
		return CAFInfo{}, ErrInvalidConfiguration
	}
	return CAFInfo{From: from, To: to, IssuerRUT: read("DA/RE")}, nil
}

// ValidatePFX checks password, RSA signing key and certificate validity.
func ValidatePFX(raw []byte, password string, now time.Time) (*x509.Certificate, error) {
	if len(raw) == 0 || password == "" {
		return nil, ErrInvalidConfiguration
	}
	key, certificate, _, err := pkcs12.DecodeChain(raw, password)
	if err != nil || certificate == nil || now.Before(certificate.NotBefore) || now.After(certificate.NotAfter) {
		return nil, ErrInvalidConfiguration
	}
	if _, ok := key.(*rsa.PrivateKey); !ok {
		return nil, ErrInvalidConfiguration
	}
	return certificate, nil
}

func ParseConfig(raw []byte, now time.Time) (Credentials, error) {
	var cfg Config
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Credentials{}, ErrInvalidConfiguration
	}
	resolutionDate, resolutionErr := time.Parse(time.DateOnly, cfg.ResolutionDate)
	if !cfg.Enabled || (cfg.Environment != EnvironmentCertification && cfg.Environment != EnvironmentProduction) || strings.TrimSpace(cfg.IssuerActivity) == "" || strings.TrimSpace(cfg.IssuerAddress) == "" || strings.TrimSpace(cfg.IssuerCommune) == "" || strings.TrimSpace(cfg.SenderRUT) == "" || resolutionErr != nil || resolutionDate.After(now) || cfg.ResolutionNumber < 0 {
		return Credentials{}, ErrInvalidConfiguration
	}
	pfx, err := base64.StdEncoding.DecodeString(cfg.CertificatePFXBase64)
	if err != nil || len(pfx) == 0 || cfg.CertificatePassword == "" {
		return Credentials{}, ErrInvalidConfiguration
	}
	privateKey, certificate, _, err := pkcs12.DecodeChain(pfx, cfg.CertificatePassword)
	if err != nil || certificate == nil || now.Before(certificate.NotBefore) || now.After(certificate.NotAfter) {
		return Credentials{}, ErrInvalidConfiguration
	}
	signingKey, ok := privateKey.(*rsa.PrivateKey)
	if !ok {
		return Credentials{}, ErrInvalidConfiguration
	}
	cafBytes, err := base64.StdEncoding.DecodeString(cfg.CAFXMLBase64)
	if err != nil || len(cafBytes) == 0 {
		return Credentials{}, ErrInvalidConfiguration
	}
	doc := etree.NewDocument()
	if err = doc.ReadFromBytes(cafBytes); err != nil {
		return Credentials{}, ErrInvalidConfiguration
	}
	caf := doc.FindElement("//CAF")
	privateKeyElement := doc.FindElement("//RSASK")
	if caf == nil || privateKeyElement == nil {
		return Credentials{}, ErrInvalidConfiguration
	}
	privatePEM := strings.TrimSpace(privateKeyElement.Text())
	block, _ := pem.Decode([]byte(privatePEM))
	if block == nil {
		return Credentials{}, ErrInvalidConfiguration
	}
	cafKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return Credentials{}, ErrInvalidConfiguration
	}
	read := func(path string) string {
		element := caf.FindElement(path)
		if element == nil {
			return ""
		}
		return strings.TrimSpace(element.Text())
	}
	folioFrom, fromErr := strconv.ParseInt(read("DA/RNG/D"), 10, 64)
	folioTo, toErr := strconv.ParseInt(read("DA/RNG/H"), 10, 64)
	if read("DA/TD") != strconv.Itoa(documentType) || fromErr != nil || toErr != nil || folioFrom < 1 || folioTo < folioFrom || read("DA/RE") == "" || read("DA/RS") == "" || read("DA/FA") == "" {
		return Credentials{}, fmt.Errorf("%w: invalid CAF", ErrInvalidConfiguration)
	}
	return Credentials{Config: cfg, IssuerRUT: read("DA/RE"), IssuerName: read("DA/RS"), FolioFrom: folioFrom, FolioTo: folioTo, AuthorizationAt: read("DA/FA"), CAF: caf.Copy(), CAFPrivateKey: cafKey, SigningKey: signingKey, Certificate: certificate}, nil
}
