package item

import (
	"net/url"
	"regexp"
	"strings"
)

// Hosts whose URLs count as a meeting link when they appear in a location or
// description instead of conferenceData (pasted Zoom or Teams invites).
var meetingHosts = []string{
	"meet.google.com",
	"zoom.us",
	"teams.microsoft.com",
	"teams.live.com",
	"whereby.com",
	"webex.com",
}

var urlPattern = regexp.MustCompile(`https?://[^\s<>"')\]]+`)

// JoinURL returns the link to join the meeting, or "" when the event has none.
// conferenceData wins, then hangoutLink, then the first meeting-host URL in the
// location, then in the description.
func (e Event) JoinURL() string {
	if e.ConferenceURL != "" {
		return e.ConferenceURL
	}

	if e.HangoutLink != "" {
		return e.HangoutLink
	}

	if u := firstMeetingURL(e.Location); u != "" {
		return u
	}

	return firstMeetingURL(e.Description)
}

// Place returns the physical location with any meeting URLs removed, or "" when
// nothing else is left. Invites often put the Zoom link in the location field.
func (e Event) Place() string {
	place := urlPattern.ReplaceAllStringFunc(e.Location, func(raw string) string {
		if isMeetingURL(raw) {
			return ""
		}

		return raw
	})

	return strings.Trim(place, " \t\n,;|-")
}

// DirectionsURL returns a Google Maps directions link to the event's place.
func (e Event) DirectionsURL() string {
	place := e.Place()
	if place == "" {
		return ""
	}

	return "https://www.google.com/maps/dir/?api=1&destination=" + url.QueryEscape(place)
}

func firstMeetingURL(s string) string {
	for _, raw := range urlPattern.FindAllString(s, -1) {
		if isMeetingURL(raw) {
			return raw
		}
	}

	return ""
}

func isMeetingURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}

	host := strings.ToLower(u.Hostname())
	for _, h := range meetingHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}

	return false
}

// GmailURL returns the web link to a thread.
func (m Mail) GmailURL() string {
	return "https://mail.google.com/mail/u/0/#all/" + m.ThreadID
}
