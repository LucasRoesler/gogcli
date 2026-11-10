// Package ui is the Bubble Tea model for gog-inbox: today's agenda with a now
// mark, the inbox, and a detail view for the selected item.
package ui

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/openclaw/gogcli/contrib/gog-inbox/internal/item"
)

type pane int

const (
	paneAgenda pane = iota
	paneInbox
)

// Layout picks how the two lists share the pane.
type Layout int

const (
	// LayoutStacked shows the agenda over the inbox when the pane is tall
	// enough, and falls back to tabs in short panes.
	LayoutStacked Layout = iota
	// LayoutTabs shows one list at a time, the agenda first.
	LayoutTabs
)

var errUnknownLayout = errors.New("unknown layout, want stacked or tabs")

// ParseLayout reads a --layout value.
func ParseLayout(s string) (Layout, error) {
	switch s {
	case "stacked":
		return LayoutStacked, nil
	case "tabs":
		return LayoutTabs, nil
	}

	return 0, fmt.Errorf("%w: %q", errUnknownLayout, s)
}

type Options struct {
	Now    func() time.Time
	Layout Layout
	// Source loads both lists and carries out archive and read/unread. Without one,
	// Events and Mails are the whole data set and actions only change local
	// state.
	Source Source
	// Refresh is how often Source reloads both lists; 0 reloads only on "r".
	Refresh time.Duration
	Events  []item.Event
	Mails   []item.Mail
	// Preloaded says Events and Mails are Source's current data, so the
	// model starts without "loading" (a --dump frame never runs Init).
	Preloaded bool
	// Open hands a URL to the desktop (xdg-open).
	Open func(url string) error
}

type Model struct {
	w, h  int
	clock func() time.Time
	now   time.Time
	// day is midnight of the date the agenda shows; "[" and "]" move it.
	day    time.Time
	events []item.Event
	// fixtures holds every fixture event, filtered to day, when there is no
	// Source.
	fixtures []item.Event
	mails    []item.Mail
	open     func(string) error
	layout   Layout
	src      Source
	refresh  time.Duration
	// hideSeconds drops the seconds from the header clock ("s").
	hideSeconds bool
	// agendaGen and inboxGen number the loads issued; agendaSeen and
	// inboxSeen the newest applied. A gap shows "loading".
	agendaGen, agendaSeen int
	inboxGen, inboxSeen   int
	// agendaFailed says the newest agenda load for day failed, so an empty
	// agenda is not shown as "no events".
	agendaFailed bool
	// writes queues archive and read/unread changes, the head in flight;
	// landed holds confirmed ones that older loads may not show yet.
	writes []write
	landed []landed
	// fetching holds thread IDs whose body is loading or failed to load.
	fetching map[string]bool

	focus  pane
	sel    [2]int
	off    [2]int
	detail bool
	dOff   int
	status string
}

type (
	tickMsg   time.Time
	statusMsg string
)

func New(o Options) Model {
	m := Model{
		w: 80, h: 24,
		clock: o.Now, now: o.Now(),
		events: sortEvents(o.Events), mails: append([]item.Mail(nil), o.Mails...),
		open: o.Open, layout: o.Layout, src: o.Source, refresh: o.Refresh,
	}
	m.day = startOfDay(m.now)

	if m.src == nil {
		m.fixtures, m.events = m.events, onDay(m.events, m.day)
	}

	m.sel[paneAgenda] = m.nowEventIndex()

	if m.src != nil {
		m.agendaGen, m.inboxGen = 1, 1
		if o.Preloaded {
			m.agendaSeen, m.inboxSeen = 1, 1
		}
	}

	return m.normalize()
}

// sortEvents copies events with all-day ones first, then by start time.
func sortEvents(in []item.Event) []item.Event {
	events := append([]item.Event(nil), in...)
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].AllDay != events[j].AllDay {
			return events[i].AllDay
		}

		return events[i].Start.Before(events[j].Start)
	})

	return events
}

// SetSize sets the pane size without a WindowSizeMsg, for frame dumps.
func (m Model) SetSize(w, h int) Model {
	m.w, m.h = w, h
	return m.normalize()
}

// Press applies key presses without running their commands, for frame dumps
// and tests.
func (m Model) Press(keys ...string) Model {
	for _, k := range keys {
		m, _ = m.key(k)
		m = m.normalize()
	}

	return m
}

