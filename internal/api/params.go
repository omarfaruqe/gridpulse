package api

import (
	"fmt"
	"net/url"
	"time"
)

// maxRangeDays caps an hourly query so one request cannot ask for years of rows.
const maxRangeDays = 92

var (
	provinces   = []string{"AB", "ON", "QC"}
	groups      = []string{"Combustible", "Hydro", "Nuclear", "Solar", "Total", "Wind"}
	reconStatus = []string{"ok", "out_of_band", "incomplete", "no_baseline", "not_comparable", "no_tolerance"}
)

// badRequest is a parameter problem the client can fix; its text is shown to them.
type badRequest string

func (e badRequest) Error() string { return string(e) }

func oneOf(name, value string, allowed []string) error {
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return badRequest(fmt.Sprintf("%s %q is not valid (use one of %v)", name, value, allowed))
}

func parseProvince(q url.Values) (string, error) {
	p := q.Get("province")
	if p == "" {
		return "", badRequest(fmt.Sprintf("province is required (use one of %v)", provinces))
	}
	return p, oneOf("province", p, provinces)
}

// span is a UTC time range: from is inclusive, to is exclusive.
type span struct {
	from, to         time.Time
	fromDay, lastDay string // the dates as the client gave them, for the response
}

// parseRange reads from and to as UTC calendar dates, both inclusive. With no
// parameters it returns the last 7 days up to and including today (UTC).
func parseRange(q url.Values, now time.Time) (span, error) {
	n := now.UTC()
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
	toDay, fromDay := today, today.AddDate(0, 0, -6)
	var err error

	toGiven := q.Get("to") != ""
	if toGiven {
		if toDay, err = time.Parse("2006-01-02", q.Get("to")); err != nil {
			return span{}, badRequest(fmt.Sprintf("to %q is not a date (use YYYY-MM-DD)", q.Get("to")))
		}
		fromDay = toDay.AddDate(0, 0, -6)
	}
	if q.Get("from") != "" {
		if fromDay, err = time.Parse("2006-01-02", q.Get("from")); err != nil {
			return span{}, badRequest(fmt.Sprintf("from %q is not a date (use YYYY-MM-DD)", q.Get("from")))
		}
	}
	if fromDay.After(toDay) {
		return span{}, badRequest("from is after to")
	}
	if days := int(toDay.Sub(fromDay).Hours()/24) + 1; days > maxRangeDays {
		return span{}, badRequest(fmt.Sprintf("range is %d days; the maximum is %d", days, maxRangeDays))
	}
	return span{
		from: fromDay, to: toDay.AddDate(0, 0, 1),
		fromDay: fromDay.Format("2006-01-02"), lastDay: toDay.Format("2006-01-02"),
	}, nil
}
