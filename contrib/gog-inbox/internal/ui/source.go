package ui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/openclaw/gogcli/contrib/gog-inbox/internal/item"
)

// Source loads the agenda and inbox and carries out archive and read/unread.
type Source interface {
	// Name is the header label, such as the account email.
	Name() string
	Agenda(ctx context.Context, day time.Time) ([]item.Event, error)
	Inbox(ctx context.Context) ([]item.Mail, error)
	// Body returns the plain-text body of a thread's latest message.
	Body(ctx context.Context, threadID string) (string, error)
	Archive(ctx context.Context, threadID string) error
	SetUnread(ctx context.Context, threadID string, unread bool) error
}

// DefaultRefresh is how often both lists reload, so mail and RSVP changes
// made elsewhere show up without a keypress.
const DefaultRefresh = 2 * time.Minute

// MinRefresh keeps a reload, which is three serialized gog calls, from
// running back to back.
const MinRefresh = 10 * time.Second

var errRefresh = errors.New("refresh must be 0 (off) or at least 10s")

// CheckRefresh validates a --refresh value.
func CheckRefresh(d time.Duration) error {
	if d != 0 && d < MinRefresh {
		return fmt.Errorf("%w, got %v", errRefresh, d)
	}

	return nil
}

// callTimeout bounds one Google round trip, including the wait for an
// earlier one; a stuck call would otherwise leave "loading" in the header
// forever.
const callTimeout = 30 * time.Second

// write is one archive or read/unread change bound for Google. The model
// sends writes one at a time in keypress order, so three quick "u" presses
// end on Google in the state the screen shows.
type write struct {
	threadID string
	archive  bool
	unread   bool
	done     string
}

// landed is a write Google has confirmed. An inbox load issued before the
// write landed (generation below until) may not show it, so the write is
// applied on top of that load's result.
type landed struct {
	write
	until int
}

type (
	// Loads carry the generation they were issued as, so a slow response
	// cannot overwrite a newer one.
	agendaMsg struct {
		gen    int
		day    time.Time
		events []item.Event
		err    error
	}
	inboxMsg struct {
		gen   int
		mails []item.Mail
		err   error
	}
	// bodyMsg carries the message count the body was fetched for, so a body
	// that a new reply overtook is not shown.
	bodyMsg struct {
		threadID, body string
		messages       int
		err            error
	}
	// writeMsg reports the write at the head of the queue. On failure the
	// inbox reloads, which undoes the optimistic local change.
	writeMsg   struct{ err error }
	refreshMsg struct{}
)

func (m Model) refreshTick() tea.Cmd {
	if m.refresh <= 0 {
		return nil
	}

	return tea.Tick(m.refresh, func(time.Time) tea.Msg { return refreshMsg{} })
}

func call[T any](f func(ctx context.Context) T) T {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()

	return f(ctx)
}

func (m Model) loadAgenda(gen int) tea.Cmd {
	src, day := m.src, m.day

	return func() tea.Msg {
		return call(func(ctx context.Context) tea.Msg {
			events, err := src.Agenda(ctx, day)
			return agendaMsg{gen, day, events, err}
		})
	}
}

func (m Model) loadInbox(gen int) tea.Cmd {
	src := m.src

	return func() tea.Msg {
		return call(func(ctx context.Context) tea.Msg {
			mails, err := src.Inbox(ctx)
			return inboxMsg{gen, mails, err}
		})
	}
}

func (m Model) loadBody(threadID string, messages int) tea.Cmd {
	src := m.src

	return func() tea.Msg {
		return call(func(ctx context.Context) tea.Msg {
			body, err := src.Body(ctx, threadID)
			return bodyMsg{threadID, body, messages, err}
		})
	}
}

func (m Model) send(w write) tea.Cmd {
	src := m.src

	return func() tea.Msg {
		return call(func(ctx context.Context) tea.Msg {
			if w.archive {
				return writeMsg{src.Archive(ctx, w.threadID)}
			}

			return writeMsg{src.SetUnread(ctx, w.threadID, w.unread)}
		})
	}
}

// enqueue queues a write and sends it when no other write is in flight.
func (m Model) enqueue(w write) (Model, tea.Cmd) {
	m.writes = append(slices.Clip(m.writes), w)
	if len(m.writes) > 1 {
		return m, nil
	}

	return m, m.send(w)
}

func (m Model) applyWrite(msg writeMsg) (Model, tea.Cmd) {
	if len(m.writes) == 0 {
		return m, nil
	}

	w := m.writes[0]
	m.writes = slices.Clone(m.writes[1:])

	var cmds []tea.Cmd

	if msg.err != nil {
		m.status = w.done + " failed: " + msg.err.Error()

		var reload tea.Cmd

		m, reload = m.reloadInbox()
		cmds = append(cmds, reload)
	} else {
		m.status = w.done
		m.landed = append(slices.Clip(m.landed), landed{w, m.inboxGen + 1})
	}

	if len(m.writes) > 0 {
		cmds = append(cmds, m.send(m.writes[0]))
	}

	return m, tea.Batch(cmds...)
}

