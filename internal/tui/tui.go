// Package tui is the interactive navigator. It only chooses; the caller
// performs the chosen action after the terminal is restored.
package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mashkovd/aisle/internal/search"
	"github.com/mashkovd/aisle/internal/service"
	"github.com/mashkovd/aisle/internal/session"
	"github.com/mashkovd/aisle/internal/status"
)

type ActionKind int

const (
	None ActionKind = iota
	Resume
	Attach
	New
)

type Action struct {
	Kind     ActionKind
	Session  session.Session
	Runtime  string // tmux session name for Attach
	Engine   string // for New
	Worktree bool   // New in a fresh git worktree
}

// SearchFunc runs a full-text query; it may update the index first.
type SearchFunc func(ctx context.Context, query string) ([]service.Result, error)

// Options configure the navigator.
type Options struct {
	Search  SearchFunc              // nil disables full-text search
	Observe func(*service.Snapshot) // refreshes runtime statuses; nil disables them
	Query   string                  // start in results mode for this query…
	Results []service.Result        // …with these results
}

// Engine describes one enabled adapter for display.
type Engine struct {
	Name   string
	Letter string
}

var engineColor = map[string]lipgloss.Color{
	"claude": "#D97757", "codex": "#10A37F", "gemini": "#4285F4", "agy": "#A142F4", "tmux": "#E5C07B",
}

var (
	liveStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#98C379")).Bold(true)
	askStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#E06C75")).Bold(true)
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#888888", Dark: "#777777"})
	warnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5C07B"))
	matchStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5C07B")).Bold(true)
	snipStyle  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#444444", Dark: "#BBBBBB"})
	appStyle   = lipgloss.NewStyle().Padding(1, 2)
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1E1E1E")).Background(lipgloss.Color("#E5C07B")).Padding(0, 1)
)

func tag(engine string) string {
	return lipgloss.NewStyle().Foreground(engineColor[engine]).Bold(true).Render(engine)
}

type sessionItem struct {
	s    session.Session
	code string
	now  time.Time
}

func (i sessionItem) Title() string {
	sum := i.s.Summary
	if sum == "" {
		sum = dimStyle.Render("(untitled)")
	}
	return fmt.Sprintf("%-4s %s", i.code, sum)
}

func (i sessionItem) Description() string {
	parts := []string{tag(i.s.Engine), project(i.s.Project), session.Ago(i.s.UpdatedAt, i.now)}
	if i.s.Runtime != nil {
		parts = append(parts, runtimeBadge(*i.s.Runtime))
	}
	if i.s.Partial() {
		parts = append(parts, warnStyle.Render("partial"))
	}
	return "     " + strings.Join(parts, dimStyle.Render(" · "))
}

func (i sessionItem) FilterValue() string {
	return strings.Join([]string{i.code, i.s.Engine, i.s.Project, i.s.Summary, i.s.NativeID}, " ")
}

// resultItem is a full-text match; enter resumes its session.
type resultItem struct {
	sessionItem
	hit search.Hit
}

func (i resultItem) Description() string {
	meta := strings.Join([]string{tag(i.s.Engine), project(i.s.Project), session.Ago(i.s.UpdatedAt, i.now)}, dimStyle.Render(" · "))
	return "     " + meta + dimStyle.Render(" · ") + highlight(i.hit.Snippet)
}

func (i resultItem) FilterValue() string {
	return i.sessionItem.FilterValue() + " " + i.hit.Snippet
}

// highlight renders the index's match markers.
func highlight(snippet string) string {
	var b strings.Builder
	for {
		i := strings.Index(snippet, search.MarkStart)
		if i < 0 {
			break
		}
		j := strings.Index(snippet[i:], search.MarkEnd)
		if j < 0 {
			break
		}
		b.WriteString(snipStyle.Render(snippet[:i]))
		b.WriteString(matchStyle.Render(snippet[i+len(search.MarkStart) : i+j]))
		snippet = snippet[i+j+len(search.MarkEnd):]
	}
	b.WriteString(snipStyle.Render(snippet))
	return b.String()
}

// runtimeBadge shows a live runtime with what its agent is doing.
func runtimeBadge(rt session.Runtime) string {
	name := rt.Name
	if rt.Inferred {
		name += " (not started by aisle)"
	}
	switch status.State(rt.Status) {
	case status.Asking:
		return askStyle.Render("⚑ needs you") + " " + liveStyle.Render(name)
	case status.Working:
		return liveStyle.Render("◐ working " + name)
	case status.Idle:
		return liveStyle.Render("● " + name)
	case status.Exited:
		return dimStyle.Render("○ exited " + name)
	}
	return liveStyle.Render("● " + name)
}

