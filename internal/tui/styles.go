package tui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

// Theme encapsulates styled Lip Gloss renderers with NO_COLOR awareness.
type Theme struct {
	NoColor bool

	// Base styles
	AppTitle      lipgloss.Style
	Border        lipgloss.Style
	HeaderRow     lipgloss.Style
	NormalRow     lipgloss.Style
	SelectedRow   lipgloss.Style
	SearchPrompt  lipgloss.Style
	SearchText    lipgloss.Style
	StatusMessage lipgloss.Style
	StatusError   lipgloss.Style
	FooterBar     lipgloss.Style
	KeyShortcut   lipgloss.Style
	KeyDesc       lipgloss.Style

	// Modal & Dialog
	ModalBox     lipgloss.Style
	ModalTitle   lipgloss.Style
	ModalPrompt  lipgloss.Style
	ModalImpact  lipgloss.Style
	ModalConfirm lipgloss.Style
	ModalCancel  lipgloss.Style

	// Form
	FormFieldLabel lipgloss.Style
	FormFieldInput lipgloss.Style
	FormFieldFocus lipgloss.Style
	FormError      lipgloss.Style

	// Detail Inspector
	DetailTitle lipgloss.Style
	DetailLabel lipgloss.Style
	DetailValue lipgloss.Style
}

// NewTheme constructs a Theme, respecting the NO_COLOR environment standard.
func NewTheme() Theme {
	noColor := os.Getenv("NO_COLOR") != ""

	if noColor {
		return Theme{
			NoColor:        true,
			AppTitle:       lipgloss.NewStyle().Bold(true),
			Border:         lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()),
			HeaderRow:      lipgloss.NewStyle().Bold(true),
			NormalRow:      lipgloss.NewStyle(),
			SelectedRow:    lipgloss.NewStyle().Reverse(true),
			SearchPrompt:   lipgloss.NewStyle().Bold(true),
			SearchText:     lipgloss.NewStyle(),
			StatusMessage:  lipgloss.NewStyle(),
			StatusError:    lipgloss.NewStyle().Bold(true),
			FooterBar:      lipgloss.NewStyle(),
			KeyShortcut:    lipgloss.NewStyle().Bold(true),
			KeyDesc:        lipgloss.NewStyle(),
			ModalBox:       lipgloss.NewStyle().BorderStyle(lipgloss.DoubleBorder()).Padding(1, 2),
			ModalTitle:     lipgloss.NewStyle().Bold(true),
			ModalPrompt:    lipgloss.NewStyle(),
			ModalImpact:    lipgloss.NewStyle().Bold(true),
			ModalConfirm:   lipgloss.NewStyle().Bold(true),
			ModalCancel:    lipgloss.NewStyle(),
			FormFieldLabel: lipgloss.NewStyle().Bold(true),
			FormFieldInput: lipgloss.NewStyle(),
			FormFieldFocus: lipgloss.NewStyle().Bold(true),
			FormError:      lipgloss.NewStyle().Bold(true),
			DetailTitle:    lipgloss.NewStyle().Bold(true),
			DetailLabel:    lipgloss.NewStyle().Bold(true),
			DetailValue:    lipgloss.NewStyle(),
		}
	}

	return Theme{
		NoColor: false,
		AppTitle: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7D56F4")),
		Border: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#555555")),
		HeaderRow: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#AAAAAA")).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(lipgloss.Color("#444444")),
		NormalRow: lipgloss.NewStyle(),
		SelectedRow: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#3A3F58")),
		SearchPrompt: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#00D7D7")),
		SearchText: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")),
		StatusMessage: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00D787")),
		StatusError: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FF5F87")),
		FooterBar: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")),
		KeyShortcut: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#5FAFFF")),
		KeyDesc: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#AAAAAA")),
		ModalBox: lipgloss.NewStyle().
			BorderStyle(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("#FF5F87")).
			Padding(1, 2),
		ModalTitle: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FF5F87")),
		ModalPrompt: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")),
		ModalImpact: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFD700")),
		ModalConfirm: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#00FF87")),
		ModalCancel: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")),
		FormFieldLabel: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#00D7D7")),
		FormFieldInput: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")),
		FormFieldFocus: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFF5F")),
		FormError: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FF5F87")),
		DetailTitle: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7D56F4")),
		DetailLabel: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#888888")),
		DetailValue: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E0E0E0")),
	}
}

// StatusIndicator returns the formatted symbol and human label for a lifecycle state.
func StatusIndicator(lifecycle session.Lifecycle, noColor bool) (string, string) {
	switch lifecycle {
	case session.LifecycleRunning:
		if noColor {
			return "[RUN]", "Running"
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF87")).Render("●"), "Running"
	case session.LifecycleStarting:
		if noColor {
			return "[START]", "Starting"
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700")).Render("○"), "Starting"
	case session.LifecycleStopping:
		if noColor {
			return "[STOPPING]", "Stopping"
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#FF8700")).Render("○"), "Stopping"
	case session.LifecycleFailed:
		if noColor {
			return "[FAIL]", "Failed"
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Render("✕"), "Failed"
	case session.LifecycleExited:
		if noColor {
			return "[EXIT]", "Exited"
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render("⬝"), "Exited"
	case session.LifecycleCreated:
		if noColor {
			return "[NEW]", "Created"
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#5FAFFF")).Render("○"), "Created"
	default:
		if noColor {
			return "[?]", string(lifecycle)
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#AF87FF")).Render("?"), string(lifecycle)
	}
}

// TruncatePath contracts an absolute path nicely for compact tables.
// Replaces home directory with ~ and collapses intermediate segments if maxLen is exceeded.
func TruncatePath(path string, maxLen int) string {
	if maxLen <= 4 {
		return "..."
	}
	if len(path) <= maxLen {
		return path
	}

	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(path, home) {
		path = "~" + strings.TrimPrefix(path, home)
	}

	if len(path) <= maxLen {
		return path
	}

	base := filepath.Base(path)
	if len(base)+4 >= maxLen {
		return path[:maxLen-3] + "..."
	}

	keepPrefixLen := maxLen - len(base) - 4
	if keepPrefixLen < 1 {
		return ".../" + base
	}

	return path[:keepPrefixLen] + ".../" + base
}
