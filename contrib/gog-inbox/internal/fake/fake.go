// Package fake supplies a day of agenda and inbox fixtures for --fixtures,
// frame dumps and tests. Times are relative to now so the now mark always lands
// mid-agenda.
package fake

import (
	"time"

	"github.com/openclaw/gogcli/contrib/gog-inbox/internal/item"
)

// RSVP values as the Calendar API spells them.
const (
	accepted    = "accepted"
	tentative   = "tentative"
	declined    = "declined"
	needsAction = "needsAction"
)

func quarter(t time.Time) time.Time {
	return t.Truncate(15 * time.Minute)
}

func Agenda(now time.Time) []item.Event {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	at := func(offset time.Duration) time.Time { return quarter(now.Add(offset)) }

	return []item.Event{
		{
			ID: "allday", Title: "Focus day: inbox TUI", AllDay: true,
			Start: day, End: day.Add(24 * time.Hour), Response: accepted,
		},
		{
			ID: "e1", Title: "Breakfast with Sam", Start: at(-4 * time.Hour), End: at(-3 * time.Hour),
			Location: "Café Oliv, Münzstraße 8, 10178 Berlin", Organizer: "Sam Weber", Response: accepted,
		},
		{
			ID: "e2", Title: "Daily standup", Start: at(-90 * time.Minute), End: at(-75 * time.Minute),
			HangoutLink: "https://meet.google.com/abc-defg-hij", Organizer: "Ana Ruiz", Response: accepted,
			Attendees: []item.Attendee{{Name: "Ana Ruiz", Response: accepted}, {Name: "Jonas Klee", Response: accepted}, {Name: "Mira Patel", Response: tentative}},
		},
		{
			ID: "e3", Title: "Design review: gog-inbox wireframe", Start: at(-20 * time.Minute), End: at(25 * time.Minute),
			ConferenceURL: "https://meet.google.com/kqx-ptrm-wze", Location: "Room Spree, https://meet.google.com/kqx-ptrm-wze",
			Organizer: "Jonas Klee", Response: accepted,
			Description: "Walk through the half and quarter pane layouts.\nBring screenshots of both sizes.\nNotes doc: https://docs.google.com/document/d/1abc",
			Attendees: []item.Attendee{
				{Name: "Jonas Klee", Response: accepted},
				{Name: "Ana Ruiz", Response: accepted},
				{Name: "Mira Patel", Response: declined},
				{Name: "Lee Chen", Response: needsAction},
			},
		},
		{
			ID: "e4", Title: "Customer sync (Zoom)", Start: at(40 * time.Minute), End: at(85 * time.Minute),
			Location: "https://acme.zoom.us/j/9876543210", Organizer: "Kim Hofmann", Response: needsAction,
			Description: "Quarterly check-in.\nJoin Zoom: https://acme.zoom.us/j/9876543210?pwd=abc",
		},
		{
			ID: "e5", Title: "Lunch at the office", Start: at(2 * time.Hour), End: at(3 * time.Hour),
			Location: "Torstraße 1, 10119 Berlin", Response: accepted,
		},
		{
			ID: "e6", Title: "Dentist", Start: at(4 * time.Hour), End: at(5 * time.Hour),
			Location: "Zahnarztpraxis Mitte, Rosenthaler Str. 40, 10178 Berlin", Response: accepted,
		},
		{
			ID: "e7", Title: "1:1 Ana / Lucas", Start: at(6 * time.Hour), End: at(6*time.Hour + 30*time.Minute),
			HangoutLink: "https://meet.google.com/one-onon-eee", Organizer: "Ana Ruiz", Response: tentative,
		},
	}
}

func Inbox(now time.Time) []item.Mail {
	ago := func(d time.Duration) time.Time { return now.Add(-d) }

	return []item.Mail{
		{ID: "m1", ThreadID: "t1", From: "Kim Hofmann", FromAddr: "kim@acme.example", Subject: "Agenda for today's sync", Snippet: "Hi Lucas, attached the three topics we want to cover, mostly the Q4 rollout and the open invoice.", Date: ago(12 * time.Minute), Unread: true, Labels: []string{"INBOX", "IMPORTANT"}},
		{ID: "m2", ThreadID: "t2", From: "GitHub", FromAddr: "notifications@github.com", Subject: "[openclaw/gogcli] Release v0.48.1", Snippet: "Fixes Gmail label lookup for mixed-case names and the calendar alias migration.", Date: ago(48 * time.Minute), Unread: true, Labels: []string{"INBOX"}},
		{ID: "m3", ThreadID: "t3", From: "Ana Ruiz", FromAddr: "ana@contiamo.example", Subject: "Re: hiring loop next week", Snippet: "Works for me. I can take the code review round on Wednesday if you do the eng chat.", Date: ago(2 * time.Hour), Unread: true, Labels: []string{"INBOX"}},
		{ID: "m4", ThreadID: "t4", From: "Google Calendar", FromAddr: "calendar-notification@google.com", Subject: "Invitation: Customer sync (Zoom) @ today", Snippet: "Kim Hofmann has invited you to Customer sync (Zoom).", Date: ago(3 * time.Hour), Labels: []string{"INBOX"}},
		{ID: "m5", ThreadID: "t5", From: "Deutsche Bahn", FromAddr: "noreply@bahn.example", Subject: "Ihre Fahrkarte Berlin → Köln", Snippet: "Vielen Dank für Ihre Buchung. Ihr Ticket finden Sie im Anhang.", Date: ago(20 * time.Hour), Labels: []string{"INBOX"}},
		{ID: "m6", ThreadID: "t6", From: "Jonas Klee", FromAddr: "jonas@contiamo.example", Subject: "Screenshots for the design review", Snippet: "Here are the two pane sizes from my laptop, the quarter one wraps the attendee line.", Date: ago(26 * time.Hour), Labels: []string{"INBOX"}},
		{ID: "m7", ThreadID: "t7", From: "Linear", FromAddr: "notifications@linear.app", Subject: "CON-4250 assigned to you", Snippet: "telag deploy bakes a minimal PATH into the unit, so every session loses the user's tools.", Date: ago(30 * time.Hour), Labels: []string{"INBOX"}},
		{ID: "m8", ThreadID: "t8", From: "Mira Patel", FromAddr: "mira@contiamo.example", Subject: "Offsite dates", Snippet: "Can everyone fill in the poll by Friday? Two options in March.", Date: ago(50 * time.Hour), Labels: []string{"INBOX"}},
	}
}