type runtimeItem struct {
	rt   session.Runtime
	code string
}

func (i runtimeItem) Title() string {
	state := dimStyle.Render("detached")
	if i.rt.Attached {
		state = liveStyle.Render("attached")
	}
	return fmt.Sprintf("%-4s %s  %s", i.code, i.rt.Name, state)
}

func (i runtimeItem) Description() string {
	what := "unmanaged tmux"
	if i.rt.Managed() {
		what = "new " + i.rt.Engine + " session"
		if i.rt.Status != "" {
			what += " · " + runtimeBadge(i.rt)
		}
	}
	return "     " + strings.Join([]string{tag("tmux"), project(i.rt.Path), i.rt.Command, what}, dimStyle.Render(" · "))
}

func (i runtimeItem) FilterValue() string {
	return strings.Join([]string{i.code, "tmux", i.rt.Name, i.rt.Path, i.rt.Command}, " ")
}

type engineItem struct{ e Engine }

func (i engineItem) Title() string { return tag(i.e.Name) }
func (i engineItem) Description() string {
	return "new " + i.e.Name + " session in the current directory (w: in a new git worktree)"
}
func (i engineItem) FilterValue() string { return i.e.Name }

func project(p string) string {
	if p == "" {
		return "general"
	}
	return filepath.Base(strings.TrimRight(p, "/"))
}

type keymap struct {
	resume, newSess, byProject, back, fullText, worktree key.Binding
}

var keys = keymap{
	resume:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "resume")),
	newSess:   key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new session")),
	byProject: key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "group by project")),
	back:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	fullText:  key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "search text")),
	worktree:  key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "in a new worktree")),
}

type mode int

const (
	browsing mode = iota
	typing        // editing a full-text query
	results       // showing full-text results
)

// observeEvery is how often live panes are looked at.
const observeEvery = 1500 * time.Millisecond

type tickMsg struct{}

// statusMsg carries fresh statuses by runtime name.
type statusMsg map[string]string

type searchDoneMsg struct {
	query   string
	results []service.Result
	err     error
}

type model struct {
	ctx       context.Context
	opts      Options
	mode      mode
	input     textinput.Model
	query     string
	searching bool
	snap      service.Snapshot
	letters   map[string]string
	engines   []Engine
	list      list.Model
	picker    list.Model
	picking   bool
	byProject bool
	action    Action
	now       time.Time
}

// Run shows the navigator and returns what the user chose.
func Run(ctx context.Context, snap service.Snapshot, engines []Engine, opts Options) (Action, error) {
	m := model{ctx: ctx, opts: opts, snap: snap, engines: engines, letters: map[string]string{}, now: time.Now()}
	m.input = textinput.New()
	m.input.Prompt = "search text: "
	m.input.Placeholder = "words from any prompt or reply"
	m.input.CharLimit = 200
	for _, e := range engines {
		m.letters[e.Name] = e.Letter
	}

	d := list.NewDefaultDelegate()
	m.list = list.New(nil, d, 0, 0)
	m.list.Title = "aisle"
	m.list.Styles.Title = titleStyle
	m.list.SetStatusBarItemName("session", "sessions")
	m.list.Filter = WordFilter
	m.list.AdditionalShortHelpKeys = func() []key.Binding {
		if opts.Search != nil {
			return []key.Binding{keys.resume, keys.fullText, keys.newSess, keys.byProject}
		}
		return []key.Binding{keys.resume, keys.newSess, keys.byProject}
	}
	m.list.AdditionalFullHelpKeys = m.list.AdditionalShortHelpKeys
	m.setItems()
	if opts.Query != "" {
		m.showResults(opts.Query, opts.Results)
	}
	if n := session.CountActionable(snap.Warnings); n > 0 {
		m.list.NewStatusMessage(warnStyle.Render(fmt.Sprintf("%d warning(s) — run `aisle doctor`", n)))
	}

	var eitems []list.Item
	for _, e := range engines {
		eitems = append(eitems, engineItem{e})
	}
	m.picker = list.New(eitems, list.NewDefaultDelegate(), 0, 0)
	m.picker.Title = "new session"
	m.picker.Styles.Title = titleStyle
	m.picker.SetFilteringEnabled(false)
	m.picker.AdditionalShortHelpKeys = func() []key.Binding { return []key.Binding{keys.worktree, keys.back} }

	out, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		return Action{}, err
	}
	return out.(model).action, nil
}

