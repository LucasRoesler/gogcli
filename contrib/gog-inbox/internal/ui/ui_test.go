package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/openclaw/gogcli/contrib/gog-inbox/internal/fake"
	"github.com/openclaw/gogcli/contrib/gog-inbox/internal/item"
)

var testNow = time.Date(2026, 11, 9, 11, 10, 0, 0, time.Local)

func newTestModel(w, h int, opened *string) Model {
	m := New(Options{
		Now:    func() time.Time { return testNow },
		Events: fake.Agenda(testNow),
		Mails:  fake.Inbox(testNow),
		Open: func(u string) error {
			if opened != nil {
				*opened = u
			}

			return nil
		},
	})

	return m.SetSize(w, h)
}

func lineWith(t *testing.T, m Model, substr string) int {
	t.Helper()

	for y, l := range m.Frame() {
		if strings.Contains(l, substr) {
			return y
		}
	}

	t.Fatalf("no line contains %q in:\n%s", substr, strings.Join(m.Frame(), "\n"))

	return -1
}

func click(m Model, x, y int) (Model, tea.Cmd) {
	next, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	return next.(Model), cmd
}

// Every frame must be exactly the pane size, or zellij shows torn lines.
func TestFrameFillsThePaneExactlyAtEverySize(t *testing.T) {
	sizes := [][2]int{{90, 46}, {90, 22}, {46, 46}, {46, 22}, {188, 50}, {40, 10}, {30, 5}}

	keySets := [][]string{nil, {"enter"}, {"tab", "enter"}, {"G", "enter"}, {"tab", "e", "e"}, {"v"}, {"v", "tab", "enter"}, {"s"}, {"]"}, {"[", "enter"}}
	for _, s := range sizes {
		for _, keys := range keySets {
			t.Run(fmt.Sprintf("%dx%d_%s", s[0], s[1], strings.Join(keys, "+")), func(t *testing.T) {
				m := newTestModel(s[0], s[1], nil).Press(keys...)

				lines, _ := m.frame()
				if len(lines) != s[1] {
					t.Fatalf("got %d lines, want %d", len(lines), s[1])
				}

				for i, l := range lines {
					if got := lipgloss.Width(l); got != s[0] {
						t.Fatalf("line %d is %d cells wide, want %d: %q", i, got, s[0], l)
					}
				}
			})
		}
	}
}

func nowMarks(m Model) map[string]string {
	marks := map[string]string{}

	for _, l := range m.Frame() {
		if r := []rune(l); len(r) > 0 && (r[0] == '┃' || r[0] == '›') {
			marks[strings.TrimSpace(string(r[1:]))] = string(r[0])
		}
	}

	return marks
}

func TestNowMarkIsALineBesideTheMeetingInProgress(t *testing.T) {
	marks := nowMarks(newTestModel(90, 46, nil))
	if len(marks) != 1 {
		t.Fatalf("want one marked row, got %v", marks)
	}

	for row, mark := range marks {
		if mark != "┃" || !strings.Contains(row, "Design review") {
			t.Fatalf("marked %q with %q", row, mark)
		}
	}
}

func gapModel() Model {
	gap := testNow.Add(30 * time.Minute)

	return New(Options{
		Now:    func() time.Time { return gap },
		Events: fake.Agenda(testNow),
		Mails:  fake.Inbox(testNow),
	}).SetSize(90, 46)
}

func TestOnlyTheUnfocusedListShowsASelectionPointer(t *testing.T) {
	var pointers []string

	for _, l := range newTestModel(90, 46, nil).Frame() {
		if strings.Contains(l, "▶") {
			pointers = append(pointers, l)
		}
	}

	if len(pointers) != 1 || !strings.Contains(pointers[0], "Kim Hofmann") {
		t.Fatalf("pointers on %q, want only the inbox's selected row", pointers)
	}
}

func TestNowMarkIsAnArrowOnTheNextMeetingDuringAGap(t *testing.T) {
	m := gapModel()

	marks := nowMarks(m)
	if len(marks) != 1 {
		t.Fatalf("want one marked row, got %v", marks)
	}

	for row, mark := range marks {
		if mark != "›" || !strings.Contains(row, "Customer sync (Zoom)") {
			t.Fatalf("marked %q with %q", row, mark)
		}
	}
}

func TestStartupSelectsTheEventInProgress(t *testing.T) {
	m := newTestModel(90, 46, nil)
	if got := m.events[m.sel[paneAgenda]].Title; got != "Design review: gog-inbox wireframe" {
		t.Fatalf("selected %q", got)
	}
}

func TestClickingAnAgendaRowShowsItsLocationAndDirections(t *testing.T) {
	m := newTestModel(90, 46, nil)

	m, _ = click(m, 10, lineWith(t, m, "Lunch at the office"))
	if !m.detail {
		t.Fatal("click did not open the detail")
	}

	lineWith(t, m, "WHERE  Torstraße 1, 10119 Berlin")
	lineWith(t, m, "click for directions")
}

func TestClickingTheJoinLineOpensTheMeetingLink(t *testing.T) {
	var opened string
	m := newTestModel(90, 22, &opened).Press("enter")

	_, cmd := click(m, 3, lineWith(t, m, "JOIN"))
	if cmd == nil {
		t.Fatal("click on JOIN returned no command")
	}

	cmd()

	if opened != "https://meet.google.com/kqx-ptrm-wze" {
		t.Fatalf("opened %q", opened)
	}
}

