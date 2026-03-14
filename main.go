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
	docStyle        = lipgloss.NewStyle()
	headerStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true) // Lighter
	pathStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))             // Blueish for path
	gitStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("35"))             // Greenish for git
	selectedStyle   = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(lipgloss.Color("170")).PaddingLeft(3)
	unselectedStyle = lipgloss.NewStyle().PaddingLeft(4)
	statusStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).MarginLeft(4)
	previewStyle    = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), true, false, false, false).BorderForeground(lipgloss.Color("240")).Padding(1, 0)
)

type item struct {
	SessionName string
	WindowIndex string
	WindowName  string
	GitBranch   string
	Path        string
}

func (i item) Title() string       { return fmt.Sprintf("[%s] %s:%s", i.SessionName, i.WindowIndex, i.WindowName) }
func (i item) Description() string { return i.Path + " " + i.GitBranch }
func (i item) FilterValue() string { return i.SessionName + i.WindowName + i.GitBranch + i.Path }

type itemDelegate struct{}

func (d itemDelegate) Height() int                               { return 2 }
func (d itemDelegate) Spacing() int                              { return 1 }
func (d itemDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }
func (d itemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	i, ok := listItem.(item)
	if !ok {
		return
	}

	header := headerStyle.Render(fmt.Sprintf("[%s] %s:%s", i.SessionName, i.WindowIndex, i.WindowName))
	
	path := tildify(i.Path)
	path = middleTruncate(path, 40)
	secondLine := pathStyle.Render(path)

	if i.GitBranch != "" {
		branch := middleTruncate(i.GitBranch, 20)
		secondLine += " " + gitStyle.Render(" "+branch)
	}
	
	fullItem := header + "\n" + secondLine

	if index == m.Index() {
		fmt.Fprint(w, selectedStyle.Render(fullItem))
	} else {
		fmt.Fprint(w, unselectedStyle.Render(fullItem))
	}
}

func tildify(path string) string {
	home := os.Getenv("HOME")
	if home != "" && strings.HasPrefix(path, home) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

func middleTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen < 5 { // Not enough room for meaningful truncation
		return s[:maxLen]
	}
	half := (maxLen - 3) / 2
	return s[:half] + "..." + s[len(s)-(maxLen-3-half):]
}

type stringSlice []string

func (s *stringSlice) String() string { return fmt.Sprintf("%v", *s) }
func (s *stringSlice) Set(value string) error {
	*s = append(*s, value)
	return nil
}

type model struct {
	list             list.Model
	viewport         viewport.Model
	selected         *item
	ready            bool
	previewMaxLines  int
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
		listWidth := msg.Width
		
		// previewMaxLines + 3 (1 border + 2 padding)
		previewTotalHeight := m.previewMaxLines + 3
		availableHeight := msg.Height
		
		if previewTotalHeight > availableHeight/2 {
			previewTotalHeight = availableHeight / 2
		}
		
		listHeight := availableHeight - previewTotalHeight - 1 // -1 for status line
		if listHeight < 0 {
			listHeight = 0
		}

		m.list.SetSize(listWidth, listHeight)

		viewportHeight := previewTotalHeight - 3
		if viewportHeight < 0 {
			viewportHeight = 0
		}

		if !m.ready {
			m.viewport = viewport.New(listWidth, viewportHeight)
			m.ready = true
		} else {
			m.viewport.Width = listWidth
			m.viewport.Height = viewportHeight
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

	current := lipgloss.NewStyle().Foreground(lipgloss.Color("170")).Bold(true).Render(fmt.Sprintf("%d", m.list.Index()+1))
	total := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(fmt.Sprintf("%d", len(m.list.Items())))
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Render(" / ")

	status := statusStyle.Render(current + divider + total)

	return docStyle.Render(
		lipgloss.JoinVertical(
			lipgloss.Left,
			m.list.View(),
			status,
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

func getGitBranch(path string) string {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--abbrev-ref", "HEAD")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

func main() {
	var sessionFlag string
	var regexFlags stringSlice
	var previewLines int

	flag.StringVar(&sessionFlag, "session", "", "tmux session name to filter by")
	flag.Var(&regexFlags, "command-regex", "regular expression to filter commands by (multiple allowed)")
	flag.IntVar(&previewLines, "preview-lines", 4, "max number of lines in the preview pane")
	flag.Parse()

	// Fetch tmux windows
	// Format: session_name|window_index|window_name|pane_current_command|pane_current_path
	args := []string{"list-windows", "-a", "-F", "#{session_name}|#{window_index}|#{window_name}|#{pane_current_command}|#{pane_current_path}"}
	if sessionFlag != "" {
		args = []string{"list-windows", "-t", sessionFlag, "-F", "#{session_name}|#{window_index}|#{window_name}|#{pane_current_command}|#{pane_current_path}"}
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
		if len(parts) == 5 {
			session := parts[0]
			index := parts[1]
			windowName := parts[2]
			command := parts[3]
			path := parts[4]

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

			filteredItems = append(filteredItems, item{
				SessionName: session,
				WindowIndex: index,
				WindowName:  windowName,
				GitBranch:   getGitBranch(path),
				Path:        path,
			})
		}
	}

	if len(filteredItems) == 0 {
		fmt.Println("No tmux windows match the criteria.")
		os.Exit(0)
	}

	m := model{
		list:            list.New(filteredItems, itemDelegate{}, 0, 0),
		previewMaxLines: previewLines,
	}
	m.list.SetShowTitle(false)
	m.list.SetShowStatusBar(false)
	m.list.SetShowPagination(false)
	m.list.SetShowHelp(false)
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