func (m *model) setItems() {
	ss := append([]session.Session(nil), m.snap.Sessions...)
	if m.byProject {
		sort.SliceStable(ss, func(i, j int) bool { return project(ss[i].Project) < project(ss[j].Project) })
	}
	// conversations waiting on you come first
	sort.SliceStable(ss, func(i, j int) bool { return needsYou(ss[i]) && !needsYou(ss[j]) })
	m.list.Title = "aisle"
	if n := m.waiting(); n > 0 {
		m.list.Title = fmt.Sprintf("aisle · %d need you", n)
	}
	counters := map[string]int{}
	codes := map[string]string{}
	// codes follow recency, independent of the current ordering
	for _, s := range m.snap.Sessions {
		counters[s.Engine]++
		codes[s.Key()] = fmt.Sprintf("%s%d", m.letters[s.Engine], counters[s.Engine])
	}
	var items []list.Item
	for i, rt := range m.snap.Unlinked {
		items = append(items, runtimeItem{rt: rt, code: fmt.Sprintf("t%d", i+1)})
	}
	for _, s := range ss {
		items = append(items, sessionItem{s: s, code: codes[s.Key()], now: m.now})
	}
	m.list.SetItems(items)
}

func needsYou(s session.Session) bool {
	return s.Runtime != nil && status.State(s.Runtime.Status).NeedsYou()
}

func (m *model) waiting() int {
	n := 0
	for _, s := range m.snap.Sessions {
		if needsYou(s) {
			n++
		}
	}
	for _, rt := range m.snap.Unlinked {
		if status.State(rt.Status).NeedsYou() {
			n++
		}
	}
	return n
}

// observe looks at the live panes off the UI goroutine, on a copy of the
// runtimes, and reports the statuses back as a message.
func (m model) observe() tea.Cmd {
	fn := m.opts.Observe
	cp := service.Snapshot{Unlinked: append([]session.Runtime(nil), m.snap.Unlinked...)}
	for _, s := range m.snap.Sessions {
		if s.Runtime != nil {
			rt := *s.Runtime
			s.Runtime = &rt
			cp.Sessions = append(cp.Sessions, s)
		}
	}
	return func() tea.Msg {
		fn(&cp)
		out := statusMsg{}
		for _, s := range cp.Sessions {
			out[s.Runtime.Name] = s.Runtime.Status
		}
		for _, rt := range cp.Unlinked {
			out[rt.Name] = rt.Status
		}
		return out
	}
}

func tick() tea.Cmd { return tea.Tick(observeEvery, func(time.Time) tea.Msg { return tickMsg{} }) }

// applyStatus stores fresh statuses and redraws, keeping the cursor on the
// same entry even when the order changes.
func (m *model) applyStatus(st statusMsg) {
	changed := false
	for i := range m.snap.Sessions {
		if rt := m.snap.Sessions[i].Runtime; rt != nil {
			if v, ok := st[rt.Name]; ok && v != rt.Status {
				rt.Status, changed = v, true
			}
		}
	}
	for i := range m.snap.Unlinked {
		if v, ok := st[m.snap.Unlinked[i].Name]; ok && v != m.snap.Unlinked[i].Status {
			m.snap.Unlinked[i].Status, changed = v, true
		}
	}
	if !changed || m.mode != browsing || m.list.FilterState() != list.Unfiltered {
		return
	}
	sel := itemKey(m.list.SelectedItem())
	m.setItems()
	for i, it := range m.list.Items() {
		if itemKey(it) == sel {
			m.list.Select(i)
			break
		}
	}
}

func itemKey(it list.Item) string {
	switch it := it.(type) {
	case sessionItem:
		return it.s.Key()
	case runtimeItem:
		return "tmux:" + it.rt.Name
	}
	return ""
}

// WordFilter keeps items containing every space-separated word of term
// (case-insensitive) and preserves the recency order, unlike fuzzy ranking.
func WordFilter(term string, targets []string) []list.Rank {
	words := strings.Fields(strings.ToLower(term))
	var ranks []list.Rank
	for i, t := range targets {
		lt := strings.ToLower(t)
		var matched []int
		ok := true
		for _, w := range words {
			j := strings.Index(lt, w)
			if j < 0 {
				ok = false
				break
			}
			// highlight positions are rune indexes into the target
			start := len([]rune(lt[:j]))
			for k := 0; k < len([]rune(w)); k++ {
				matched = append(matched, start+k)
			}
		}
		if ok {
			ranks = append(ranks, list.Rank{Index: i, MatchedIndexes: matched})
		}
	}
	return ranks
}

