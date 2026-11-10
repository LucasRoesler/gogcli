package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/openclaw/gogcli/contrib/gog-inbox/internal/item"
)

// Layout thresholds, chosen for zellij panes on a 2256x1504 panel at 1.25 scale
// with a 12pt font: a half-screen pane is about 90x46, a quarter about 90x22.
const (
	tallMinHeight   = 18  // stacked layout: agenda over inbox; below this, one tab at a time
	wideMinWidth    = 140 // detail preview beside the lists
	detailMinHeight = 14  // event detail without scrolling: when, join, where, RSVP, guests
	minWidth        = 40
	minHeight       = 10
)

type rect struct{ x, y, w, h int }

func (r rect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

type geometry struct {
	tall, wide bool
	show       [2]bool
	panes      [2]rect
	showDetail bool
	detail     rect
}

// rows is the number of list rows a pane shows under its title line.
func (g geometry) rows(p pane) int {
	if !g.show[p] {
		return 0
	}

	if g.tall {
		return g.panes[p].h - 1
	}

	return g.panes[p].h
}

// geometry splits the screen. In the stacked layout, tall panes stack the
// agenda (sized to its rows, at most 60%) over the inbox; the detail replaces
// the other list when that leaves it detailMinHeight rows, else it covers both.
// The tabs layout and short panes show one list as a tab and open the detail
// over it. Wide panes keep a live preview on the right.
func (m Model) geometry() geometry {
	g := geometry{tall: m.layout == LayoutStacked && m.h >= tallMinHeight, wide: m.w >= wideMinWidth}

	list := rect{0, 1, m.w, m.h - 2}
	if g.wide {
		lw := m.w * 45 / 100
		g.detail = rect{lw + 1, 1, m.w - lw - 1, m.h - 2}
		g.showDetail = true
		list.w = lw
	}

	other := 1 - m.focus
	if g.tall {
		top := clamp(len(m.events)+1, 4, list.h*3/5)
		g.panes[paneAgenda] = rect{list.x, list.y, list.w, top}
		g.panes[paneInbox] = rect{list.x, list.y + top, list.w, list.h - top}

		g.show = [2]bool{true, true}
		if m.detail && !g.wide {
			g.showDetail = true
			if g.panes[other].h >= detailMinHeight {
				g.show[other], g.detail = false, g.panes[other]
			} else {
				g.show = [2]bool{}
				g.detail = list
			}
		}

		return g
	}
	g.panes[m.focus] = list

	g.show[m.focus] = true
	if m.detail && !g.wide {
		g.show[m.focus] = false
		g.showDetail, g.detail = true, list
	}

	return g
}

type hitKind int

const (
	hitFocus hitKind = iota
	hitSelect
	hitOpen
)

// hit is a clickable region in screen coordinates, produced by the same render
// pass that draws it so clicks always match what is on screen.
type hit struct {
	y, x0, x1 int
	kind      hitKind
	pane      pane
	idx       int
	url       string
}

func (m Model) View() tea.View {
	lines, _ := m.frame()
	v := tea.NewView(strings.Join(lines, "\n"))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "gog-inbox"

	return v
}

// Frame renders the screen as plain lines, ANSI stripped, for dumps and tests.
func (m Model) Frame() []string {
	lines, _ := m.frame()

	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Strip(l)
	}

	return out
}

