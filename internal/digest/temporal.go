package digest

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// TimeRange represents a resolved time window for queries.
type TimeRange struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Period represents a named digest cadence.
type Period string

const (
	PeriodDaily   Period = "daily"
	PeriodWeekly  Period = "weekly"
	PeriodMonthly Period = "monthly"
)

// ParsePeriod converts a string to a known Period, or returns an error.
func ParsePeriod(s string) (Period, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "daily", "day":
		return PeriodDaily, nil
	case "weekly", "week":
		return PeriodWeekly, nil
	case "monthly", "month":
		return PeriodMonthly, nil
	default:
		return "", fmt.Errorf("unknown period %q (use daily, weekly, or monthly)", s)
	}
}

// ResolvePeriod converts a Period into a concrete TimeRange ending at now.
func ResolvePeriod(p Period, now time.Time) TimeRange {
	now = now.UTC()
	to := startOfDay(now.AddDate(0, 0, 1))
	switch p {
	case PeriodDaily:
		return TimeRange{From: startOfDay(now), To: to}
	case PeriodWeekly:
		return TimeRange{From: startOfDay(now.AddDate(0, 0, -6)), To: to}
	case PeriodMonthly:
		return TimeRange{From: startOfDay(now.AddDate(0, -1, 0)), To: to}
	default:
		return TimeRange{From: startOfDay(now.AddDate(0, 0, -6)), To: to}
	}
}

// ParseNaturalDate attempts to resolve a human-friendly date expression
// into a TimeRange relative to the given reference time. Supported forms:
//
//   - "today", "yesterday"
//   - "last week", "last month", "last year"
//   - "this week", "this month", "this year"
//   - "past N days/weeks/months" or "last N days/weeks/months"
//   - "in January", "in March 2025"
//   - "March 2025", "January"
//   - "YYYY-MM-DD" (exact date, returns that day)
//   - "YYYY-MM" (entire month)
//   - "before <expr>" (open start up to parsed date)
func ParseNaturalDate(expr string, now time.Time) (TimeRange, error) {
	now = now.UTC()
	s := strings.ToLower(strings.TrimSpace(expr))

	if s == "" {
		return TimeRange{}, fmt.Errorf("empty date expression")
	}

	if tr, ok := parseExactDate(s); ok {
		return tr, nil
	}
	if tr, ok := parseYearMonth(s); ok {
		return tr, nil
	}
	if tr, ok := parseRelativeKeyword(s, now); ok {
		return tr, nil
	}
	if tr, ok := parsePastDuration(s, now); ok {
		return tr, nil
	}
	if tr, ok := parseNamedMonth(s, now); ok {
		return tr, nil
	}
	if tr, ok := parseBefore(s, now); ok {
		return tr, nil
	}

	return TimeRange{}, fmt.Errorf("could not parse date expression: %q", expr)
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// parseExactDate handles YYYY-MM-DD.
func parseExactDate(s string) (TimeRange, bool) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return TimeRange{}, false
	}
	return TimeRange{
		From: t,
		To:   t.AddDate(0, 0, 1),
	}, true
}

// parseYearMonth handles YYYY-MM.
func parseYearMonth(s string) (TimeRange, bool) {
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return TimeRange{}, false
	}
	return TimeRange{
		From: t,
		To:   t.AddDate(0, 1, 0),
	}, true
}

var monthNames = map[string]time.Month{
	"january": time.January, "february": time.February, "march": time.March,
	"april": time.April, "may": time.May, "june": time.June,
	"july": time.July, "august": time.August, "september": time.September,
	"october": time.October, "november": time.November, "december": time.December,
	"jan": time.January, "feb": time.February, "mar": time.March,
	"apr": time.April, "jun": time.June, "jul": time.July,
	"aug": time.August, "sep": time.September, "oct": time.October,
	"nov": time.November, "dec": time.December,
}

// parseNamedMonth handles "in January", "in March 2025", "March 2025", "January".
var namedMonthRe = regexp.MustCompile(`^(?:in\s+)?(\w+)(?:\s+(\d{4}))?$`)

func parseNamedMonth(s string, now time.Time) (TimeRange, bool) {
	m := namedMonthRe.FindStringSubmatch(s)
	if m == nil {
		return TimeRange{}, false
	}

	month, ok := monthNames[strings.ToLower(m[1])]
	if !ok {
		return TimeRange{}, false
	}

	year := now.Year()
	if m[2] != "" {
		y, err := strconv.Atoi(m[2])
		if err != nil {
			return TimeRange{}, false
		}
		year = y
	} else if month > now.Month() {
		// "in September" when it's March → previous year
		year--
	}

	from := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	return TimeRange{
		From: from,
		To:   from.AddDate(0, 1, 0),
	}, true
}