func (m *model) codes() map[string]string {
	counters := map[string]int{}
	codes := map[string]string{}
	for _, s := range m.snap.Sessions {
		counters[s.Engine]++
		codes[s.Key()] = fmt.Sprintf("%s%d", m.letters[s.Engine], counters[s.Engine])
	}
	return codes
}

func (m *model) showResults(query string, rs []service.Result) {
	m.mode, m.query = results, query
	codes := m.codes()
	items := make([]list.Item, 0, len(rs))
	for _, r := range rs {
		items = append(items, resultItem{sessionItem: sessionItem{s: r.Session, code: codes[r.Session.Key()], now: m.now}, hit: r.Hit})
	}
	m.list.ResetFilter()
	m.list.SetItems(items)
	m.list.Select(0)
	m.list.Title = "aisle · “" + query + "”"
	m.list.SetStatusBarItemName("match", "matches")
}

func (m *model) browse() {
	m.mode, m.query = browsing, ""
	m.list.SetStatusBarItemName("session", "sessions")
	m.setItems()
	m.list.Select(0)
}

func (m model) runSearch(query string) tea.Cmd {
	fn, ctx := m.opts.Search, m.ctx
	return func() tea.Msg {
		rs, err := fn(ctx, query)
		return searchDoneMsg{query: query, results: rs, err: err}
	}
}

func (m model) Init() tea.Cmd {
	if m.opts.Observe == nil || !m.snap.Live() {
		return nil
	}
	return m.observe()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h, v := appStyle.GetFrameSize()
		m.list.SetSize(msg.Width-h, msg.Height-v)
		m.picker.SetSize(msg.Width-h, msg.Height-v)
		return m, nil
	case statusMsg:
		m.applyStatus(msg)
		return m, tick()
	case tickMsg:
		return m, m.observe()
	case searchDoneMsg:
		m.searching = false
		if msg.err != nil {
			return m, m.list.NewStatusMessage(warnStyle.Render("search failed: " + msg.err.Error()))
		}
		m.showResults(msg.query, msg.results)
		return m, nil
	case tea.KeyMsg:
		if m.mode == typing {
			switch msg.Type {
			case tea.KeyEsc:
				if m.query != "" {
					m.mode = results
				} else {
					m.mode = browsing
				}
				return m, nil
			case tea.KeyEnter:
				q := strings.TrimSpace(m.input.Value())
				if q == "" {
					return m, nil
				}
				m.searching = true
				m.mode = browsing
				if m.query != "" {
					m.mode = results
				}
				return m, tea.Batch(m.runSearch(q), m.list.NewStatusMessage(dimStyle.Render("searching… (the first search builds the index)")))
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		if m.picking {
			switch {
			case key.Matches(msg, keys.back):
				m.picking = false
				return m, nil
			case key.Matches(msg, keys.resume), key.Matches(msg, keys.worktree):
				if it, ok := m.picker.SelectedItem().(engineItem); ok {
					m.action = Action{Kind: New, Engine: it.e.Name, Worktree: key.Matches(msg, keys.worktree)}
					return m, tea.Quit
				}
			}
			var cmd tea.Cmd
			m.picker, cmd = m.picker.Update(msg)
			return m, cmd
		}
		if m.list.FilterState() == list.Filtering {
			break
		}
		switch {
		case key.Matches(msg, keys.fullText) && m.opts.Search != nil && !m.searching:
			m.mode = typing
			m.input.SetValue(m.query)
			m.input.CursorEnd()
			return m, m.input.Focus()
		case key.Matches(msg, keys.back) && m.mode == results:
			m.browse()
			return m, nil
		case key.Matches(msg, keys.resume):
			switch it := m.list.SelectedItem().(type) {
			case resultItem:
				m.action = Action{Kind: Resume, Session: it.s}
				return m, tea.Quit
			case sessionItem:
				m.action = Action{Kind: Resume, Session: it.s}
				return m, tea.Quit
			case runtimeItem:
				m.action = Action{Kind: Attach, Runtime: it.rt.Name}
				return m, tea.Quit
			}
		case key.Matches(msg, keys.newSess):
			m.picking = true
			return m, nil
		case key.Matches(msg, keys.byProject) && m.mode == browsing:
			m.byProject = !m.byProject
			m.setItems()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m model) View() string {
	if m.picking {
		return appStyle.Render(m.picker.View())
	}
	if m.mode == typing {
		h := m.list.Height()
		m.list.SetHeight(h - 2)
		v := m.input.View() + "\n\n" + m.list.View()
		m.list.SetHeight(h)
		return appStyle.Render(v)
	}
	return appStyle.Render(m.list.View())
}
