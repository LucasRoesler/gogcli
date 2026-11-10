package gog

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func canned(t *testing.T, stdout string, gotArgs *[]string) Runner {
	t.Helper()

	return func(_ context.Context, args []string, out, _ io.Writer) error {
		if gotArgs != nil {
			*gotArgs = args
		}

		_, _ = io.WriteString(out, stdout)

		return nil
	}
}

const eventsJSON = `{"events": [
  {"id": "a", "summary": "Design review", "status": "confirmed",
   "start": {"dateTime": "2026-11-09T10:45:00+01:00"}, "end": {"dateTime": "2026-11-09T11:30:00+01:00"},
   "location": "Room Spree",
   "conferenceData": {"entryPoints": [{"entryPointType": "phone", "uri": "tel:+49"}, {"entryPointType": "video", "uri": "https://meet.google.com/abc-defg-hij"}]},
   "organizer": {"email": "jonas@example.com", "displayName": "Jonas"},
   "attendees": [
     {"email": "me@example.com", "self": true, "responseStatus": "tentative"},
     {"email": "room@resource.calendar.google.com", "resource": true, "responseStatus": "accepted"},
     {"email": "ana@example.com", "displayName": "Ana", "responseStatus": "accepted"},
     {"email": "ben@example.com", "responseStatus": "needsAction"}]},
  {"id": "b", "summary": "Home", "eventType": "workingLocation", "start": {"date": "2026-11-09"}, "end": {"date": "2026-11-10"}},
  {"id": "c", "summary": "Gone", "status": "cancelled", "start": {"dateTime": "2026-11-09T12:00:00+01:00"}},
  {"id": "d", "summary": "Focus day", "start": {"date": "2026-11-09"}, "end": {"date": "2026-11-10"}}
]}`

func TestAgendaMapsCalendarEventsAndDropsNoise(t *testing.T) {
	var args []string

	events, err := Source{Account: "me@example.com", Run: canned(t, eventsJSON, &args)}.Agenda(context.Background(), time.Date(2026, 11, 9, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(args, " "); got != "--json --no-input --account me@example.com calendar events --from 2026-11-09 --days 1 --max 250" {
		t.Fatalf("args %q", got)
	}

	if len(events) != 2 {
		t.Fatalf("got %d events, want the meeting and the all-day event: %+v", len(events), events)
	}

	e := events[0]
	if e.ConferenceURL != "https://meet.google.com/abc-defg-hij" || e.Response != "tentative" || e.Organizer != "Jonas" {
		t.Fatalf("mapped %+v", e)
	}

	if len(e.Attendees) != 3 || e.Attendees[2].Name != "ben@example.com" {
		t.Fatalf("attendees %+v, want the room left out and Ben named by email", e.Attendees)
	}

	if !e.Start.Equal(time.Date(2026, 11, 9, 9, 45, 0, 0, time.UTC)) || e.AllDay {
		t.Fatalf("start %v allDay %v", e.Start, e.AllDay)
	}

	if !events[1].AllDay || events[1].Response != "accepted" {
		t.Fatalf("all-day event %+v", events[1])
	}
}

func TestInboxMapsThreadsAndKeepsOnlyUserLabels(t *testing.T) {
	// t1's first message is read, but the is:unread search finds a reply.
	inbox := `{"threads": [{"id": "t1", "internalDateIso": "2026-11-09T10:59:12.345+01:00", "messageCount": 3,
	  "from": "Kim Hofmann <kim@example.com>", "subject": "Agenda", "labels": ["INBOX", "CATEGORY_UPDATES", "clients/acme"]},
	  {"id": "t2", "from": "noreply@example.com", "labels": ["INBOX", "UNREAD"]}], "count": 2}`
	unread := `{"threads": [{"id": "t1"}], "count": 1}`

	var queries []string

	run := func(_ context.Context, args []string, out, _ io.Writer) error {
		queries = append(queries, strings.Join(args, " "))
		if slices.Contains(args, "in:inbox is:unread") {
			_, _ = io.WriteString(out, unread)
		} else {
			_, _ = io.WriteString(out, inbox)
		}

		return nil
	}

	mails, err := Source{Max: 30, Run: run}.Inbox(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"--json --no-input gmail search in:inbox --max 30", "--json --no-input gmail search in:inbox is:unread --max 30"}
	if !slices.Equal(queries, want) {
		t.Fatalf("ran %q, want %q", queries, want)
	}

	m := mails[0]
	if m.From != "Kim Hofmann" || m.FromAddr != "kim@example.com" || !m.Unread || m.ThreadID != "t1" || m.Messages != 3 {
		t.Fatalf("mapped %+v", m)
	}

	if len(m.Labels) != 1 || m.Labels[0] != "clients/acme" {
		t.Fatalf("labels %v", m.Labels)
	}

	if m.Date.IsZero() || mails[1].Subject != "(no subject)" || mails[1].From != "noreply@example.com" || mails[1].Unread {
		t.Fatalf("mapped %+v / %+v", m, mails[1])
	}
}

func b64(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) }

func TestBodyReadsTheNewestMessageAndStripsHTMLOnlyMail(t *testing.T) {
	out := `{"thread": {"id": "t1", "messages": [
	  {"payload": {"mimeType": "text/plain", "body": {"data": "` + b64("older") + `"}}},
	  {"payload": {"mimeType": "multipart/alternative", "parts": [
	    {"mimeType": "text/html", "body": {"data": "` + b64("<p>Hi <b>Lucas</b></p>") + `"}}]}}]}}`

	body, err := Source{Run: canned(t, out, nil)}.Body(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(body, "Hi") || !strings.Contains(body, "Lucas") || strings.Contains(body, "<") || strings.Contains(body, "older") {
		t.Fatalf("body %q", body)
	}
}

func TestWritesSendTheGmailLabelChange(t *testing.T) {
	var args []string

	s := Source{Run: canned(t, `{}`, &args)}
	ctx := context.Background()

	cases := []struct {
		run  func() error
		want string
	}{
		{func() error { return s.Archive(ctx, "t1") }, "gmail thread modify t1 --remove INBOX"},
		{func() error { return s.SetUnread(ctx, "t1", false) }, "gmail thread modify t1 --remove UNREAD"},
		{func() error { return s.SetUnread(ctx, "t1", true) }, "gmail thread modify t1 --add UNREAD"},
	}
	for _, c := range cases {
		if err := c.run(); err != nil {
			t.Fatal(err)
		}

		if got := strings.Join(args[2:], " "); got != c.want {
			t.Fatalf("args %q, want %q", got, c.want)
		}
	}
}

var errExit = errors.New("exit status 3")

func TestFailuresCarryGogsOwnMessage(t *testing.T) {
	run := func(_ context.Context, _ []string, out, errOut io.Writer) error {
		_, _ = io.WriteString(out, `{"error": {"code": 3, "message": "missing --account (or set GOG_ACCOUNT)", "class": "usage"}}`+"\n")
		_, _ = io.WriteString(errOut, "noise\n")

		return errExit
	}

	_, err := Source{Run: run}.Inbox(context.Background())
	if err == nil || !strings.Contains(err.Error(), "gog gmail search: gog: missing --account") {
		t.Fatalf("err %v", err)
	}
}

// fakeGoogle serves the Calendar and Gmail endpoints gog calls, recording
// each request path.
func fakeGoogle(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()

	var mu sync.Mutex

	var paths []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()

		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")

		var body any

		switch {
		case strings.HasSuffix(r.URL.Path, "/hang/modify"):
			// The server sees the client hang up only once the body is read.
			_, _ = io.Copy(io.Discard, r.Body)
			<-r.Context().Done()

			return
		case strings.HasSuffix(r.URL.Path, "/events"):
			body = map[string]any{"items": []any{map[string]any{
				"id": "e1", "summary": "Standup", "hangoutLink": "https://meet.google.com/x-y-z",
				"start": map[string]any{"dateTime": "2026-11-09T09:30:00Z"}, "end": map[string]any{"dateTime": "2026-11-09T09:45:00Z"},
			}}}
		case strings.Contains(r.URL.Path, "/calendars/primary"), strings.Contains(r.URL.Path, "/users/me/calendarList/primary"):
			body = map[string]any{"id": "primary", "timeZone": "Europe/Berlin"}
		case strings.HasSuffix(r.URL.Path, "/labels"):
			body = map[string]any{"labels": []any{map[string]any{"id": "INBOX", "name": "INBOX", "type": "system"}}}
		case strings.HasSuffix(r.URL.Path, "/modify"):
			body = map[string]any{"id": "t1"}
		default:
			http.Error(w, `{"error": {"code": 404, "message": "not faked"}}`, http.StatusNotFound)
			return
		}

		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)

	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()

		return append([]string(nil), paths...)
	}
}

