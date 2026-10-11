package functions

import (
	"testing"
	"time"
)

func TestToViewPreservesMeetingAndHostMetadata(t *testing.T) {
	var event calendarEvent
	event.ID = "event-1"
	event.Summary = "Reunión"
	event.Start.DateTime = "2026-10-11T15:00:00Z"
	event.End.DateTime = "2026-10-11T16:00:00Z"
	event.ExtendedProperties.Private = map[string]string{
		"meetLink": "https://meet.google.com/example",
		"hostSk": "HOST#1",
		"hostName": "Coordinadora",
		"hostColor": "#3a7",
	}
	view := toView(event)
	for key, want := range map[string]string{
		"date":"2026-10-11",
		"startTime":"12:00",
		"endTime":"13:00",
		"googleMeetLink":"https://meet.google.com/example",
		"hostSk":"HOST#1",
		"hostName":"Coordinadora",
		"hostColor":"#3a7",
	} {
		if got, ok := view[key].(string); !ok || got != want {
			t.Errorf("%s: got %v, want %q", key, view[key], want)
		}
	}
}

func TestAllDayEventHasCalendarDate(t *testing.T) {
	var event calendarEvent
	event.Start.Date = "2026-10-11"
	got := toView(event)["date"]
	if got != "2026-10-11" {
		t.Fatalf("all-day date = %v", got)
	}
}

func TestSantiagoTimezoneIsAvailable(t *testing.T) {
	loc := santiago()
	if loc == time.UTC {
		t.Fatal("America/Santiago timezone must be loaded by Lambda runtime")
	}
}