// tick fires on each whole second, so the header clock and the now mark
// change with the system clock. It only redraws; Google reloads on refresh.
func tick() tea.Cmd {
	return tea.Every(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) Init() tea.Cmd {
	if m.src == nil {
		return tick()
	}

	return tea.Batch(tick(), m.refreshTick(), m.loadAgenda(m.agendaGen), m.loadInbox(m.inboxGen))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tickMsg:
		wasToday := m.today()
		m.now = m.clock()
		cmd = tick()

		// Past midnight, an agenda showing today moves on to the new day.
		if wasToday && !m.today() {
			m, cmd = m.showDay(startOfDay(m.now))
			cmd = tea.Batch(cmd, tick())
		}
	case statusMsg:
		m.status = string(msg)
	case refreshMsg:
		m, cmd = m.reload()
		cmd = tea.Batch(cmd, m.refreshTick())
	case agendaMsg:
		m = m.applyAgenda(msg)
	case inboxMsg:
		m = m.applyInbox(msg)
	case bodyMsg:
		m = m.applyBody(msg)
	case writeMsg:
		m, cmd = m.applyWrite(msg)
	case tea.KeyPressMsg:
		m.status = ""
		m, cmd = m.key(msg.String())
	case tea.MouseClickMsg:
		m.status = ""
		m, cmd = m.click(msg.Mouse())
	case tea.MouseWheelMsg:
		m = m.wheel(msg.Mouse())
	}

	m = m.normalize()

	m, body := m.wantBody()

	return m, tea.Batch(cmd, body)
}

func (m Model) key(k string) (Model, tea.Cmd) {
	switch k {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		m = m.move(1)
	case "k", "up":
		m = m.move(-1)
	case "g", "home":
		m.sel[m.focus], m.dOff = 0, 0
	case "G", "end":
		m.sel[m.focus], m.dOff = m.count(m.focus)-1, 0
	case "tab", "shift+tab":
		m = m.focusPane(1 - m.focus)
	case "v":
		m.layout = 1 - m.layout
	case "s":
		m.hideSeconds = !m.hideSeconds
	case "a":
		m = m.focusPane(paneAgenda)
	case "i":
		m = m.focusPane(paneInbox)
	case "enter", "l", "space":
		m.detail, m.dOff = m.count(m.focus) > 0, 0
	case "esc", "h", "backspace":
		m.detail = false
	case "n":
		m = m.focusPane(paneAgenda)
		if !m.today() {
			return m.showDay(startOfDay(m.now))
		}

		m.sel[paneAgenda] = m.nowEventIndex()
	case "[":
		return m.focusPane(paneAgenda).showDay(m.day.AddDate(0, 0, -1))
	case "]":
		return m.focusPane(paneAgenda).showDay(m.day.AddDate(0, 0, 1))
	case "t":
		return m.focusPane(paneAgenda).showDay(startOfDay(m.now))
	case "ctrl+d", "pgdown":
		m.dOff += 5
	case "ctrl+u", "pgup":
		m.dOff -= 5
	case "o":
		return m, m.openCmd(m.primaryURL())
	case "m":
		if e, ok := m.selectedEvent(); ok {
			return m, m.openCmd(e.DirectionsURL())
		}
	case "y":
		if u := m.primaryURL(); u != "" {
			return m, tea.Batch(tea.SetClipboard(u), status("copied "+shortURL(u)))
		}

		return m, status("nothing to copy")
	case "e":
		return m.archive()
	case "u":
		return m.toggleUnread()
	case "r":
		if m.src == nil {
			return m, status("fixtures: nothing to refresh")
		}

		m.fetching = nil // retry bodies that failed

		return m.reload()
	}

	return m, nil
}

func (m Model) focusPane(p pane) Model {
	if m.focus != p {
		m.focus, m.detail, m.dOff = p, false, 0
	}

	return m
}

func (m Model) move(d int) Model {
	m.sel[m.focus] += d
	m.dOff = 0

	return m
}

func (m Model) count(p pane) int {
	if p == paneAgenda {
		return len(m.events)
	}

	return len(m.mails)
}

func (m Model) archive() (Model, tea.Cmd) {
	i, ok := m.selectedMailIndex()
	if !ok {
		return m, nil
	}
	subject := m.mails[i].Subject

	threadID := m.mails[i].ThreadID

	m.mails = append(m.mails[:i:i], m.mails[i+1:]...)
	if len(m.mails) == 0 {
		m.detail = false
	}

	done := fmt.Sprintf("archived %q", subject)
	if m.src == nil {
		return m, status(done + " (fixtures)")
	}

	return m.enqueue(write{threadID: threadID, archive: true, done: done})
}

func (m Model) toggleUnread() (Model, tea.Cmd) {
	i, ok := m.selectedMailIndex()
	if !ok {
		return m, nil
	}

	m.mails = append([]item.Mail(nil), m.mails...)
	m.mails[i].Unread = !m.mails[i].Unread
	threadID, unread := m.mails[i].ThreadID, m.mails[i].Unread

	done := "marked " + map[bool]string{true: "unread", false: "read"}[unread]
	if m.src == nil {
		return m, status(done + " (fixtures)")
	}

	return m.enqueue(write{threadID: threadID, unread: unread, done: done})
}

func (m Model) click(ms tea.Mouse) (Model, tea.Cmd) {
	if ms.Button != tea.MouseLeft {
		return m, nil
	}

	_, hits := m.frame()
	for _, h := range hits {
		if ms.Y != h.y || ms.X < h.x0 || ms.X >= h.x1 {
			continue
		}

		switch h.kind {
		case hitFocus:
			return m.focusPane(h.pane), nil
		case hitSelect:
			m = m.focusPane(h.pane)
			m.sel[h.pane], m.detail, m.dOff = h.idx, true, 0

			return m, nil
		case hitOpen:
			return m, m.openCmd(h.url)
		}
	}

	return m, nil
}