// Runs real gog code in-process against a fake Google API, so a change to
// gog's flags or output that breaks gog-inbox fails here.
func TestInProcessGogReadsTheAgendaAndArchives(t *testing.T) {
	srv, paths := fakeGoogle(t)

	t.Setenv("GOG_HOME", t.TempDir())
	t.Setenv("GOG_ACCESS_TOKEN", "test-token")
	t.Setenv("GOG_GOOGLE_API_BASE_URL", srv.URL)
	t.Setenv("GOG_KEYRING_BACKEND", "file")

	s := Source{Run: InProcess}

	events, err := s.Agenda(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("agenda: %v (requests %v)", err, paths())
	}

	if len(events) != 1 || events[0].JoinURL() != "https://meet.google.com/x-y-z" {
		t.Fatalf("events %+v", events)
	}

	if err := s.Archive(context.Background(), "t1"); err != nil {
		t.Fatalf("archive: %v (requests %v)", err, paths())
	}

	if got := paths(); !strings.HasSuffix(got[len(got)-1], "/threads/t1/modify") {
		t.Fatalf("last request %v", got)
	}
}

// A hung Google call must end at the caller's deadline and free gog for the
// next command, or one stuck request blocks every later load and write.
func TestInProcessGogGivesUpAtTheDeadline(t *testing.T) {
	srv, paths := fakeGoogle(t)

	t.Setenv("GOG_HOME", t.TempDir())
	t.Setenv("GOG_ACCESS_TOKEN", "test-token")
	t.Setenv("GOG_GOOGLE_API_BASE_URL", srv.URL)
	t.Setenv("GOG_KEYRING_BACKEND", "file")

	s := Source{Run: InProcess}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()

	if err := s.Archive(ctx, "hang"); err == nil {
		t.Fatal("archive of a hung thread succeeded")
	}

	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("archive returned after %v, want about the 200ms deadline", took)
	}

	if err := s.Archive(context.Background(), "t1"); err != nil {
		t.Fatalf("archive after the hung call: %v (requests %v)", err, paths())
	}
}
