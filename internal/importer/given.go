package importer

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Precision is how much of a [GivenDate] is known. The zero value is a full date.
type Precision int

const (
	DayPrecision   Precision = iota // 2019-07-04
	MonthPrecision                  // 2019-07
	YearPrecision                   // 2019
)

// GivenDate is a date supplied by the user for a manual import, possibly only a year
// or a year and month.
type GivenDate struct {
	// Time is the first moment of the date (the 1st of the month or January 1st when
	// less precise), at midnight UTC.
	Time      time.Time
	Precision Precision
}

// ParseGivenDate parses YYYY, YYYY-MM or YYYY-MM-DD.
func ParseGivenDate(s string) (GivenDate, error) {
	parts := strings.Split(s, "-")
	widths := []int{4, 2, 2}
	if len(parts) > len(widths) {
		return GivenDate{}, fmt.Errorf("%q is not a date: use YYYY, YYYY-MM or YYYY-MM-DD", s)
	}
	nums := []int{0, 1, 1}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || len(p) != widths[i] || n < 0 {
			return GivenDate{}, fmt.Errorf("%q is not a date: use YYYY, YYYY-MM or YYYY-MM-DD", s)
		}
		nums[i] = n
	}
	t := time.Date(nums[0], time.Month(nums[1]), nums[2], 0, 0, 0, 0, time.UTC)
	// time.Date normalises out-of-range values (2019-02-30 becomes March 2nd).
	if t.Year() != nums[0] || int(t.Month()) != nums[1] || t.Day() != nums[2] {
		return GivenDate{}, fmt.Errorf("%q is not a valid date", s)
	}
	return GivenDate{Time: t, Precision: Precision(len(widths) - len(parts))}, nil
}

// String formats the date at its precision: 2019, 2019-07 or 2019-07-04.
func (g GivenDate) String() string {
	switch g.Precision {
	case YearPrecision:
		return g.Time.Format("2006")
	case MonthPrecision:
		return g.Time.Format("2006-01")
	}
	return g.Time.Format("2006-01-02")
}

// Contains reports whether t falls on the given date, comparing only the fields the
// date has: the same year for a year, the same month for a month.
func (g GivenDate) Contains(t time.Time) bool {
	switch g.Precision {
	case YearPrecision:
		return t.Year() == g.Time.Year()
	case MonthPrecision:
		return t.Year() == g.Time.Year() && t.Month() == g.Time.Month()
	}
	return t.Format("2006-01-02") == g.String()
}
