// Package gog loads the agenda and inbox by running gog commands in-process,
// so gog-inbox uses the same accounts, keyring, config and reauth as the gog
// CLI. It decodes gog's --json output, which keeps it independent of gog's
// internals beyond cmd.ExecuteIO.
package gog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"slices"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/gmail/v1"

	"github.com/openclaw/gogcli/contrib/gog-inbox/internal/item"
	"github.com/openclaw/gogcli/internal/cmd"
	"github.com/openclaw/gogcli/internal/gmailcontent"
)

// Runner runs one gog command line and writes its stdout and stderr. It
// stops the command when ctx ends.
type Runner func(ctx context.Context, args []string, out, errOut io.Writer) error

// InProcess runs gog inside this binary with empty stdin and --no-input, so a
// command that needs a prompt or a browser fails and never writes to the
// terminal the TUI owns.
func InProcess(ctx context.Context, args []string, out, errOut io.Writer) error {
	return cmd.ExecuteIO(ctx, args, strings.NewReader(""), out, errOut) //nolint:wrapcheck // run wraps it with the command name.
}

// Source reads one account's primary agenda and inbox through gog.
type Source struct {
	// Account is passed as --account; empty lets gog pick its default
	// account (GOG_ACCOUNT, then the stored default).
	Account string
	// Max caps the inbox threads loaded.
	Max int
	Run Runner
}

var errGog = errors.New("gog")

func (s Source) Name() string {
	if s.Account != "" {
		return s.Account
	}

	return "gog default account"
}

func (s Source) run(ctx context.Context, out any, args ...string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("gog %s: %w", strings.Join(args[:min(len(args), 2)], " "), err)
	}

	full := []string{"--json", "--no-input"}
	if s.Account != "" {
		full = append(full, "--account", s.Account)
	}

	full = append(full, args...)

	var stdout, stderr bytes.Buffer
	if err := s.Run(ctx, full, &stdout, &stderr); err != nil {
		return fmt.Errorf("gog %s: %w", strings.Join(args[:min(len(args), 2)], " "), describe(err, stdout.Bytes(), stderr.String()))
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
		return fmt.Errorf("gog %s: decode output: %w", strings.Join(args[:min(len(args), 2)], " "), err)
	}

	return nil
}

// describe returns the error text of a failed run: the JSON error envelope
// gog writes in --json mode, else the last stderr line.
func describe(err error, stdout []byte, stderr string) error {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if json.Unmarshal(stdout, &envelope) == nil && envelope.Error.Message != "" {
		return fmt.Errorf("%w: %s", errGog, envelope.Error.Message)
	}

	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return fmt.Errorf("%w: %s", errGog, last)
	}

	return err
}

// Agenda returns the events on day's date from the primary calendar. gog
// reads the date in the calendar's own timezone.
func (s Source) Agenda(ctx context.Context, day time.Time) ([]item.Event, error) {
	var out struct {
		Events []*calendar.Event `json:"events"`
	}

	if err := s.run(ctx, &out, "calendar", "events", "--from", day.Format(time.DateOnly), "--days", "1", "--max", "250"); err != nil {
		return nil, err
	}

	events := make([]item.Event, 0, len(out.Events))
	for _, e := range out.Events {
		if ev, ok := toEvent(e); ok {
			events = append(events, ev)
		}
	}

	return events, nil
}

func toEvent(e *calendar.Event) (item.Event, bool) {
	// Working-location entries ("Home", "Office") span the day but are not
	// meetings.
	if e == nil || e.Status == "cancelled" || e.EventType == "workingLocation" {
		return item.Event{}, false
	}

	ev := item.Event{
		ID:          e.Id,
		Title:       e.Summary,
		Location:    e.Location,
		Description: e.Description,
		HangoutLink: e.HangoutLink,
		HTMLLink:    e.HtmlLink,
		Response:    "accepted",
	}

	if ev.Title == "" {
		ev.Title = "(no title)"
	}

	var ok bool

	ev.Start, ev.AllDay, ok = eventTime(e.Start)
	if !ok {
		return item.Event{}, false
	}

	if ev.End, _, ok = eventTime(e.End); !ok {
		ev.End = ev.Start
	}

	if e.ConferenceData != nil {
		for _, p := range e.ConferenceData.EntryPoints {
			if p.EntryPointType == "video" && p.Uri != "" {
				ev.ConferenceURL = p.Uri
				break
			}
		}
	}

	if e.Organizer != nil {
		ev.Organizer = firstNonEmpty(e.Organizer.DisplayName, e.Organizer.Email)
	}

	for _, a := range e.Attendees {
		if a.Self {
			ev.Response = a.ResponseStatus
		}

		if a.Resource {
			continue
		}

		ev.Attendees = append(ev.Attendees, item.Attendee{Name: firstNonEmpty(a.DisplayName, a.Email), Email: a.Email, Response: a.ResponseStatus})
	}

	return ev, true
}