func (m Model) wheel(ms tea.Mouse) Model {
	d := 1
	if ms.Button == tea.MouseWheelUp {
		d = -1
	}

	g := m.geometry()
	if g.showDetail && g.detail.contains(ms.X, ms.Y) {
		m.dOff += 3 * d
		return m
	}

	for _, p := range []pane{paneAgenda, paneInbox} {
		if g.show[p] && g.panes[p].contains(ms.X, ms.Y) {
			m = m.focusPane(p)
			return m.move(d)
		}
	}

	return m
}

func (m Model) openCmd(u string) tea.Cmd {
	if u == "" {
		return status("no link for this item")
	}

	return func() tea.Msg {
		if m.open == nil {
			return statusMsg("would open " + shortURL(u))
		}

		if err := m.open(u); err != nil {
			return statusMsg("open failed: " + err.Error())
		}

		return statusMsg("opened " + shortURL(u))
	}
}

func status(s string) tea.Cmd {
	return func() tea.Msg { return statusMsg(s) }
}

// primaryURL is what "o" opens: the meeting link, else directions, else the
// Gmail thread.
func (m Model) primaryURL() string {
	if e, ok := m.selectedEvent(); ok {
		if u := e.JoinURL(); u != "" {
			return u
		}

		return e.DirectionsURL()
	}

	if i, ok := m.selectedMailIndex(); ok {
		return m.mails[i].GmailURL()
	}

	return ""
}

func (m Model) selectedEvent() (item.Event, bool) {
	if m.focus != paneAgenda || len(m.events) == 0 {
		return item.Event{}, false
	}

	return m.events[m.sel[paneAgenda]], true
}

func (m Model) selectedMailIndex() (int, bool) {
	if m.focus != paneInbox || len(m.mails) == 0 {
		return 0, false
	}

	return m.sel[paneInbox], true
}

// nowEventIndex picks the event in progress, else the next one, else the last.
func (m Model) nowEventIndex() int {
	if !m.today() {
		// Another day has no now: start at its first timed event.
		return max(indexOf(len(m.events), func(i int) bool { return !m.events[i].AllDay }), 0)
	}

	next := -1

	for i, e := range m.events {
		if e.InProgress(m.now) {
			return i
		}

		if next < 0 && !e.AllDay && e.Start.After(m.now) {
			next = i
		}
	}

	if next >= 0 {
		return next
	}

	return max(len(m.events)-1, 0)
}

// normalize clamps selections and scrolls each list so its selection is visible.
func (m Model) normalize() Model {
	for _, p := range []pane{paneAgenda, paneInbox} {
		m.sel[p] = clamp(m.sel[p], 0, max(m.count(p)-1, 0))
	}

	if m.count(m.focus) == 0 {
		m.detail = false
	}
	m.dOff = max(m.dOff, 0)

	g := m.geometry()
	for _, p := range []pane{paneAgenda, paneInbox} {
		rows := g.rows(p)
		if rows <= 0 {
			continue
		}
		line := m.sel[p]

		total := len(m.mails)
		if p == paneAgenda {
			total = len(m.events)
		}

		off := m.off[p]
		if line < off {
			off = line
		}

		if line >= off+rows {
			off = line - rows + 1
		}
		m.off[p] = clamp(off, 0, max(total-rows, 0))
	}

	return m
}

func clamp(v, lo, hi int) int {
	return min(max(v, lo), hi)
}

func shortURL(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	s := p.Host + p.Path

	return strings.TrimSuffix(s, "/")
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// today reports whether the agenda shows the current date, the only day with
// a now mark.
func (m Model) today() bool {
	return m.day.Equal(startOfDay(m.now))
}

// onDay keeps the events that start on the date starting at day or run into
// it from an earlier date.
func onDay(events []item.Event, day time.Time) []item.Event {
	next := day.AddDate(0, 0, 1)

	var out []item.Event

	for _, e := range events {
		startsThatDay := !e.Start.Before(day) && e.Start.Before(next)
		if startsThatDay || e.Start.Before(day) && e.End.After(day) {
			out = append(out, e)
		}
	}

	return out
}

// showDay switches the agenda to the date starting at day and loads it.
func (m Model) showDay(day time.Time) (Model, tea.Cmd) {
	if day.Equal(m.day) {
		return m, nil
	}

	m.day, m.off[paneAgenda] = day, 0

	if m.src == nil {
		m.events = onDay(m.fixtures, day)
		m.sel[paneAgenda] = m.nowEventIndex()

		return m, nil
	}

	// nil events make applyAgenda pick the selection afresh.
	m.events, m.sel[paneAgenda], m.agendaFailed = nil, 0, false
	m.agendaGen++

	return m, m.loadAgenda(m.agendaGen)
}
