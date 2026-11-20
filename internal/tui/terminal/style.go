package terminal

import (
	"fmt"
	"strings"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
)

const sgrReset = "\x1b[0m"

// StyleToSGR converts an app.TerminalStyle into an ANSI SGR escape sequence.
// If the style is zero/default, it returns an empty string or reset sequence.
func StyleToSGR(s app.TerminalStyle) string {
	var codes []string

	if s.Attributes&app.TerminalAttrBold != 0 {
		codes = append(codes, "1")
	}
	if s.Attributes&app.TerminalAttrFaint != 0 {
		codes = append(codes, "2")
	}
	if s.Attributes&app.TerminalAttrItalic != 0 {
		codes = append(codes, "3")
	}
	if s.Attributes&app.TerminalAttrBlink != 0 || s.Attributes&app.TerminalAttrRapidBlink != 0 {
		codes = append(codes, "5")
	}
	if s.Attributes&app.TerminalAttrReverse != 0 {
		codes = append(codes, "7")
	}
	if s.Attributes&app.TerminalAttrStrikethrough != 0 {
		codes = append(codes, "9")
	}
	if s.Underline != app.TerminalUnderlineNone {
		codes = append(codes, "4")
	}

	if s.Foreground.Set {
		codes = append(codes, fmt.Sprintf("38;2;%d;%d;%d", s.Foreground.Red, s.Foreground.Green, s.Foreground.Blue))
	}
	if s.Background.Set {
		codes = append(codes, fmt.Sprintf("48;2;%d;%d;%d", s.Background.Red, s.Background.Green, s.Background.Blue))
	}

	if len(codes) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}

// ResetSGR returns the ANSI reset sequence.
func ResetSGR() string {
	return sgrReset
}