var relativeKeywords = map[string]func(time.Time) TimeRange{
	"today": func(now time.Time) TimeRange {
		d := startOfDay(now)
		return TimeRange{From: d, To: d.AddDate(0, 0, 1)}
	},
	"yesterday": func(now time.Time) TimeRange {
		d := startOfDay(now.AddDate(0, 0, -1))
		return TimeRange{From: d, To: d.AddDate(0, 0, 1)}
	},
	"this week": func(now time.Time) TimeRange {
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		from := startOfDay(now.AddDate(0, 0, -(weekday - 1)))
		return TimeRange{From: from, To: from.AddDate(0, 0, 7)}
	},
	"last week": func(now time.Time) TimeRange {
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		thisMonday := startOfDay(now.AddDate(0, 0, -(weekday - 1)))
		from := thisMonday.AddDate(0, 0, -7)
		return TimeRange{From: from, To: thisMonday}
	},
	"this month": func(now time.Time) TimeRange {
		from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		return TimeRange{From: from, To: from.AddDate(0, 1, 0)}
	},
	"last month": func(now time.Time) TimeRange {
		thisFirst := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		from := thisFirst.AddDate(0, -1, 0)
		return TimeRange{From: from, To: thisFirst}
	},
	"this year": func(now time.Time) TimeRange {
		from := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
		return TimeRange{From: from, To: from.AddDate(1, 0, 0)}
	},
	"last year": func(now time.Time) TimeRange {
		thisJan := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
		from := thisJan.AddDate(-1, 0, 0)
		return TimeRange{From: from, To: thisJan}
	},
}

func parseRelativeKeyword(s string, now time.Time) (TimeRange, bool) {
	if fn, ok := relativeKeywords[s]; ok {
		return fn(now), true
	}
	return TimeRange{}, false
}

// parsePastDuration handles "past 3 days", "last 2 weeks", "past 6 months".
var pastDurationRe = regexp.MustCompile(`^(?:past|last)\s+(\d+)\s+(days?|weeks?|months?|years?)$`)

func parsePastDuration(s string, now time.Time) (TimeRange, bool) {
	m := pastDurationRe.FindStringSubmatch(s)
	if m == nil {
		return TimeRange{}, false
	}

	n, _ := strconv.Atoi(m[1])
	to := startOfDay(now.AddDate(0, 0, 1))

	var from time.Time
	switch {
	case strings.HasPrefix(m[2], "day"):
		from = startOfDay(now.AddDate(0, 0, -n))
	case strings.HasPrefix(m[2], "week"):
		from = startOfDay(now.AddDate(0, 0, -n*7))
	case strings.HasPrefix(m[2], "month"):
		from = startOfDay(now.AddDate(0, -n, 0))
	case strings.HasPrefix(m[2], "year"):
		from = startOfDay(now.AddDate(-n, 0, 0))
	default:
		return TimeRange{}, false
	}

	return TimeRange{From: from, To: to}, true
}

// parseBefore handles "before January", "before March 2025", "before 2025-06-01".
func parseBefore(s string, now time.Time) (TimeRange, bool) {
	if !strings.HasPrefix(s, "before ") {
		return TimeRange{}, false
	}

	rest := strings.TrimPrefix(s, "before ")

	if tr, ok := parseExactDate(rest); ok {
		return TimeRange{From: time.Time{}, To: tr.From}, true
	}
	if tr, ok := parseYearMonth(rest); ok {
		return TimeRange{From: time.Time{}, To: tr.From}, true
	}
	if tr, ok := parseNamedMonth(rest, now); ok {
		return TimeRange{From: time.Time{}, To: tr.From}, true
	}

	return TimeRange{}, false
}

// Label returns a human-friendly description of the time range.
func (tr TimeRange) Label() string {
	if tr.From.IsZero() {
		return fmt.Sprintf("before %s", tr.To.Format("2 January 2006"))
	}
	fromDay := tr.From.Format("2 Jan 2006")
	toDay := tr.To.AddDate(0, 0, -1).Format("2 Jan 2006")
	if fromDay == toDay {
		return fromDay
	}
	return fmt.Sprintf("%s – %s", fromDay, toDay)
}

// Days returns the number of days the range spans.
func (tr TimeRange) Days() int {
	if tr.From.IsZero() {
		return 0
	}
	return int(tr.To.Sub(tr.From).Hours()/24 + 0.5)
}
