package ui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/openclaw/gogcli/contrib/gog-inbox/internal/fake"
	"github.com/openclaw/gogcli/contrib/gog-inbox/internal/item"
)

type stubSource struct {
	archived, bodies []string
	unread           map[string]bool
	// writes logs every write in the order Google received it.
	writes     []string
	failWrites bool
	// messages overrides a thread's message count, as a new reply does.
	messages map[string]int
	// days logs the date of every agenda load.
	days       []string
	failAgenda bool
}

var errStub = errors.New("stub refused")

func (s *stubSource) Name() string { return "me@example.com" }

func (s *stubSource) Agenda(_ context.Context, day time.Time) ([]item.Event, error) {
	s.days = append(s.days, day.Format(time.DateOnly))
	if s.failAgenda {
		return nil, errStub
	}

	return fake.Agenda(day.Add(testNow.Sub(startOfDay(testNow)))), nil
}

func (s *stubSource) Inbox(context.Context) ([]item.Mail, error) {
	mails := fake.Inbox(testNow)
	for i := range mails {
		mails[i].Body, mails[i].Messages = "", 1
		if n, ok := s.messages[mails[i].ThreadID]; ok {
			mails[i].Messages = n
		}
	}

	return mails, nil
}

func (s *stubSource) Body(_ context.Context, id string) (string, error) {
	s.bodies = append(s.bodies, id)
	return fmt.Sprintf("full body of %s, message %d", id, max(s.messages[id], 1)), nil
}

func (s *stubSource) Archive(_ context.Context, id string) error {
	if s.failWrites {
		return errStub
	}

	s.archived = append(s.archived, id)
	s.writes = append(s.writes, "archive "+id)

	return nil
}

func (s *stubSource) SetUnread(_ context.Context, id string, unread bool) error {
	if s.failWrites {
		return errStub
	}

	if s.unread == nil {
		s.unread = map[string]bool{}
	}

	s.unread[id] = unread
	s.writes = append(s.writes, fmt.Sprintf("unread=%v %s", unread, id))

	return nil
}

// drain runs a command and feeds every message it produces back into the
// model, the way the Bubble Tea runtime does, skipping timers.
func drain(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()

	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 50 {
			t.Fatal("commands did not settle")
		}

		c := queue[0]
		queue = queue[1:]

		if c == nil {
			continue
		}

		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()

		var msg tea.Msg
		select {
		case msg = <-done:
		case <-time.After(100 * time.Millisecond):
			continue // a tea.Tick timer
		}

		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}

		next, more := m.Update(msg)
		m = next.(Model)

		queue = append(queue, more)
	}

	return m
}

func newSourced(t *testing.T, src *stubSource) Model {
	t.Helper()

	m := New(Options{Now: func() time.Time { return testNow }, Source: src}).SetSize(90, 46)

	return drain(t, m, m.Init())
}

func press(t *testing.T, m Model, k string) Model {
	t.Helper()

	key := tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
	switch k {
	case "tab":
		key = tea.KeyPressMsg{Code: tea.KeyTab}
	case "enter":
		key = tea.KeyPressMsg{Code: tea.KeyEnter}
	}

	next, cmd := m.Update(key)

	return drain(t, next.(Model), cmd)
}

func TestSourceLoadsBothListsAndSelectsTheMeetingInProgress(t *testing.T) {
	m := newSourced(t, &stubSource{})

	if len(m.events) == 0 || len(m.mails) == 0 {
		t.Fatalf("loaded %d events, %d mails", len(m.events), len(m.mails))
	}

	if got := m.events[m.sel[paneAgenda]].Title; got != "Design review: gog-inbox wireframe" {
		t.Fatalf("selected %q", got)
	}

	if frameHas(m, "loading") || !frameHas(m, "me@example.com") {
		t.Fatalf("header after load:\n%s", m.Frame()[0])
	}
}

func TestOpeningAMailFetchesItsBodyOnce(t *testing.T) {
	src := &stubSource{}
	m := press(t, press(t, newSourced(t, src), "tab"), "enter")
	m = press(t, m, "y") // any later update must not refetch

	if len(src.bodies) != 1 {
		t.Fatalf("fetched bodies %v, want one", src.bodies)
	}

	if !frameHas(m, "full body of "+src.bodies[0]+", message 1") {
		t.Fatalf("body not shown:\n%s", strings.Join(m.Frame(), "\n"))
	}
}

func TestArchiveAndMarkReadReachTheSource(t *testing.T) {
	src := &stubSource{}
	m := press(t, newSourced(t, src), "tab")
	first, second := m.mails[0].ThreadID, m.mails[1].ThreadID

	m = press(t, m, "e")
	m = press(t, m, "u")

	if len(src.archived) != 1 || src.archived[0] != first {
		t.Fatalf("archived %v, want [%s]", src.archived, first)
	}

	if unread, ok := src.unread[second]; !ok || unread {
		t.Fatalf("SetUnread calls %v, want %s read", src.unread, second)
	}

	if !strings.Contains(m.status, "marked read") {
		t.Fatalf("status %q", m.status)
	}
}

