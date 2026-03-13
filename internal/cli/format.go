package cli

import (
	"fmt"
	"strings"

	"github.com/fatih/color"
)

var (
	bold      = color.New(color.Bold).SprintFunc()
	faint     = color.New(color.Faint).SprintFunc()
	cyan      = color.New(color.FgCyan).SprintFunc()
	green     = color.New(color.FgGreen).SprintFunc()
	yellow    = color.New(color.FgYellow).SprintFunc()
	red       = color.New(color.FgRed).SprintFunc()
	magenta   = color.New(color.FgMagenta).SprintFunc()
	blue      = color.New(color.FgBlue).SprintFunc()
	white     = color.New(color.FgWhite).SprintFunc()
	boldCyan  = color.New(color.Bold, color.FgCyan).SprintFunc()
	boldGreen = color.New(color.Bold, color.FgGreen).SprintFunc()
)

var sourceColors = map[string]func(a ...interface{}) string{
	"github":          blue,
	"filesystem":      green,
	"arxiv":           magenta,
	"springer":        magenta,
	"github_trending": yellow,
	"youtube":         red,
	"onedrive":        cyan,
}

var sourceIcons = map[string]string{
	"github":          "●",
	"filesystem":      "◆",
	"arxiv":           "◇",
	"springer":        "◇",
	"github_trending": "▲",
	"youtube":         "▶",
	"onedrive":        "☁",
}

func colorSource(source string) string {
	if fn, ok := sourceColors[source]; ok {
		return fn(source)
	}
	return white(source)
}

func iconSource(source string) string {
	icon := sourceIcons[source]
	if icon == "" {
		icon = "○"
	}
	if fn, ok := sourceColors[source]; ok {
		return fn(icon)
	}
	return icon
}

func scoreBar(score float64) string {
	filled := int(score * 20)
	if filled < 0 {
		filled = 0
	}
	if filled > 20 {
		filled = 20
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", 20-filled)
	return faint(fmt.Sprintf("[%s] %.1f%%", bar, score*100))
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func hyperlink(url, text string) string {
	return fmt.Sprintf("\033]8;;%s\033\\%s\033]8;;\033\\", url, text)
}

func urlOrEmpty(u *string) string {
	if u != nil && *u != "" {
		return *u
	}
	return ""
}

func header(text string) {
	fmt.Printf("\n%s\n%s\n\n", boldCyan(text), faint(strings.Repeat("─", len(text)+4)))
}

func sectionHeader(text string) {
	fmt.Printf("  %s\n", bold(text))
}

func keyValue(key string, value any) {
	fmt.Printf("  %-20s %v\n", faint(key), value)
}
