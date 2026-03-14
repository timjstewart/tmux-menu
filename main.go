package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	docStyle        = lipgloss.NewStyle().Margin(1, 2)
	headerStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true) // Lighter
	contentStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))            // Darker/Muted
	selectedStyle   = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(lipgloss.Color("170")).PaddingLeft(3)
	unselectedStyle = lipgloss.NewStyle().PaddingLeft(4)
	previewStyle    = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), true, false, false, false).BorderForeground(lipgloss.Color("240")).Padding(1, 0)
)

type item struct {
	SessionName string
	WindowIndex string
	WindowName  string
	Content     string
}

func (i item) Title() string       { return i.Content }
func (i item) Description() string { return "" }
func (i item) FilterValue() string { return i.SessionName + i.WindowName + i.Content }

type itemDelegate struct {
	height int
}

func (d itemDelegate) Height() int                               { return d.height }
func (d itemDelegate) Spacing() int                              { return 1 }
func (d itemDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }
func (d itemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	i, ok := listItem.(item)
	if !ok {
		return
	}

	header := headerStyle.Render(fmt.Sprintf("[%s] %s:%s", i.SessionName, i.WindowIndex, i.WindowName))
	content := contentStyle.Render(i.Content)
	fullItem := header + "\n" + content

	if index == m.Index() {
		fmt.Fprint(w, selectedStyle.Render(fullItem))
	} else {
		fmt.Fprint(w, unselectedStyle.Render(fullItem))
	}
}

type stringSlice []string

func (s *stringSlice) String() string { return fmt.Sprintf("%v", *s) }
func (s *stringSlice) Set(value string) error {
	*s = append(*s, value)
	return nil
}

type model struct {
	list     list.Model
	viewport viewport.Model
	selected *item
	ready    bool
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.list.FilterState() == list.Filtering {
			break
		}
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "enter":
			i, ok := m.list.SelectedItem().(item)
			if ok {
				m.selected = &i
				return m, tea.Quit
			}
		case "pgup", "alt+v":
			m.viewport.LineUp(m.viewport.Height)
			return m, nil
		case "pgdown", "ctrl+v":
			m.viewport.LineDown(m.viewport.Height)
			return m, nil
		case "u":
			m.viewport.LineUp(1)
			return m, nil
		case "d":
			m.viewport.LineDown(1)
			return m, nil
		}
	case tea.WindowSizeMsg:
		h, v := docStyle.GetFrameSize()
		listWidth := msg.Width - h
		listHeight := (msg.Height - v) / 2
		previewHeight := (msg.Height - v) - listHeight

		m.list.SetSize(listWidth, listHeight)

		if !m.ready {
			m.viewport = viewport.New(listWidth, previewHeight-2) // -2 for border and padding
			m.ready = true
		} else {
			m.viewport.Width = listWidth
			m.viewport.Height = previewHeight - 2
		}
	}

	oldIndex := m.list.Index()
	var listCmd tea.Cmd
	m.list, listCmd = m.list.Update(msg)
	cmds = append(cmds, listCmd)

	if m.list.Index() != oldIndex || !m.ready {
		if i, ok := m.list.SelectedItem().(item); ok {
			content := getFullPaneContent(fmt.Sprintf("%s:%s", i.SessionName, i.WindowIndex))
			m.viewport.SetContent(content)
			m.viewport.GotoBottom()
		}
	}

	var viewCmd tea.Cmd
	m.viewport, viewCmd = m.viewport.Update(msg)
	cmds = append(cmds, viewCmd)

	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	if !m.ready {
		return "\n  Initializing..."
	}
	return docStyle.Render(
		lipgloss.JoinVertical(
			lipgloss.Left,
			m.list.View(),
			previewStyle.Render(m.viewport.View()),
		),
	)
}

func getFullPaneContent(target string) string {
	// -S - gets the entire history
	cmd := exec.Command("tmux", "capture-pane", "-p", "-t", target, "-S", "-")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "Error capturing pane"
	}
	return out.String()
}

func getPaneContent(target string, n int) string {
	cmd := exec.Command("tmux", "capture-pane", "-p", "-t", target)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "Error capturing pane"
	}

	var lines []string
	scanner := bufio.NewScanner(&out)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lines = append(lines, line)
		}
	}

	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}

	return strings.Join(lines, "\n")
}

func main() {
	var sessionFlag string
	var regexFlags stringSlice
	var linesFlag int

	flag.StringVar(&sessionFlag, "session", "", "tmux session name to filter by")
	flag.Var(&regexFlags, "command-regex", "regular expression to filter commands by (multiple allowed)")
	flag.IntVar(&linesFlag, "lines", 3, "number of non-empty lines to show per window")
	flag.Parse()

	// Fetch tmux windows
	args := []string{"list-windows", "-a", "-F", "#{session_name}|#{window_index}|#{window_name}|#{pane_current_command}"}
	if sessionFlag != "" {
		args = []string{"list-windows", "-t", sessionFlag, "-F", "#{session_name}|#{window_index}|#{window_name}|#{pane_current_command}"}
	}

	cmd := exec.Command("tmux", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		fmt.Printf("Error running tmux: %v\n", err)
		os.Exit(1)
	}

	var regexes []*regexp.Regexp
	for _, r := range regexFlags {
		re, err := regexp.Compile(r)
		if err != nil {
			fmt.Printf("Invalid regex %q: %v\n", r, err)
			os.Exit(1)
		}
		regexes = append(regexes, re)
	}

	var filteredItems []list.Item
	scanner := bufio.NewScanner(&out)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, "|")
		if len(parts) == 4 {
			session := parts[0]
			index := parts[1]
			windowName := parts[2]
			command := parts[3]

			// Command regex filter (OR logic)
			if len(regexes) > 0 {
				matched := false
				for _, re := range regexes {
					if re.MatchString(command) {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}

			content := getPaneContent(fmt.Sprintf("%s:%s", session, index), linesFlag)
			filteredItems = append(filteredItems, item{
				SessionName: session,
				WindowIndex: index,
				WindowName:  windowName,
				Content:     content,
			})
		}
	}

	if len(filteredItems) == 0 {
		fmt.Println("No tmux windows match the criteria.")
		os.Exit(0)
	}

	m := model{
		list: list.New(filteredItems, itemDelegate{height: linesFlag + 1}, 0, 0),
	}
	m.list.SetShowTitle(false)
	m.list.SetShowStatusBar(false)
	m.list.KeyMap.Quit.SetKeys("q", "ctrl+c")

	p := tea.NewProgram(m, tea.WithAltScreen())

	finalModel, err := p.Run()
	if err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}

	if m, ok := finalModel.(model); ok && m.selected != nil {
		target := fmt.Sprintf("%s:%s", m.selected.SessionName, m.selected.WindowIndex)
		cmd := exec.Command("tmux", "switch-client", "-t", target)
		if err := cmd.Run(); err != nil {
			fmt.Printf("Error switching to window: %v\n", err)
		}
	}
}
