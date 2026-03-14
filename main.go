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
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	docStyle        = lipgloss.NewStyle()
	sessionStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))            // Muted grey
	windowStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true) // Bright pink, most noticeable
	commandStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))            // Light grey
	sepStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("33")).Bold(true)  // Dark grey for separators
	cmdStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))            // Orange/Yellow for command
	pathStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))             // Blueish for path
	gitStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("35"))             // Greenish for git
	selectedStyle   = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(lipgloss.Color("170")).PaddingLeft(3)
	unselectedStyle = lipgloss.NewStyle().PaddingLeft(4)
	statusStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).MarginLeft(4)
)

type item struct {
	SessionName string
	WindowIndex string
	WindowName  string
	GitBranch   string
	Path        string
	FullCommand string
}

func (i item) Title() string {
	window := i.WindowName
	if window == "" {
		window = i.WindowIndex
	}
	return fmt.Sprintf("%s :: %s", i.SessionName, window)
}
func (i item) Description() string { return i.FullCommand + " " + i.Path + " " + i.GitBranch }
func (i item) FilterValue() string {
	return i.SessionName + i.WindowIndex + i.WindowName + i.GitBranch + i.Path + i.FullCommand
}

type itemDelegate struct{}

func (d itemDelegate) Height() int                               { return 3 }
func (d itemDelegate) Spacing() int                              { return 1 }
func (d itemDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }
func (d itemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	i, ok := listItem.(item)
	if !ok {
		return
	}

	displayWindow := i.WindowName
	if displayWindow == "" {
		displayWindow = i.WindowIndex
	}

	sName := sessionStyle.Render(i.SessionName)
	wInfo := windowStyle.Render(displayWindow)
	sep1 := sepStyle.Render(" :: ")

	header := sName + sep1 + wInfo

	cmd := ""
	if i.FullCommand != "" {
		cmd = cmdStyle.Render("> "+middleTruncate(i.FullCommand, 80)) + "\n"
	}

	path := tildify(i.Path)
	path = middleTruncate(path, 40)
	secondLine := pathStyle.Render(path)

	if i.GitBranch != "" {
		branch := middleTruncate(i.GitBranch, 20)
		secondLine += " " + gitStyle.Render(" "+branch)
	}

	fullItem := header + "\n" + cmd + secondLine

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
	list          list.Model
	selected      *item
	ready         bool
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	oldIndex := m.list.Index()
	var listCmd tea.Cmd
	m.list, listCmd =  m.list.Update(msg)
	if m.list.Index() != oldIndex {
		i, ok := m.list.SelectedItem().(item)
		if ok {
			m.selected = &i
			target := fmt.Sprintf("%s:%s", m.selected.SessionName, m.selected.WindowIndex)
			cmd := exec.Command("tmux", "switch-client", "-t", target)
			if err := cmd.Run(); err != nil {
				fmt.Printf("Error switching to window: %v\n", err)
			}
		}
	}

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
		}
	case tea.WindowSizeMsg:
		listWidth := msg.Width

		availableHeight := msg.Height

		listHeight := max(0, availableHeight-1)

		m.list.SetSize(listWidth, listHeight)

		if !m.ready {
			m.ready = true
		}
	}

	cmds = append(cmds, listCmd)

	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	if !m.ready {
		return "\n  Initializing..."
	}

	current := lipgloss.NewStyle().Foreground(lipgloss.Color("170")).Bold(true).Render(fmt.Sprintf("%d", m.list.Index()+1))
	total := lipgloss.NewStyle().Foreground(lipgloss.Color("170")).Bold(true).Render(fmt.Sprintf("%d", len(m.list.Items())))
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color("170")).Bold(true).Render(" / ")

	status := statusStyle.Render(current + divider + total)

	return docStyle.Render(
		lipgloss.JoinVertical(
			lipgloss.Left,
			m.list.View(),
			status,
		),
	)
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

func getFullCommand(tty string) string {
	if tty == "" || tty == "<nil>" {
		return ""
	}
	// ps -t expects tty without /dev/ prefix usually, but pts/5 works
	cmd := exec.Command("ps", "-t", tty, "-o", "args=")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) == 0 {
		return ""
	}
	// The first line is usually the shell (e.g. -bash).
	// We want the last one, which is likely the foreground process.
	last := strings.TrimSpace(lines[len(lines)-1])
	if len(lines) == 1 && strings.HasPrefix(last, "-") {
		return strings.TrimPrefix(last, "-")
	}
	return last
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
	// Format: session_name|window_index|window_name|pane_current_command|pane_current_path|pane_tty
	args := []string{"list-windows", "-a", "-F", "#{session_name}|#{window_index}|#{window_name}|#{pane_current_command}|#{pane_current_path}|#{pane_tty}"}
	if sessionFlag != "" {
		args = []string{"list-windows", "-t", sessionFlag, "-F", "#{session_name}|#{window_index}|#{window_name}|#{pane_current_command}|#{pane_current_path}|#{pane_tty}"}
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
		if len(parts) == 6 {
			session := parts[0]
			index := parts[1]
			windowName := parts[2]
			command := parts[3]
			path := parts[4]
			tty := parts[5]

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
				FullCommand: getFullCommand(tty),
			})
		}
	}

	if len(filteredItems) == 0 {
		fmt.Println("No tmux windows match the criteria.")
		os.Exit(0)
	}

	m := model{
		list: list.New(filteredItems, itemDelegate{}, 0, 0),
	}
	m.list.SetShowTitle(false)
	m.list.SetShowStatusBar(false)
	m.list.SetShowPagination(false)
	m.list.SetShowHelp(false)
	m.list.KeyMap.Quit.SetKeys("q", "ctrl+c")

	// Style the built-in filter bar
	m.list.Styles.Title = lipgloss.NewStyle().MarginLeft(4).Foreground(lipgloss.Color("170")).Bold(true)
	m.list.FilterInput.PromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("170"))
	m.list.FilterInput.TextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))

	p := tea.NewProgram(m, tea.WithAltScreen())

	_, err := p.Run()
	if err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}

	// if m, ok := finalModel.(model); ok && m.selected != nil {
	// 	target := fmt.Sprintf("%s:%s", m.selected.SessionName, m.selected.WindowIndex)
	// 	cmd := exec.Command("tmux", "switch-client", "-t", target)
	// 	if err := cmd.Run(); err != nil {
	// 		fmt.Printf("Error switching to window: %v\n", err)
	// 	}
	// }
}
