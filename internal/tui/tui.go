// Package tui is the interactive navigator. It only chooses; the caller
// performs the chosen action after the terminal is restored.
package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mashkovd/aisle/internal/service"
	"github.com/mashkovd/aisle/internal/session"
)

type ActionKind int

const (
	None ActionKind = iota
	Resume
	Attach
	New
)

type Action struct {
	Kind    ActionKind
	Session session.Session
	Runtime string // tmux session name for Attach
	Engine  string // for New
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
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#888888", Dark: "#777777"})
	warnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5C07B"))
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
		parts = append(parts, liveStyle.Render("● "+i.s.Runtime.Name))
	}
	if i.s.Partial() {
		parts = append(parts, warnStyle.Render("partial"))
	}
	return "     " + strings.Join(parts, dimStyle.Render(" · "))
}

func (i sessionItem) FilterValue() string {
	return strings.Join([]string{i.code, i.s.Engine, i.s.Project, i.s.Summary, i.s.NativeID}, " ")
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
	}
	return "     " + strings.Join([]string{tag("tmux"), project(i.rt.Path), i.rt.Command, what}, dimStyle.Render(" · "))
}

func (i runtimeItem) FilterValue() string {
	return strings.Join([]string{i.code, "tmux", i.rt.Name, i.rt.Path, i.rt.Command}, " ")
}

type engineItem struct{ e Engine }

func (i engineItem) Title() string { return tag(i.e.Name) }
func (i engineItem) Description() string {
	return "new " + i.e.Name + " session in the current directory"
}
func (i engineItem) FilterValue() string { return i.e.Name }

func project(p string) string {
	if p == "" {
		return "general"
	}
	return filepath.Base(strings.TrimRight(p, "/"))
}

type keymap struct {
	resume, newSess, byProject, back key.Binding
}

var keys = keymap{
	resume:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "resume")),
	newSess:   key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new session")),
	byProject: key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "group by project")),
	back:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
}

type model struct {
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
func Run(snap service.Snapshot, engines []Engine) (Action, error) {
	m := model{snap: snap, engines: engines, letters: map[string]string{}, now: time.Now()}
	for _, e := range engines {
		m.letters[e.Name] = e.Letter
	}

	d := list.NewDefaultDelegate()
	m.list = list.New(nil, d, 0, 0)
	m.list.Title = "aisle"
	m.list.Styles.Title = titleStyle
	m.list.SetStatusBarItemName("session", "sessions")
	m.list.Filter = WordFilter
	m.list.AdditionalShortHelpKeys = func() []key.Binding { return []key.Binding{keys.resume, keys.newSess, keys.byProject} }
	m.list.AdditionalFullHelpKeys = m.list.AdditionalShortHelpKeys
	m.setItems()
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
	m.picker.AdditionalShortHelpKeys = func() []key.Binding { return []key.Binding{keys.back} }

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

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h, v := appStyle.GetFrameSize()
		m.list.SetSize(msg.Width-h, msg.Height-v)
		m.picker.SetSize(msg.Width-h, msg.Height-v)
		return m, nil
	case tea.KeyMsg:
		if m.picking {
			switch {
			case key.Matches(msg, keys.back):
				m.picking = false
				return m, nil
			case key.Matches(msg, keys.resume):
				if it, ok := m.picker.SelectedItem().(engineItem); ok {
					m.action = Action{Kind: New, Engine: it.e.Name}
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
		case key.Matches(msg, keys.resume):
			switch it := m.list.SelectedItem().(type) {
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
		case key.Matches(msg, keys.byProject):
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
	return appStyle.Render(m.list.View())
}