func (m Model) frame() ([]string, []hit) {
	if m.w < minWidth || m.h < minHeight {
		msg := fmt.Sprintf("pane is %dx%d, gog-inbox needs %dx%d", m.w, m.h, minWidth, minHeight)
		lines := make([]string, max(m.h, 1))

		lines[0] = fit(msg, m.w)
		for i := 1; i < len(lines); i++ {
			lines[i] = fit("", m.w)
		}

		return lines, nil
	}
	g := m.geometry()
	var hits []hit
	header, hh := m.header(g)
	hits = append(hits, hh...)

	type block struct {
		r     rect
		lines []string
	}
	var left []block

	for _, p := range []pane{paneAgenda, paneInbox} {
		if !g.show[p] {
			continue
		}
		var ls []string

		var hs []hit
		if p == paneAgenda {
			ls, hs = m.agendaBlock(g, g.panes[p])
		} else {
			ls, hs = m.inboxBlock(g, g.panes[p])
		}
		left = append(left, block{g.panes[p], ls})
		hits = append(hits, hs...)
	}
	var detail []string

	if g.showDetail {
		var dh []hit
		detail, dh = m.detailBlock(g, g.detail)

		hits = append(hits, dh...)
		if !g.wide {
			left = append(left, block{g.detail, detail})
		}
	}

	body := make([]string, m.h-2)

	for _, b := range left {
		for i, l := range b.lines {
			body[b.r.y-1+i] = l
		}
	}

	if g.wide {
		sep := stDim.Render("│")
		for i := range body {
			body[i] = fit(body[i], g.detail.x-1) + sep + detail[i]
		}
	}

	lines := make([]string, 0, m.h)

	lines = append(lines, header)
	for _, l := range body {
		lines = append(lines, fit(l, m.w))
	}

	lines = append(lines, m.footer(g))

	return lines, hits
}

func (m Model) header(g geometry) (string, []hit) {
	layout := "Mon 2 Jan  15:04:05"
	if m.hideSeconds {
		layout = "Mon 2 Jan  15:04"
	}

	clock := m.now.Format(layout) + " "
	var left string
	var hits []hit

	if g.tall {
		left = stBold.Render(" gog-inbox") + stDim.Render(" · "+m.sourceName())
	} else {
		x := 1
		left = " "

		for _, p := range []pane{paneAgenda, paneInbox} {
			label := m.tabLabel(p)

			st := stTitleBlur
			if p == m.focus {
				st = stTab
			}
			left += st.Render(label) + " "
			hits = append(hits, hit{y: 0, x0: x, x1: x + lipgloss.Width(label), kind: hitFocus, pane: p})
			x += lipgloss.Width(label) + 1
		}
	}

	gap := m.w - lipgloss.Width(left) - lipgloss.Width(clock)
	if gap < 1 {
		return fit(left, m.w), hits
	}

	return left + strings.Repeat(" ", gap) + stBold.Render(clock), hits
}

func (m Model) sourceName() string {
	name := "fixtures"
	if m.src != nil {
		name = m.src.Name()
	}

	if m.loading() {
		name += " · loading…"
	}

	return name
}

func (m Model) tabLabel(p pane) string {
	if p == paneAgenda {
		if !m.today() {
			return " Agenda " + m.day.Format("Mon 2 Jan") + " "
		}

		return fmt.Sprintf(" Agenda %d left ", m.eventsLeft())
	}

	return fmt.Sprintf(" Inbox %d new ", m.unread())
}

func (m Model) eventsLeft() int {
	n := 0

	for _, e := range m.events {
		if !e.AllDay && !e.Past(m.now) {
			n++
		}
	}

	return n
}

func (m Model) unread() int {
	n := 0

	for _, ml := range m.mails {
		if ml.Unread {
			n++
		}
	}

	return n
}

func (m Model) footer(g geometry) string {
	if m.status != "" {
		return fit(" "+stWarn.Render(m.status), m.w)
	}
	var keys []string

	switch {
	case m.detail && m.focus == paneAgenda:
		keys = []string{"o join", "m directions", "y copy", "j/k next", "esc back", "q quit"}
	case m.detail:
		keys = []string{"o gmail", "e archive", "u read", "j/k next", "esc back", "q quit"}
	default:
		keys = []string{"j/k move", "⏎ details", "tab switch", "n now", "o open", "v view", "q quit"}
	}

	if g.wide && !m.detail {
		keys = append([]string{"o join/open", "m directions"}, keys[:len(keys)-2]...)
		keys = append(keys, "q quit")
	}

	return fit(" "+stDim.Render(strings.Join(keys, " · ")), m.w)
}