// overlay lays landed and queued writes over a loaded inbox, oldest first.
func (m Model) overlay(mails []item.Mail) []item.Mail {
	pending := make([]write, 0, len(m.landed)+len(m.writes))
	for _, l := range m.landed {
		pending = append(pending, l.write)
	}

	pending = append(pending, m.writes...)

	for _, w := range pending {
		i := indexOf(len(mails), func(i int) bool { return mails[i].ThreadID == w.threadID })
		switch {
		case i < 0:
		case w.archive:
			mails = slices.Delete(mails, i, i+1)
		default:
			mails[i].Unread = w.unread
		}
	}

	return mails
}

func (m Model) loading() bool {
	return m.agendaGen > m.agendaSeen || m.inboxGen > m.inboxSeen
}

func (m Model) reload() (Model, tea.Cmd) {
	if m.src == nil {
		return m, nil
	}

	m.agendaGen++
	m, inbox := m.reloadInbox()

	return m, tea.Batch(m.loadAgenda(m.agendaGen), inbox)
}

func (m Model) reloadInbox() (Model, tea.Cmd) {
	m.inboxGen++
	return m, m.loadInbox(m.inboxGen)
}

// wantBody fetches the selected thread's body when its detail is on screen
// and the body is not loaded or loading yet.
func (m Model) wantBody() (Model, tea.Cmd) {
	if m.src == nil || m.focus != paneInbox || len(m.mails) == 0 || !m.geometry().showDetail {
		return m, nil
	}

	ml := m.mails[m.sel[paneInbox]]
	if ml.Body != "" || m.fetching[ml.ThreadID] {
		return m, nil
	}

	m.fetching = copyFlags(m.fetching)
	m.fetching[ml.ThreadID] = true

	return m, m.loadBody(ml.ThreadID, ml.Messages)
}

func copyFlags(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in)+1)
	for k, v := range in {
		out[k] = v
	}

	return out
}

func (m Model) applyAgenda(msg agendaMsg) Model {
	if msg.gen < m.agendaSeen {
		return m
	}

	m.agendaSeen = msg.gen
	if !msg.day.Equal(m.day) {
		// Loaded for a day the user has since left.
		return m
	}

	m.agendaFailed = msg.err != nil
	if msg.err != nil {
		m.status = fmt.Sprintf("agenda: %v", msg.err)
		return m
	}

	keep := ""
	if len(m.events) > 0 {
		keep = m.events[m.sel[paneAgenda]].ID
	}

	first := m.events == nil
	m.events = sortEvents(msg.events)

	m.sel[paneAgenda] = indexOf(len(m.events), func(i int) bool { return m.events[i].ID == keep })
	if first || m.sel[paneAgenda] < 0 {
		m.sel[paneAgenda] = m.nowEventIndex()
	}

	return m
}

func (m Model) applyInbox(msg inboxMsg) Model {
	if msg.gen < m.inboxSeen {
		return m
	}

	m.inboxSeen = msg.gen
	if msg.err != nil {
		m.status = fmt.Sprintf("inbox: %v", msg.err)
		return m
	}

	m.landed = slices.DeleteFunc(slices.Clone(m.landed), func(l landed) bool { return l.until <= msg.gen })

	keep := ""
	if len(m.mails) > 0 {
		keep = m.mails[m.sel[paneInbox]].ThreadID
	}

	// A loaded body stays while the thread has the same messages.
	loaded := make(map[string]item.Mail, len(m.mails))
	for _, ml := range m.mails {
		loaded[ml.ThreadID] = ml
	}

	m.mails = m.overlay(slices.Clone(msg.mails))
	for i := range m.mails {
		if old, ok := loaded[m.mails[i].ThreadID]; ok && m.mails[i].Body == "" && old.Messages == m.mails[i].Messages {
			m.mails[i].Body = old.Body
		}
	}

	m.sel[paneInbox] = max(indexOf(len(m.mails), func(i int) bool { return m.mails[i].ThreadID == keep }), 0)

	return m
}

func (m Model) applyBody(msg bodyMsg) Model {
	m.fetching = copyFlags(m.fetching)
	delete(m.fetching, msg.threadID)

	if msg.err != nil {
		// Leave the thread marked as fetching so the detail does not retry on
		// every frame; "r" clears it.
		m.fetching[msg.threadID] = true
		m.status = fmt.Sprintf("thread: %v", msg.err)

		return m
	}

	if msg.body == "" {
		msg.body = "(no text body)"
	}

	m.mails = slices.Clone(m.mails)
	for i := range m.mails {
		if m.mails[i].ThreadID == msg.threadID && m.mails[i].Messages == msg.messages {
			m.mails[i].Body = msg.body
		}
	}

	return m
}

func indexOf(n int, match func(int) bool) int {
	for i := range n {
		if match(i) {
			return i
		}
	}

	return -1
}