func TestFailedArchiveReloadsTheInboxAndSaysSo(t *testing.T) {
	src := &stubSource{failWrites: true}
	m := press(t, newSourced(t, src), "tab")
	before := len(m.mails)

	m = press(t, m, "e")

	if len(m.mails) != before {
		t.Fatalf("inbox has %d threads after a failed archive, want %d back", len(m.mails), before)
	}

	if !strings.Contains(m.status, "failed: stub refused") {
		t.Fatalf("status %q", m.status)
	}
}

// update feeds one message to the model without running the commands it
// returns.
func update(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

func keyMsg(k string) tea.Msg {
	if k == "tab" {
		return tea.KeyPressMsg{Code: tea.KeyTab}
	}

	return tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
}

// collect runs a command and returns the messages it produces, expanding
// batches and skipping timers, without feeding them to the model.
func collect(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()

	var msgs []tea.Msg

	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]

		if c == nil {
			continue
		}

		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()

		select {
		case msg := <-done:
			if batch, ok := msg.(tea.BatchMsg); ok {
				queue = append(queue, batch...)
			} else if msg != nil {
				msgs = append(msgs, msg)
			}
		case <-time.After(100 * time.Millisecond):
		}
	}

	return msgs
}

func TestQuickUnreadTogglesReachGoogleInKeypressOrder(t *testing.T) {
	src := &stubSource{}
	m := press(t, newSourced(t, src), "tab")
	id, wasUnread := m.mails[0].ThreadID, m.mails[0].Unread

	cmds := make([]tea.Cmd, 0, 3)

	for range 3 {
		var cmd tea.Cmd

		m, cmd = update(m, keyMsg("u"))
		cmds = append(cmds, cmd)
	}

	// Only the first press sends; the other two wait their turn.
	for _, cmd := range cmds[1:] {
		for _, msg := range collect(t, cmd) {
			if _, ok := msg.(writeMsg); ok {
				t.Fatal("a second write started while the first was in flight")
			}
		}
	}

	m = drain(t, m, cmds[0])

	want := []string{
		fmt.Sprintf("unread=%v %s", !wasUnread, id),
		fmt.Sprintf("unread=%v %s", wasUnread, id),
		fmt.Sprintf("unread=%v %s", !wasUnread, id),
	}
	if !slices.Equal(src.writes, want) {
		t.Fatalf("Google received %v, want %v", src.writes, want)
	}

	if m.mails[0].Unread != !wasUnread || len(m.writes) != 0 {
		t.Fatalf("screen shows unread=%v with %d writes queued", m.mails[0].Unread, len(m.writes))
	}
}

// The stub's inbox never drops archived threads, like a load Google answered
// before the archive reached it.
func TestReloadsThatPredateAnArchiveKeepTheThreadOut(t *testing.T) {
	src := &stubSource{}
	m := press(t, newSourced(t, src), "tab")
	id := m.mails[0].ThreadID

	m, archive := update(m, keyMsg("e"))

	// A refresh issued while the archive is in flight answers first.
	m, before := update(m, refreshMsg{})
	beforeMsgs := collect(t, before)

	for _, msg := range beforeMsgs {
		m, _ = update(m, msg)
	}

	if slices.ContainsFunc(m.mails, func(ml item.Mail) bool { return ml.ThreadID == id }) {
		t.Fatal("a reload during the archive brought the thread back")
	}

	// Another refresh issued before the archive lands, answered after it.
	m, late := update(m, refreshMsg{})
	lateMsgs := collect(t, late)

	for _, msg := range collect(t, archive) {
		m, _ = update(m, msg)
	}

	for _, msg := range lateMsgs {
		m, _ = update(m, msg)
	}

	if slices.ContainsFunc(m.mails, func(ml item.Mail) bool { return ml.ThreadID == id }) {
		t.Fatal("a reload issued before the archive landed brought the thread back")
	}

	// A reload issued after the archive landed shows Google's state, so the
	// archive is no longer laid over it.
	m = drain(t, m, func() tea.Msg { return refreshMsg{} })

	if !slices.ContainsFunc(m.mails, func(ml item.Mail) bool { return ml.ThreadID == id }) || len(m.landed) != 0 {
		t.Fatalf("after a fresh reload: thread shown %v, %d landed writes kept",
			slices.ContainsFunc(m.mails, func(ml item.Mail) bool { return ml.ThreadID == id }), len(m.landed))
	}
}

func TestASlowInboxLoadDoesNotOverwriteANewerOne(t *testing.T) {
	m := newSourced(t, &stubSource{})
	newer := m.mails[:1]

	m, _ = update(m, refreshMsg{})
	m, _ = update(m, refreshMsg{})
	m, _ = update(m, inboxMsg{gen: m.inboxGen, mails: newer})
	m, _ = update(m, inboxMsg{gen: m.inboxGen - 1, mails: fake.Inbox(testNow)})

	if len(m.mails) != 1 {
		t.Fatalf("inbox has %d threads, want the newer load's 1", len(m.mails))
	}
}