func TestOKeyOpensZoomLinkPastedIntoTheInvite(t *testing.T) {
	var opened string
	m := newTestModel(90, 46, &opened).Press("j")
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	cmd()

	if opened != "https://acme.zoom.us/j/9876543210" {
		t.Fatalf("opened %q", opened)
	}
}

func TestDetailKeepsTheAgendaVisibleOnlyWhenThereIsRoom(t *testing.T) {
	half := newTestModel(90, 46, nil).Press("enter")
	lineWith(t, half, "AGENDA")
	lineWith(t, half, "JOIN")

	quarter := newTestModel(90, 22, nil).Press("enter")
	lineWith(t, quarter, "JOIN")

	for _, l := range quarter.Frame() {
		if strings.Contains(l, "AGENDA") {
			t.Fatal("quarter pane squeezed the detail under the agenda")
		}
	}
}

func TestArchiveRemovesTheThreadLocally(t *testing.T) {
	m := newTestModel(90, 46, nil).Press("tab", "e")
	if len(m.mails) != 7 || m.mails[0].Subject == "Agenda for today's sync" {
		t.Fatalf("archive left %d mails, first %q", len(m.mails), m.mails[0].Subject)
	}
}

func frameHas(m Model, substr string) bool {
	return strings.Contains(strings.Join(m.Frame(), "\n"), substr)
}

func TestTabsLayoutShowsOnlyTheAgendaFirstThenTheInbox(t *testing.T) {
	m := New(Options{
		Now:    func() time.Time { return testNow },
		Layout: LayoutTabs,
		Events: fake.Agenda(testNow),
		Mails:  fake.Inbox(testNow),
	}).SetSize(90, 46)

	if !frameHas(m, "Design review") || frameHas(m, "Kim Hofmann") {
		t.Fatalf("tabs layout should open on the agenda alone:\n%s", strings.Join(m.Frame(), "\n"))
	}

	m = m.Press("tab")
	if frameHas(m, "Design review") || !frameHas(m, "Kim Hofmann") {
		t.Fatalf("tab should show the inbox alone:\n%s", strings.Join(m.Frame(), "\n"))
	}
}

func TestVKeyTogglesBetweenStackedAndTabs(t *testing.T) {
	m := newTestModel(90, 46, nil)
	if !frameHas(m, "Kim Hofmann") {
		t.Fatal("stacked layout should show the inbox under the agenda")
	}

	if m = m.Press("v"); frameHas(m, "Kim Hofmann") {
		t.Fatal("v should switch to tabs and hide the inbox")
	}

	if m = m.Press("v"); !frameHas(m, "Kim Hofmann") {
		t.Fatal("a second v should switch back to stacked")
	}
}

func TestSKeyTogglesTheClockSeconds(t *testing.T) {
	m := newTestModel(90, 46, nil)
	withSeconds := testNow.Format("15:04:05")

	if !strings.Contains(m.Frame()[0], withSeconds) {
		t.Fatalf("header %q, want seconds by default", m.Frame()[0])
	}

	m = m.Press("s")
	if h := m.Frame()[0]; strings.Contains(h, withSeconds) || !strings.Contains(h, testNow.Format("15:04")) {
		t.Fatalf("header after s: %q", h)
	}

	if h := m.Press("s").Frame()[0]; !strings.Contains(h, withSeconds) {
		t.Fatalf("header after s s: %q", h)
	}
}

func TestFixturesOnAnotherDayShowNoEvents(t *testing.T) {
	m := newTestModel(90, 46, nil).Press("]")
	if len(m.events) != 0 || !frameHas(m, "no events") || !frameHas(m, "tomorrow") {
		t.Fatalf("frame:\n%s", strings.Join(m.Frame(), "\n"))
	}

	if m = m.Press("["); len(m.events) == 0 || len(nowMarks(m)) != 1 {
		t.Fatalf("back on today: %d events, marks %v", len(m.events), nowMarks(m))
	}
}

func TestOnlyTodayShowsAMeetingInProgress(t *testing.T) {
	overnight := item.Event{
		ID: "overnight", Title: "Overnight deploy",
		Start: testNow.Add(-15 * time.Hour), End: testNow.Add(time.Hour),
	}
	tomorrow := item.Event{
		ID: "tomorrow", Title: "Tomorrow's standup",
		Start: testNow.AddDate(0, 0, 1), End: testNow.AddDate(0, 0, 1).Add(15 * time.Minute),
	}
	m := New(Options{Now: func() time.Time { return testNow }, Events: []item.Event{overnight, tomorrow}}).SetSize(90, 46)

	if len(m.events) != 1 || m.events[0].ID != "overnight" {
		t.Fatalf("today shows %v, want only the overnight deploy", m.events)
	}

	if m = m.Press("["); !frameHas(m, "Overnight deploy") || len(nowMarks(m)) != 0 {
		t.Fatalf("yesterday (marks %v):\n%s", nowMarks(m), strings.Join(m.Frame(), "\n"))
	}
}
