// Package item holds the inbox and agenda records the UI renders. internal/gog
// fills them from the Gmail and Calendar API shapes, so the UI never imports
// gogcli packages.
package item

import "time"

type Event struct {
	ID          string
	Title       string
	Start, End  time.Time
	AllDay      bool
	Location    string
	Description string
	// ConferenceURL is the video entry point from conferenceData (Meet, Zoom,
	// Teams add-ons); HangoutLink is the legacy Meet field. Either may be empty.
	ConferenceURL string
	HangoutLink   string
	Organizer     string
	Attendees     []Attendee
	// Response is the user's own RSVP: accepted, tentative, declined, needsAction.
	Response string
	HTMLLink string
}

type Attendee struct {
	Name     string
	Email    string
	Response string
}

type Mail struct {
	ID       string
	ThreadID string
	From     string
	FromAddr string
	To       string
	Subject  string
	Snippet  string
	Body     string
	Date     time.Time
	Unread   bool
	Labels   []string
	// Messages is the thread's message count; a change means a new reply, so
	// a loaded Body is stale.
	Messages int
}

// InProgress reports whether now falls inside a timed event.
func (e Event) InProgress(now time.Time) bool {
	return !e.AllDay && !now.Before(e.Start) && now.Before(e.End)
}

// Past reports whether a timed event has ended.
func (e Event) Past(now time.Time) bool {
	return !e.AllDay && !now.Before(e.End)
}
