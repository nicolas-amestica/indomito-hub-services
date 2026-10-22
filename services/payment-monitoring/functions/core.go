// Package functions supervisa rendiciones sin modificar datos financieros.
package functions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
)

const monitorPK = "RENDITION_MONITOR"

type config struct {
	Enabled        *bool  `json:"enabled"`
	Email          string `json:"email"`
	StartMonth     string `json:"startMonth"`
	PendingMinutes int    `json:"pendingMinutes"`
	UnsettledHours int    `json:"unsettledHours"`
	ReminderHours  int    `json:"reminderHours"`
	MaxPages       int    `json:"maxPages"`
}

func parseConfig(raw string, now time.Time) (config, error) {
	var c config
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return c, errors.New("MONITOR_CONFIG_INVALID")
	}
	address, err := mail.ParseAddress(c.Email)
	if err != nil || address.Address != c.Email || strings.ContainsAny(c.Email, "\r\n") || c.Enabled == nil || !regexp.MustCompile(`^20\d{2}-(0[1-9]|1[0-2])$`).MatchString(c.StartMonth) || c.StartMonth > now.UTC().Format("2006-01") {
		return c, errors.New("MONITOR_CONFIG_INVALID")
	}
	for _, bound := range [][3]int{{c.PendingMinutes, 5, 1440}, {c.UnsettledHours, 1, 720}, {c.ReminderHours, 1, 168}, {c.MaxPages, 2, 100}} {
		if bound[0] < bound[1] || bound[0] > bound[2] {
			return c, errors.New("MONITOR_CONFIG_INVALID")
		}
	}
	return c, nil
}

// Los nombres internos de documentos financieros son los existentes en DynamoDB,
// no los json tags de las respuestas HTTP. Los metadatos propios conservan camelCase.
type rendition struct {
	ID                               string
	ProviderID                       int64
	Status, ReceivedAt, PendingSince string
}
type cashEvent struct{ Type, Reference, TripID, RecordedAt string }
type settlement struct{ Amount, SettledGross *int64 }
type item struct {
	PK         string            `dynamodbav:"pk"`
	SK         string            `dynamodbav:"sk"`
	Rendition  *rendition        `dynamodbav:"rendition,omitempty"`
	Event      *cashEvent        `dynamodbav:"event,omitempty"`
	Settlement *settlement       `dynamodbav:"settlement,omitempty"`
	SentAt     string            `dynamodbav:"sentAt,omitempty"`
	Month      string            `dynamodbav:"month,omitempty"`
	Kind       string            `dynamodbav:"kind,omitempty"`
	After      map[string]string `dynamodbav:"after,omitempty"`
	CheckedAt  string            `dynamodbav:"checkedAt,omitempty"`
}
type page struct {
	Items []item
	Next  map[string]string
}
type finding struct{ Code, ID, Label string }

type store interface {
	Get(context.Context, string, string) (item, error)
	Put(context.Context, item) error
	Query(context.Context, string, map[string]string) (page, error)
}
type sender interface {
	Send(context.Context, string, string) error
}

func age(now time.Time, raw string) (time.Duration, error) {
	date, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || date.IsZero() {
		return 0, errors.New("MONITOR_SOURCE_DATE_INVALID")
	}
	return now.Sub(date), nil
}

func inspect(ctx context.Context, row item, db store, c config, now time.Time) (*finding, error) {
	if row.Rendition != nil {
		latest, err := db.Get(ctx, "RENDITION#"+row.Rendition.ID, "META")
		if err != nil {
			return nil, err
		}
		r := latest.Rendition
		if r == nil {
			return nil, errors.New("MONITOR_RENDITION_MISSING")
		}
		if r.Status == "OBSERVED" || r.Status == "REVIEW_REQUIRED" {
			return &finding{r.Status, r.ID, fmt.Sprintf("Rendición %d: %s", r.ProviderID, r.Status)}, nil
		}
		if r.Status == "PENDING" {
			since := r.PendingSince
			if since == "" || strings.HasPrefix(since, "0001-") {
				since = r.ReceivedAt
			}
			elapsed, err := age(now, since)
			if err != nil {
				return nil, err
			}
			if elapsed >= time.Duration(c.PendingMinutes)*time.Minute {
				return &finding{"PENDING_TOO_LONG", r.ID, fmt.Sprintf("Rendición %d: pendiente por más de %d minutos desde su recepción o reprocesamiento", r.ProviderID, c.PendingMinutes)}, nil
			}
		}
	}
	e := row.Event
	if e == nil || e.Type != "PAYMENT_RECEIVED" || !strings.HasPrefix(e.Reference, "khipu:") {
		return nil, nil
	}
	elapsed, err := age(now, e.RecordedAt)
	if err != nil {
		return nil, err
	}
	if elapsed < time.Duration(c.UnsettledHours)*time.Hour {
		return nil, nil
	}
	latest, err := db.Get(ctx, "TRIP#"+e.TripID, "SETTLEMENT#"+e.Reference)
	if err != nil {
		return nil, err
	}
	s := latest.Settlement
	if s != nil && (s.Amount == nil || s.SettledGross == nil || *s.Amount < 0 || *s.SettledGross < 0) {
		return nil, errors.New("MONITOR_SETTLEMENT_INVALID")
	}
	if s == nil || *s.SettledGross < *s.Amount {
		return &finding{"UNSETTLED_PAYMENT", e.TripID + ":" + e.Reference, fmt.Sprintf("Gira %s, pago %s: sin liquidación completa después de %d horas. Revisar recepción y procesamiento del reporte.", e.TripID, e.Reference, c.UnsettledHours)}, nil
	}
	return nil, nil
}