func paneTitle(name, info string, focused bool, w int) string {
	st := stTitleBlur
	if focused {
		st = stTitleFoc
	}

	return fit(st.Render(" "+name+" ")+" "+stDim.Render(info), w)
}

func (m Model) agendaBlock(g geometry, r rect) ([]string, []hit) {
	var out []string
	var hits []hit

	y := r.y
	if g.tall {
		out = append(out, paneTitle("AGENDA", m.agendaInfo(), m.focus == paneAgenda, r.w))
		hits = append(hits, hit{y: y, x0: r.x, x1: r.x + r.w, kind: hitFocus, pane: paneAgenda})
		y++
	}
	next := m.nextEventIndex()

	rows := g.rows(paneAgenda)
	for i := 0; i < rows; i++ {
		idx := m.off[paneAgenda] + i
		if idx >= len(m.events) {
			empty := ""

			switch {
			case i > 0 || m.loading():
			case m.agendaFailed:
				empty = stWarn.Render("   could not load · r retries")
			default:
				empty = stDim.Render("   no events")
			}

			out = append(out, fit(empty, r.w))

			continue
		}

		sel := idx == m.sel[paneAgenda]
		out = append(out, m.eventRow(m.events[idx], r.w, sel, sel && m.focus == paneAgenda, idx == next))
		hits = append(hits, hit{y: y + i, x0: r.x, x1: r.x + r.w, kind: hitSelect, pane: paneAgenda, idx: idx})
	}

	return out, hits
}

// nextEventIndex returns the first upcoming timed event when nothing is in
// progress, so the now mark sits on it; -1 while a meeting runs or after the
// last one ends.
func (m Model) nextEventIndex() int {
	if !m.today() {
		return -1
	}

	for _, e := range m.events {
		if !e.AllDay && e.InProgress(m.now) {
			return -1
		}
	}

	for i, e := range m.events {
		if !e.AllDay && e.Start.After(m.now) {
			return i
		}
	}

	return -1
}

// nowMark is the one-cell column left of the selection: a line beside the
// meeting in progress, or an arrow on the next one during a gap.
func (m Model) nowMark(e item.Event, next bool) string {
	switch {
	case m.today() && !e.AllDay && e.InProgress(m.now):
		return stNow.Render("┃")
	case next:
		return stNow.Render("›")
	}

	return " "
}

func meetingTag(e item.Event) string {
	u := e.JoinURL()
	switch {
	case strings.Contains(u, "meet.google.com"):
		return "meet"
	case strings.Contains(u, "zoom.us"):
		return "zoom"
	case strings.Contains(u, "teams."):
		return "teams"
	case u != "":
		return "video"
	case e.Place() != "":
		return "map"
	}

	return ""
}

func (m Model) eventRow(e item.Event, w int, selected, focused, next bool) string {
	mark := m.nowMark(e, next)
	w--

	timeW := 11
	if w < 56 {
		timeW = 7
	}

	tm := "all day"
	if !e.AllDay {
		tm = e.Start.Format("15:04")
		if timeW == 11 {
			tm += "–" + e.End.Format("15:04")
		}
	}
	tag := meetingTag(e)

	tagW := 0
	if w >= 44 {
		tagW = 6
	}
	prefix := ""

	switch e.Response {
	case "needsAction":
		prefix = "? "
	case "tentative":
		prefix = "~ "
	}
	titleW := max(w-2-timeW-1-tagW, 1)
	title := trunc(prefix+e.Title, titleW)

	// The pointer marks the selection in the unfocused list only; the
	// focused list highlights its whole row.
	gutter := "  "
	if selected && !focused {
		gutter = "▶ "
	}

	live := m.today() && e.InProgress(m.now)
	tmCell := pad(tm, timeW) + " "
	titleCell := pad(title, titleW)

	tagCell := ""
	if tagW > 0 {
		tagCell = fmt.Sprintf("%*s ", tagW-1, tag)
	}

	if focused {
		return mark + stSel.Render(fit(gutter+tmCell+titleCell+tagCell, w))
	}

	if e.Past(m.now) && !selected {
		return mark + stDim.Render(fit(gutter+tmCell+titleCell+tagCell, w))
	}

	if selected {
		gutter = stBold.Render(gutter)
	}

	if live {
		tmCell = stLive.Render(tmCell)
	}

	switch {
	case e.Response == "declined":
		titleCell = stStrike.Render(titleCell)
	case prefix != "":
		titleCell = stWarn.Render(prefix) + pad(trunc(e.Title, titleW-2), titleW-2)
	case live || selected:
		titleCell = stBold.Render(titleCell)
	}

	return mark + fit(gutter+tmCell+titleCell+stDim.Render(tagCell), w)
}