func TestANewReplyReloadsTheOpenThreadsBody(t *testing.T) {
	src := &stubSource{}
	m := press(t, press(t, newSourced(t, src), "tab"), "enter")
	id := m.mails[0].ThreadID

	src.messages = map[string]int{id: 2}
	m = drain(t, m, func() tea.Msg { return refreshMsg{} })

	if len(src.bodies) != 2 || !frameHas(m, "full body of "+id+", message 2") {
		t.Fatalf("fetched %v; frame:\n%s", src.bodies, strings.Join(m.Frame(), "\n"))
	}
}

func TestAFailedWriteShowsTheRecoveryReload(t *testing.T) {
	m := press(t, newSourced(t, &stubSource{failWrites: true}), "tab")

	m, archive := update(m, keyMsg("e"))
	for _, msg := range collect(t, archive) {
		m, _ = update(m, msg)
	}

	if !frameHas(m, "loading") {
		t.Fatalf("header while the inbox reloads:\n%s", m.Frame()[0])
	}
}

func TestRefreshAcceptsOffOrTenSecondsAndUp(t *testing.T) {
	for d, ok := range map[time.Duration]bool{0: true, 10 * time.Second: true, 5 * time.Minute: true, time.Second: false, -time.Minute: false} {
		if err := CheckRefresh(d); (err == nil) != ok {
			t.Errorf("CheckRefresh(%v) = %v", d, err)
		}
	}
}

func TestRefreshZeroSchedulesNoReload(t *testing.T) {
	src := &stubSource{}
	if New(Options{Now: func() time.Time { return testNow }, Source: src}).refreshTick() != nil {
		t.Fatal("refresh 0 scheduled a reload")
	}

	if New(Options{Now: func() time.Time { return testNow }, Source: src, Refresh: time.Minute}).refreshTick() == nil {
		t.Fatal("refresh 1m scheduled no reload")
	}
}

func TestDayKeysLoadThatDaysAgendaWithoutANowMark(t *testing.T) {
	src := &stubSource{}
	m := press(t, newSourced(t, src), "]")

	today, tomorrow := testNow.Format(time.DateOnly), testNow.AddDate(0, 0, 1).Format(time.DateOnly)
	if !slices.Equal(src.days, []string{today, tomorrow}) {
		t.Fatalf("loaded days %v, want %s then %s", src.days, today, tomorrow)
	}

	if !frameHas(m, "tomorrow") || len(nowMarks(m)) != 0 {
		t.Fatalf("tomorrow's frame (marks %v):\n%s", nowMarks(m), strings.Join(m.Frame(), "\n"))
	}

	if m.events[m.sel[paneAgenda]].AllDay {
		t.Fatalf("selected the all-day %q, want the first timed event", m.events[m.sel[paneAgenda]].Title)
	}

	m = press(t, m, "t")
	if got := src.days[len(src.days)-1]; got != today || len(nowMarks(m)) != 1 {
		t.Fatalf("after t: loaded %s, marks %v", got, nowMarks(m))
	}
}

func TestAgendaLoadedForADayAlreadyLeftIsDropped(t *testing.T) {
	m := newSourced(t, &stubSource{})
	todays := m.events

	m, _ = update(m, keyMsg("]"))
	m, _ = update(m, agendaMsg{gen: m.agendaGen, day: startOfDay(testNow), events: todays})

	if len(m.events) != 0 {
		t.Fatalf("tomorrow's view shows %d of today's events", len(m.events))
	}
}

func TestAgendaShowingTodayMovesOnAtMidnight(t *testing.T) {
	src := &stubSource{}
	now := testNow
	m := New(Options{Now: func() time.Time { return now }, Source: src}).SetSize(90, 46)
	m = press(t, drain(t, m, m.Init()), "tab")

	now = startOfDay(testNow).AddDate(0, 0, 1).Add(time.Second)
	m = drain(t, m, func() tea.Msg { return tickMsg(now) })

	if !m.day.Equal(startOfDay(now)) || src.days[len(src.days)-1] != now.Format(time.DateOnly) {
		t.Fatalf("after midnight: day %v, loaded %v", m.day, src.days)
	}

	if m.focus != paneInbox {
		t.Fatal("the midnight switch moved focus off the inbox")
	}
}

func TestAFailedAgendaLoadIsNotShownAsAnEmptyDay(t *testing.T) {
	src := &stubSource{}
	m := newSourced(t, src)
	src.failAgenda = true

	if m = press(t, m, "]"); frameHas(m, "no events") || !frameHas(m, "could not load") {
		t.Fatalf("frame:\n%s", strings.Join(m.Frame(), "\n"))
	}
}

func TestAPreloadedFrameIsNotLoading(t *testing.T) {
	src := &stubSource{}
	mails, _ := src.Inbox(context.Background())
	m := New(Options{Now: func() time.Time { return testNow }, Source: src, Mails: mails, Preloaded: true}).SetSize(90, 46)

	if m.loading() || frameHas(m, "loading") || !frameHas(m, "no events") {
		t.Fatalf("frame:\n%s", strings.Join(m.Frame(), "\n"))
	}
}
