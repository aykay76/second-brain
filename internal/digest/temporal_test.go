package digest

import (
	"testing"
	"time"
)

var refTime = time.Date(2026, 3, 13, 14, 30, 0, 0, time.UTC) // Friday 13 March 2026

func TestParsePeriod(t *testing.T) {
	tests := []struct {
		input   string
		want    Period
		wantErr bool
	}{
		{"daily", PeriodDaily, false},
		{"day", PeriodDaily, false},
		{"weekly", PeriodWeekly, false},
		{"week", PeriodWeekly, false},
		{"monthly", PeriodMonthly, false},
		{"month", PeriodMonthly, false},
		{"  Weekly  ", PeriodWeekly, false},
		{"quarterly", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParsePeriod(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParsePeriod(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ParsePeriod(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolvePeriod(t *testing.T) {
	tests := []struct {
		period   Period
		wantFrom string
		wantTo   string
	}{
		{PeriodDaily, "2026-03-13", "2026-03-14"},
		{PeriodWeekly, "2026-03-07", "2026-03-14"},
		{PeriodMonthly, "2026-02-13", "2026-03-14"},
	}

	for _, tt := range tests {
		t.Run(string(tt.period), func(t *testing.T) {
			tr := ResolvePeriod(tt.period, refTime)
			gotFrom := tr.From.Format("2006-01-02")
			gotTo := tr.To.Format("2006-01-02")
			if gotFrom != tt.wantFrom {
				t.Errorf("From = %s, want %s", gotFrom, tt.wantFrom)
			}
			if gotTo != tt.wantTo {
				t.Errorf("To = %s, want %s", gotTo, tt.wantTo)
			}
		})
	}
}

func TestParseNaturalDate_ExactDate(t *testing.T) {
	tr, err := ParseNaturalDate("2025-06-15", refTime)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertDate(t, tr.From, "2025-06-15")
	assertDate(t, tr.To, "2025-06-16")
}

func TestParseNaturalDate_YearMonth(t *testing.T) {
	tr, err := ParseNaturalDate("2025-03", refTime)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertDate(t, tr.From, "2025-03-01")
	assertDate(t, tr.To, "2025-04-01")
}

func TestParseNaturalDate_RelativeKeywords(t *testing.T) {
	tests := []struct {
		expr     string
		wantFrom string
		wantTo   string
	}{
		{"today", "2026-03-13", "2026-03-14"},
		{"yesterday", "2026-03-12", "2026-03-13"},
		{"this week", "2026-03-09", "2026-03-16"},
		{"last week", "2026-03-02", "2026-03-09"},
		{"this month", "2026-03-01", "2026-04-01"},
		{"last month", "2026-02-01", "2026-03-01"},
		{"this year", "2026-01-01", "2027-01-01"},
		{"last year", "2025-01-01", "2026-01-01"},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			tr, err := ParseNaturalDate(tt.expr, refTime)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertDate(t, tr.From, tt.wantFrom)
			assertDate(t, tr.To, tt.wantTo)
		})
	}
}

func TestParseNaturalDate_PastDuration(t *testing.T) {
	tests := []struct {
		expr     string
		wantFrom string
		wantTo   string
	}{
		{"past 3 days", "2026-03-10", "2026-03-14"},
		{"last 2 weeks", "2026-02-27", "2026-03-14"},
		{"past 6 months", "2025-09-13", "2026-03-14"},
		{"last 1 year", "2025-03-13", "2026-03-14"},
		{"past 1 day", "2026-03-12", "2026-03-14"},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			tr, err := ParseNaturalDate(tt.expr, refTime)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertDate(t, tr.From, tt.wantFrom)
			assertDate(t, tr.To, tt.wantTo)
		})
	}
}

func TestParseNaturalDate_NamedMonth(t *testing.T) {
	tests := []struct {
		expr     string
		wantFrom string
		wantTo   string
	}{
		{"January", "2026-01-01", "2026-02-01"},
		{"in January", "2026-01-01", "2026-02-01"},
		{"in March 2025", "2025-03-01", "2025-04-01"},
		{"March 2025", "2025-03-01", "2025-04-01"},
		{"September", "2025-09-01", "2025-10-01"},   // future month → previous year
		{"in December", "2025-12-01", "2026-01-01"},  // future month → previous year
		{"February", "2026-02-01", "2026-03-01"},     // past month → same year
		{"feb", "2026-02-01", "2026-03-01"},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			tr, err := ParseNaturalDate(tt.expr, refTime)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertDate(t, tr.From, tt.wantFrom)
			assertDate(t, tr.To, tt.wantTo)
		})
	}
}

func TestParseNaturalDate_Before(t *testing.T) {
	tests := []struct {
		expr     string
		wantTo   string
		zeroFrom bool
	}{
		{"before 2025-06-01", "2025-06-01", true},
		{"before 2025-03", "2025-03-01", true},
		{"before January", "2026-01-01", true},
		{"before March 2025", "2025-03-01", true},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			tr, err := ParseNaturalDate(tt.expr, refTime)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.zeroFrom && !tr.From.IsZero() {
				t.Errorf("From should be zero, got %v", tr.From)
			}
			assertDate(t, tr.To, tt.wantTo)
		})
	}
}

func TestParseNaturalDate_Errors(t *testing.T) {
	badExprs := []string{
		"",
		"something random",
		"next tuesday",
		"42 days ago",
	}

	for _, expr := range badExprs {
		t.Run(expr, func(t *testing.T) {
			_, err := ParseNaturalDate(expr, refTime)
			if err == nil {
				t.Errorf("ParseNaturalDate(%q) should return error", expr)
			}
		})
	}
}

func TestTimeRangeLabel(t *testing.T) {
	tests := []struct {
		from, to string
		want     string
	}{
		{"2026-03-13", "2026-03-14", "13 Mar 2026"},
		{"2026-03-07", "2026-03-14", "7 Mar 2026 – 13 Mar 2026"},
	}

	for _, tt := range tests {
		from, _ := time.Parse("2006-01-02", tt.from)
		to, _ := time.Parse("2006-01-02", tt.to)
		tr := TimeRange{From: from, To: to}
		got := tr.Label()
		if got != tt.want {
			t.Errorf("Label() = %q, want %q", got, tt.want)
		}
	}
}

func TestTimeRangeLabelZeroFrom(t *testing.T) {
	to, _ := time.Parse("2006-01-02", "2026-03-01")
	tr := TimeRange{From: time.Time{}, To: to}
	got := tr.Label()
	if got != "before 1 March 2026" {
		t.Errorf("Label() = %q, want %q", got, "before 1 March 2026")
	}
}

func TestTimeRangeDays(t *testing.T) {
	from, _ := time.Parse("2006-01-02", "2026-03-01")
	to, _ := time.Parse("2006-01-02", "2026-03-08")
	tr := TimeRange{From: from, To: to}
	if d := tr.Days(); d != 7 {
		t.Errorf("Days() = %d, want 7", d)
	}
}

func TestTimeRangeDaysZeroFrom(t *testing.T) {
	to, _ := time.Parse("2006-01-02", "2026-03-08")
	tr := TimeRange{From: time.Time{}, To: to}
	if d := tr.Days(); d != 0 {
		t.Errorf("Days() = %d, want 0", d)
	}
}

func assertDate(t *testing.T, got time.Time, want string) {
	t.Helper()
	gotStr := got.Format("2006-01-02")
	if gotStr != want {
		t.Errorf("date = %s, want %s", gotStr, want)
	}
}
