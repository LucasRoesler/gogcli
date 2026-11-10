package item

import "testing"

func TestJoinURLPrefersConferenceDataOverPastedLinks(t *testing.T) {
	e := Event{
		ConferenceURL: "https://meet.google.com/abc-defg-hij",
		HangoutLink:   "https://meet.google.com/zzz-zzzz-zzz",
		Location:      "https://zoom.us/j/123",
	}
	if got := e.JoinURL(); got != "https://meet.google.com/abc-defg-hij" {
		t.Fatalf("JoinURL() = %q", got)
	}
}

func TestJoinURLFindsZoomLinkPastedInDescription(t *testing.T) {
	e := Event{Description: "Agenda below.\nJoin: https://contiamo.zoom.us/j/987?pwd=x\nSee https://example.com/doc"}
	if got := e.JoinURL(); got != "https://contiamo.zoom.us/j/987?pwd=x" {
		t.Fatalf("JoinURL() = %q", got)
	}
}

func TestJoinURLIgnoresNonMeetingLinks(t *testing.T) {
	e := Event{Description: "Doc: https://docs.google.com/document/d/1"}
	if got := e.JoinURL(); got != "" {
		t.Fatalf("JoinURL() = %q, want empty", got)
	}
}

func TestPlaceDropsMeetingURLFromLocation(t *testing.T) {
	cases := map[string]string{
		"https://zoom.us/j/1":                       "",
		"Room Spree, https://meet.google.com/a-b-c": "Room Spree",
		"Torstraße 1, 10119 Berlin":                 "Torstraße 1, 10119 Berlin",
		"":                                          "",
	}
	for loc, want := range cases {
		if got := (Event{Location: loc}).Place(); got != want {
			t.Errorf("Place(%q) = %q, want %q", loc, got, want)
		}
	}
}

func TestDirectionsURLEncodesThePlace(t *testing.T) {
	e := Event{Location: "Torstraße 1, 10119 Berlin"}

	want := "https://www.google.com/maps/dir/?api=1&destination=Torstra%C3%9Fe+1%2C+10119+Berlin"
	if got := e.DirectionsURL(); got != want {
		t.Fatalf("DirectionsURL() = %q, want %q", got, want)
	}

	if got := (Event{Location: "https://zoom.us/j/1"}).DirectionsURL(); got != "" {
		t.Fatalf("DirectionsURL() for a Zoom-only location = %q, want empty", got)
	}
}
