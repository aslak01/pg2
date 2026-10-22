package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	searchURL   = "https://apibay.org/q.php?cat=0&q="
	trackersURL = "https://raw.githubusercontent.com/ngosang/trackerslist/master/trackers_best.txt"
)

var httpClient = &http.Client{Timeout: 15 * time.Second}

// torrent is a single search result from apibay.
type torrent struct {
	Name     string `json:"name"`
	InfoHash string `json:"info_hash"`
	Seeders  string `json:"seeders"`
	Leechers string `json:"leechers"`
	Size     string `json:"size"`
	Added    string `json:"added"`
	Username string `json:"username"`
}

func (t torrent) Title() string       { return t.Name }
func (t torrent) FilterValue() string { return t.Name }
func (t torrent) Description() string {
	size, _ := strconv.ParseInt(t.Size, 10, 64)
	added := ""
	if ts, err := strconv.ParseInt(t.Added, 10, 64); err == nil {
		added = time.Unix(ts, 0).Format("2006-01-02")
	}
	return fmt.Sprintf("S:%s L:%s  %s  %s  %s", t.Seeders, t.Leechers, humanSize(size), added, t.Username)
}

func (t torrent) magnet(trackers []string) string {
	var b strings.Builder
	b.WriteString("magnet:?xt=urn:btih:")
	b.WriteString(t.InfoHash)
	b.WriteString("&dn=")
	b.WriteString(url.QueryEscape(t.Name))
	for _, tr := range trackers {
		b.WriteString("&tr=")
		b.WriteString(url.QueryEscape(tr))
	}
	return b.String()
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func search(query string) ([]torrent, error) {
	resp, err := httpClient.Get(searchURL + url.QueryEscape(query))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search: %s", resp.Status)
	}
	var results []torrent
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	// apibay signals "no results" with a single zero-hash entry.
	out := results[:0]
	for _, r := range results {
		if strings.Trim(r.InfoHash, "0") != "" {
			r.Name = html.UnescapeString(r.Name)
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, _ := strconv.Atoi(out[i].Seeders)
		b, _ := strconv.Atoi(out[j].Seeders)
		return a > b
	})
	return out, nil
}

func fetchTrackers() ([]string, error) {
	resp, err := httpClient.Get(trackersURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("trackers: %s", resp.Status)
	}
	var trackers []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			trackers = append(trackers, line)
		}
	}
	return trackers, sc.Err()
}

type resultsMsg struct {
	torrents []torrent
	trackers []string
	err      error
}

// load runs the search and tracker fetch concurrently.
func load(query string) tea.Cmd {
	return func() tea.Msg {
		type tr struct {
			list []string
			err  error
		}
		ch := make(chan tr, 1)
		go func() {
			l, err := fetchTrackers()
			ch <- tr{l, err}
		}()
		torrents, err := search(query)
		t := <-ch
		if err == nil {
			err = t.err
		}
		return resultsMsg{torrents: torrents, trackers: t.list, err: err}
	}
}

type state int

const (
	stateInput state = iota
	stateLoading
	stateList
)

var (
	errStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	helpStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	appStyle  = lipgloss.NewStyle().Margin(1, 2)
)

type model struct {
	state    state
	input    textinput.Model
	spinner  spinner.Model
	list     list.Model
	trackers []string
	query    string
	err      error
	copied   string
	magnet   string
}

func newModel(query string) model {
	ti := textinput.New()
	ti.Placeholder = "search torrents"
	ti.Prompt = "> "
	ti.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.SetShowStatusBar(true)
	l.DisableQuitKeybindings()
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "copy magnet")),
			key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "new search")),
			key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
		}
	}

	m := model{input: ti, spinner: sp, list: l, query: query}
	if query != "" {
		m.state = stateLoading
	}
	return m
}

func (m model) Init() tea.Cmd {
	if m.state == stateLoading {
		return tea.Batch(m.spinner.Tick, load(m.query))
	}
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h, v := appStyle.GetFrameSize()
		m.list.SetSize(msg.Width-h, msg.Height-v)
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

	case resultsMsg:
		if msg.err != nil {
			m.err = msg.err
			m.state = stateInput
			m.input.SetValue(m.query)
			return m, textinput.Blink
		}
		m.trackers = msg.trackers
		items := make([]list.Item, len(msg.torrents))
		for i, t := range msg.torrents {
			items[i] = t
		}
		m.list.Title = fmt.Sprintf("%q", m.query)
		m.list.ResetFilter()
		m.list.Select(0)
		m.state = stateList
		return m, m.list.SetItems(items)
	}

	switch m.state {
	case stateInput:
		if k, ok := msg.(tea.KeyMsg); ok {
			switch k.String() {
			case "esc":
				return m, tea.Quit
			case "enter":
				q := strings.TrimSpace(m.input.Value())
				if q == "" {
					return m, nil
				}
				m.query, m.err, m.state = q, nil, stateLoading
				return m, tea.Batch(m.spinner.Tick, load(q))
			}
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case stateLoading:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case stateList:
		if k, ok := msg.(tea.KeyMsg); ok && m.list.FilterState() != list.Filtering {
			switch k.String() {
			case "q", "esc":
				if m.list.FilterState() == list.FilterApplied && k.String() == "esc" {
					break // let the list clear the filter
				}
				return m, tea.Quit
			case "s":
				m.state = stateInput
				m.input.SetValue("")
				return m, textinput.Blink
			case "enter":
				t, ok := m.list.SelectedItem().(torrent)
				if !ok {
					return m, nil
				}
				m.copied, m.magnet = t.Name, t.magnet(m.trackers)
				return m, tea.Quit
			}
		}
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) View() string {
	if m.copied != "" {
		return ""
	}
	var s string
	switch m.state {
	case stateInput:
		s = "Search\n\n" + m.input.View() + "\n\n" + helpStyle.Render("enter search • esc quit")
	case stateLoading:
		s = fmt.Sprintf("%s searching for %q...", m.spinner.View(), m.query)
	case stateList:
		if len(m.list.Items()) == 0 {
			s = fmt.Sprintf("No results for %q\n\n", m.query) + helpStyle.Render("s new search • q quit")
		} else {
			s = m.list.View()
		}
	}
	if m.err != nil {
		s += "\n\n" + errStyle.Render(m.err.Error())
	}
	return appStyle.Render(s)
}

func main() {
	query := strings.TrimSpace(strings.Join(os.Args[1:], " "))
	p := tea.NewProgram(newModel(query), tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	m := final.(model)
	if m.magnet == "" {
		return
	}
	if err := copyToClipboard(m.magnet); err != nil {
		fmt.Fprintf(os.Stderr, "clipboard: %v\n%s\n", err, m.magnet)
		os.Exit(1)
	}
	fmt.Printf("Copied magnet for %s\n", m.copied)
}