func (m Model) inboxBlock(g geometry, r rect) ([]string, []hit) {
	var out []string
	var hits []hit

	y := r.y
	if g.tall {
		out = append(out, paneTitle("INBOX", fmt.Sprintf("%d new · %d threads", m.unread(), len(m.mails)), m.focus == paneInbox, r.w))
		hits = append(hits, hit{y: y, x0: r.x, x1: r.x + r.w, kind: hitFocus, pane: paneInbox})
		y++
	}

	rows := g.rows(paneInbox)
	for i := 0; i < rows; i++ {
		idx := m.off[paneInbox] + i
		if idx >= len(m.mails) {
			if idx == 0 {
				out = append(out, fit(stDim.Render("  inbox zero"), r.w))
				continue
			}

			out = append(out, fit("", r.w))

			continue
		}
		sel := idx == m.sel[paneInbox]
		out = append(out, m.mailRow(m.mails[idx], r.w, sel, sel && m.focus == paneInbox))
		hits = append(hits, hit{y: y + i, x0: r.x, x1: r.x + r.w, kind: hitSelect, pane: paneInbox, idx: idx})
	}

	return out, hits
}

func (m Model) mailDate(t time.Time) string {
	switch {
	case sameDay(t, m.now):
		return t.Format("15:04")
	case m.now.Sub(t) < 6*24*time.Hour:
		return t.Format("Mon")
	}

	return t.Format("2 Jan")
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()

	return ay == by && am == bm && ad == bd
}

func (m Model) mailRow(ml item.Mail, w int, selected, focused bool) string {
	// The blank first cell lines the inbox up with the agenda's now-mark column.
	w--
	fromW := 0

	switch {
	case w >= 70:
		fromW = 16
	case w >= 48:
		fromW = 12
	}
	date := m.mailDate(ml.Date)
	const dateW = 6

	// The pointer marks the selection in the unfocused list only; the
	// focused list highlights its whole row.
	gutter := "  "
	if selected && !focused {
		gutter = "▶ "
	}

	dot := "  "
	if ml.Unread {
		dot = "● "
	}
	textW := max(w-4-fromW-dateW-2, 1)

	from := ""
	if fromW > 0 {
		from = pad(trunc(ml.From, fromW-1), fromW)
	}
	subject := trunc(ml.Subject, textW)

	rest := ""
	if room := textW - lipgloss.Width(subject); room > 4 {
		rest = trunc(" "+ml.Snippet, room)
	}
	text := pad(subject+rest, textW)

	dateCell := fmt.Sprintf(" %*s ", dateW, date)
	if focused {
		return " " + stSel.Render(fit(gutter+dot+from+text+dateCell, w))
	}

	if ml.Unread {
		dot, from, subject = stLive.Render(dot), stBold.Render(from), stBold.Render(subject)
	}

	if selected {
		gutter = stBold.Render(gutter)
	}

	return " " + fit(gutter+dot+from+subject+stDim.Render(pad(rest, textW-lipgloss.Width(subject)))+stDim.Render(dateCell), w)
}