func eventTime(t *calendar.EventDateTime) (time.Time, bool, bool) {
	if t == nil {
		return time.Time{}, false, false
	}

	if t.DateTime != "" {
		v, err := time.Parse(time.RFC3339, t.DateTime)
		return v.Local(), false, err == nil
	}

	v, err := time.ParseInLocation(time.DateOnly, t.Date, time.Local)

	return v, true, err == nil
}

// thread is one row of `gog gmail search --json`.
type thread struct {
	ID              string   `json:"id"`
	InternalDateISO string   `json:"internalDateIso"` //nolint:tagliatelle // gog's field name.
	From            string   `json:"from"`
	Subject         string   `json:"subject"`
	Labels          []string `json:"labels"`
	MessageCount    int      `json:"messageCount"` //nolint:tagliatelle // gog's field name.
}

// Inbox returns the newest threads in the inbox. gog's search output has no
// snippet, so the list shows subjects and the body loads on open.
//
// A thread is unread when any of its messages is, as in Gmail. gog's search
// rows carry the first message's labels only, so a read thread with a new
// unread reply looks read; a second search for is:unread finds those.
func (s Source) Inbox(ctx context.Context) ([]item.Mail, error) {
	limit := strconv.Itoa(max(s.Max, 1))

	var all, unread struct {
		Threads []thread `json:"threads"`
	}

	if err := s.run(ctx, &all, "gmail", "search", "in:inbox", "--max", limit); err != nil {
		return nil, err
	}

	if err := s.run(ctx, &unread, "gmail", "search", "in:inbox is:unread", "--max", limit); err != nil {
		return nil, err
	}

	unreadIDs := make(map[string]bool, len(unread.Threads))
	for _, t := range unread.Threads {
		unreadIDs[t.ID] = true
	}

	mails := make([]item.Mail, 0, len(all.Threads))
	for _, t := range all.Threads {
		m := toMail(t)
		m.Unread = unreadIDs[t.ID]
		mails = append(mails, m)
	}

	return mails, nil
}

// systemLabels are Gmail's own labels; the detail view shows only the user's.
var systemLabels = []string{"INBOX", "UNREAD", "IMPORTANT", "STARRED", "SENT", "DRAFT", "SPAM", "TRASH", "CHAT"}

func toMail(t thread) item.Mail {
	m := item.Mail{
		ID:       t.ID,
		ThreadID: t.ID,
		From:     t.From,
		Subject:  firstNonEmpty(t.Subject, "(no subject)"),
		Messages: t.MessageCount,
	}

	if addr, err := mail.ParseAddress(t.From); err == nil {
		m.From, m.FromAddr = firstNonEmpty(addr.Name, addr.Address), addr.Address
	}

	if d, err := time.Parse(time.RFC3339Nano, t.InternalDateISO); err == nil {
		m.Date = d.Local()
	}

	for _, l := range t.Labels {
		if !slices.Contains(systemLabels, l) && !strings.HasPrefix(l, "CATEGORY_") {
			m.Labels = append(m.Labels, l)
		}
	}

	return m
}

// Body returns the text of the thread's newest message, with HTML-only mail
// reduced to text.
func (s Source) Body(ctx context.Context, threadID string) (string, error) {
	var out struct {
		Thread gmail.Thread `json:"thread"`
	}

	if err := s.run(ctx, &out, "gmail", "thread", "get", threadID); err != nil {
		return "", err
	}

	msgs := out.Thread.Messages
	if len(msgs) == 0 || msgs[len(msgs)-1] == nil {
		return "", nil
	}

	body, isHTML := gmailcontent.BestBodyForDisplay(msgs[len(msgs)-1].Payload)
	if isHTML {
		body = gmailcontent.StripHTMLTags(body)
	}

	return strings.TrimSpace(body), nil
}

// Archive removes the thread from the inbox, as Gmail's archive button does.
func (s Source) Archive(ctx context.Context, threadID string) error {
	return s.run(ctx, nil, "gmail", "thread", "modify", threadID, "--remove", "INBOX")
}

func (s Source) SetUnread(ctx context.Context, threadID string, unread bool) error {
	flag := "--remove"
	if unread {
		flag = "--add"
	}

	return s.run(ctx, nil, "gmail", "thread", "modify", threadID, flag, "UNREAD")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}

	return ""
}
