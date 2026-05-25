package cli

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/skael-dev/skael/cli/client"
)

type selectorItem struct {
	skill    client.DiscoveredSkill
	selected bool
}

type selectorModel struct {
	items    []selectorItem
	cursor   int
	done     bool
	canceled bool
}

type selectorResult struct {
	selected []client.DiscoveredSkill
	canceled bool
}

func newSelectorModel(skills []client.DiscoveredSkill) selectorModel {
	items := make([]selectorItem, len(skills))
	for i, sk := range skills {
		items[i] = selectorItem{
			skill:    sk,
			selected: sk.ExistingVersion == 0,
		}
	}
	return selectorModel{items: items}
}

func (m selectorModel) Init() tea.Cmd {
	return nil
}

func (m selectorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case " ":
			m.items[m.cursor].selected = !m.items[m.cursor].selected
		case "a":
			allSelected := true
			for _, item := range m.items {
				if !item.selected {
					allSelected = false
					break
				}
			}
			for i := range m.items {
				m.items[i].selected = !allSelected
			}
		case "enter":
			m.done = true
			return m, tea.Quit
		case "q", "esc", "ctrl+c":
			m.canceled = true
			return m, tea.Quit
		}
	}
	return m, nil
}

var (
	selectorCursor    = lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e")).Bold(true)
	selectorDim       = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	selectorName      = lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e")).Bold(true)
	selectorDesc      = lipgloss.NewStyle().Foreground(lipgloss.Color("#a0a0a0"))
	selectorFiles     = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	selectorClean     = lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e"))
	selectorWarn      = lipgloss.NewStyle().Foreground(lipgloss.Color("#f59e0b"))
	selectorCritical  = lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444"))
	selectorExisting  = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	selectorHelp      = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
)

func (m selectorModel) View() string {
	var b strings.Builder

	count := 0
	for _, item := range m.items {
		if item.selected {
			count++
		}
	}

	for i, item := range m.items {
		cursor := "  "
		if i == m.cursor {
			cursor = selectorCursor.Render("> ")
		}

		check := "[ ]"
		if item.selected {
			check = selectorCursor.Render("[x]")
		}

		scanBadge := selectorClean.Render("clean")
		if item.skill.ScanStatus == "warn" {
			scanBadge = selectorWarn.Render("warn")
		} else if item.skill.ScanStatus == "critical" {
			scanBadge = selectorCritical.Render("critical")
		}

		versionBadge := ""
		if item.skill.ExistingVersion > 0 {
			versionBadge = selectorExisting.Render(fmt.Sprintf(" v%d", item.skill.ExistingVersion))
		}

		name := selectorName.Render(fmt.Sprintf("%-20s", item.skill.Name))
		desc := selectorDesc.Render(truncateDesc(item.skill.Description, 30))
		files := selectorFiles.Render(fmt.Sprintf("%d files", len(item.skill.Files)))

		b.WriteString(fmt.Sprintf("  %s%s %s %s  %s  %s%s\n", cursor, check, name, desc, files, scanBadge, versionBadge))
	}

	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  %s selected", selectorDim.Render(fmt.Sprintf("%d", count))))
	b.WriteString("\n")
	b.WriteString(selectorHelp.Render("  ↑↓ move · space toggle · a all · enter confirm · esc cancel"))
	b.WriteString("\n")

	return b.String()
}

func runSelector(skills []client.DiscoveredSkill) selectorResult {
	m := newSelectorModel(skills)
	p := tea.NewProgram(m)
	finalModel, err := p.Run()
	if err != nil {
		return selectorResult{canceled: true}
	}
	final := finalModel.(selectorModel)
	if final.canceled {
		return selectorResult{canceled: true}
	}
	var selected []client.DiscoveredSkill
	for _, item := range final.items {
		if item.selected {
			selected = append(selected, item.skill)
		}
	}
	return selectorResult{selected: selected}
}