func (m Model) detailBlock(g geometry, r rect) ([]string, []hit) {
	var content []dline

	name := "EVENT"
	if m.focus == paneInbox {
		name = "MAIL"

		if len(m.mails) > 0 {
			content = m.mailDetail(m.mails[m.sel[paneInbox]], r.w-2)
		}
	} else if len(m.events) > 0 {
		content = m.eventDetail(m.events[m.sel[paneAgenda]], r.w-2)
	}

	info := "esc back"
	if g.wide {
		info = "preview"
	}
	out := []string{paneTitle(name, info, true, r.w)}
	var hits []hit
	rows := r.h - 1

	off := clamp(m.dOff, 0, max(len(content)-rows, 0))
	for i := 0; i < rows; i++ {
		ci := off + i
		if ci >= len(content) {
			out = append(out, fit("", r.w))
			continue
		}

		line := content[ci]
		if i == rows-1 && ci < len(content)-1 {
			out = append(out, fit(stDim.Render(" ↓ more (ctrl+d)"), r.w))
			continue
		}

		out = append(out, fit(" "+line.text, r.w))
		if line.url != "" {
			hits = append(hits, hit{y: r.y + 1 + i, x0: r.x, x1: r.x + r.w, kind: hitOpen, url: line.url})
		}
	}

	return out, hits
}

// dline is a detail line; a non-empty url makes the whole line clickable.
type dline struct {
	text string
	url  string
}

func wrapLines(s string, w int, st lipgloss.Style) []dline {
	var out []dline

	for _, para := range strings.Split(s, "\n") {
		wrapped := lipgloss.NewStyle().Width(max(w, 1)).Render(para)
		for _, l := range strings.Split(wrapped, "\n") {
			out = append(out, dline{text: st.Render(strings.TrimRight(l, " "))})
		}
	}

	return out
}

func field(label, value string) string {
	return stLabel.Render(fmt.Sprintf("%-7s", label)) + value
}

func (m Model) eventDetail(e item.Event, w int) []dline {
	out := wrapLines(e.Title, w, stBold)
	out = append(out, dline{text: m.when(e)}, dline{})

	join, place := e.JoinURL(), e.Place()
	if join != "" {
		out = append(out,
			dline{text: field("JOIN", link(trunc(shortURL(join), w-7), join)), url: join},
			dline{text: field("", stDim.Render("o or click to join · y copy")), url: join},
		)
	}

	if place != "" {
		dir := e.DirectionsURL()
		for i, l := range wrapLines(place, w-7, stLinkBase.Hyperlink(dir)) {
			label := ""
			if i == 0 {
				label = "WHERE"
			}

			out = append(out, dline{text: field(label, l.text), url: dir})
		}
		out = append(out, dline{text: field("", stDim.Render("m or click for directions")), url: dir})
	}

	if join == "" && place == "" {
		out = append(out, dline{text: stDim.Render("no meeting link or location")})
	}

	out = append(out, dline{})

	if rsvp := rsvpText(e.Response); rsvp != "" {
		out = append(out, dline{text: field("RSVP", rsvp)})
	}

	if e.Organizer != "" {
		out = append(out, dline{text: field("BY", e.Organizer)})
	}

	if len(e.Attendees) > 0 {
		out = append(out, dline{text: field("GUESTS", guestSummary(e.Attendees))})

		names := make([]string, 0, len(e.Attendees))
		for _, a := range e.Attendees {
			names = append(names, a.Name+" "+rsvpMark(a.Response))
		}

		for _, l := range wrapLines(strings.Join(names, ", "), w-7, stDim) {
			out = append(out, dline{text: field("", l.text)})
		}
	}

	if e.Description != "" {
		out = append(out, dline{})
		out = append(out, wrapLines(e.Description, w, lipgloss.NewStyle())...)
	}

	return out
}

func (m Model) when(e item.Event) string {
	if e.AllDay {
		return stDim.Render("all day")
	}

	span := e.Start.Format("15:04") + "–" + e.End.Format("15:04")
	switch {
	case e.InProgress(m.now):
		return stLive.Render(span + " · now, " + human(e.End.Sub(m.now)) + " left")
	case e.Past(m.now):
		return stDim.Render(span + " · ended " + human(m.now.Sub(e.End)) + " ago")
	}

	return span + " · " + stWarn.Render("in "+human(e.Start.Sub(m.now)))
}