func notifyFindings(ctx context.Context, findings []finding, db store, mail sender, c config, now time.Time) error {
	var due []item
	var labels []string
	for _, f := range findings {
		hash := sha256.Sum256([]byte(f.Code + ":" + f.ID))
		key := "ALERT#" + hex.EncodeToString(hash[:])
		previous, err := db.Get(ctx, monitorPK, key)
		if err != nil {
			return err
		}
		if previous.SentAt != "" {
			elapsed, err := age(now, previous.SentAt)
			if err != nil {
				return err
			}
			if elapsed < time.Duration(c.ReminderHours)*time.Hour {
				continue
			}
		}
		due = append(due, item{PK: monitorPK, SK: key, SentAt: now.UTC().Format(time.RFC3339Nano)})
		labels = append(labels, f.Label)
	}
	if len(due) == 0 {
		return nil
	}
	if err := mail.Send(ctx, "Rendiciones Khipu: revisión requerida", strings.Join(labels, "\n")); err != nil {
		return err
	}
	// Persistir solo después de aceptación SMTP. Un fallo puede repetir el aviso,
	// nunca un asiento financiero: este servicio no tiene permisos para escribirlos.
	for _, row := range due {
		if err := db.Put(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

func runMonitor(ctx context.Context, db store, mail sender, c config, now time.Time) error {
	if !*c.Enabled {
		return nil
	}
	current := now.UTC().Format("2006-01")
	for _, lane := range []string{"CURRENT", "HISTORY"} {
		key := "CURSOR#" + lane
		cursor, err := db.Get(ctx, monitorPK, key)
		if err != nil {
			return err
		}
		if cursor.Month < c.StartMonth || cursor.Month > current || (lane == "CURRENT" && cursor.Month != current) {
			month := c.StartMonth
			if lane == "CURRENT" {
				month = current
			}
			cursor = item{Month: month, Kind: "RENDITION_MONTH"}
		}
		if cursor.Kind != "RENDITION_MONTH" && cursor.Kind != "CASH" {
			return errors.New("MONITOR_CURSOR_INVALID")
		}
		for n := 0; n < c.MaxPages/2; n++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < 45*time.Second {
				return nil
			}
			result, err := db.Query(ctx, cursor.Kind+"#"+cursor.Month, cursor.After)
			if err != nil {
				return err
			}
			var findings []finding
			for _, row := range result.Items {
				f, err := inspect(ctx, row, db, c, now)
				if err != nil {
					return err
				}
				if f != nil {
					findings = append(findings, *f)
				}
			}
			if err := notifyFindings(ctx, findings, db, mail, c, now); err != nil {
				return err
			}
			cursor.After = result.Next
			complete := false
			if len(result.Next) == 0 {
				if cursor.Kind == "RENDITION_MONTH" {
					cursor.Kind = "CASH"
				} else {
					cursor.Kind = "RENDITION_MONTH"
					month, err := time.Parse("2006-01", cursor.Month)
					if err != nil {
						return errors.New("MONITOR_CURSOR_INVALID")
					}
					cursor.Month = month.AddDate(0, 1, 0).Format("2006-01")
					complete = lane == "CURRENT" || cursor.Month > current
					if complete {
						cursor.Month = c.StartMonth
						if lane == "CURRENT" {
							cursor.Month = current
						}
					}
				}
			}
			cursor.PK = monitorPK
			cursor.SK = key
			cursor.CheckedAt = now.UTC().Format(time.RFC3339Nano)
			if err := db.Put(ctx, cursor); err != nil {
				return err
			}
			if complete {
				break
			}
		}
	}
	return nil
}