func human(d time.Duration) string {
	mins := int(d.Round(time.Minute).Minutes())
	if mins < 60 {
		return fmt.Sprintf("%d min", mins)
	}

	if mins%60 == 0 {
		return fmt.Sprintf("%dh", mins/60)
	}

	return fmt.Sprintf("%dh %dm", mins/60, mins%60)
}

func rsvpText(r string) string {
	switch r {
	case "accepted":
		return stOK.Render("going")
	case "tentative":
		return stWarn.Render("maybe")
	case "declined":
		return stDim.Render("declined")
	case "needsAction":
		return stWarn.Render("not answered")
	}

	return ""
}

func rsvpMark(r string) string {
	switch r {
	case "accepted":
		return "✓"
	case "declined":
		return "✗"
	case "tentative":
		return "~"
	}

	return "?"
}

func guestSummary(as []item.Attendee) string {
	var yes, maybe, no, wait int

	for _, a := range as {
		switch a.Response {
		case "accepted":
			yes++
		case "tentative":
			maybe++
		case "declined":
			no++
		default:
			wait++
		}
	}

	parts := []string{fmt.Sprintf("%d yes", yes)}
	if maybe > 0 {
		parts = append(parts, fmt.Sprintf("%d maybe", maybe))
	}

	if no > 0 {
		parts = append(parts, fmt.Sprintf("%d no", no))
	}

	if wait > 0 {
		parts = append(parts, fmt.Sprintf("%d waiting", wait))
	}

	return strings.Join(parts, " · ")
}

func (m Model) mailDetail(ml item.Mail, w int) []dline {
	out := wrapLines(ml.Subject, w, stBold)
	out = append(out,
		dline{text: field("FROM", trunc(ml.From+" <"+ml.FromAddr+">", w-7))},
		dline{text: field("DATE", m.mailDate(ml.Date)+stDim.Render(" · "+human(m.now.Sub(ml.Date))+" ago"))},
	)

	flags := strings.Join(ml.Labels, " ")
	if ml.Unread {
		flags = stLive.Render("● unread") + " " + stDim.Render(flags)
	} else {
		flags = stDim.Render(flags)
	}
	out = append(out,
		dline{text: field("", flags)},
		dline{},
		dline{text: field("OPEN", link("open in Gmail", ml.GmailURL())), url: ml.GmailURL()},
		dline{text: field("", stDim.Render("e archive · u mark read/unread"))},
		dline{},
	)

	body := ml.Body
	if body == "" {
		body = ml.Snippet
		if m.src != nil {
			body += "\n\n" + stDim.Render("loading the full message…")
		}
	}

	return append(out, wrapLines(body, w, lipgloss.NewStyle())...)
}

// fit truncates or pads a styled string to exactly w cells.
func fit(s string, w int) string {
	if lipgloss.Width(s) > w {
		s = ansi.Truncate(s, w, "")
	}

	return pad(s, w)
}

func pad(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}

	return s
}

func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}

	return ansi.Truncate(s, w, "…")
}

// agendaInfo is the agenda title's detail: meetings left today, or how far
// another day is from today and how many events it has.
func (m Model) agendaInfo() string {
	date := m.day.Format("Mon 2 Jan")
	if m.today() {
		return fmt.Sprintf("%s · %d left", date, m.eventsLeft())
	}

	days := int(math.Round(m.day.Sub(startOfDay(m.now)).Hours() / 24))

	var rel string

	switch {
	case days == 1:
		rel = "tomorrow"
	case days == -1:
		rel = "yesterday"
	case days > 1:
		rel = fmt.Sprintf("in %d days", days)
	default:
		rel = fmt.Sprintf("%d days ago", -days)
	}

	count := fmt.Sprintf("%d events", len(m.events))
	if len(m.events) == 1 {
		count = "1 event"
	}

	return fmt.Sprintf("%s · %s · %s · t today", date, rel, count)
}
