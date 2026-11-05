package render

import (
	"sort"
	"time"
)

// QuotaRow is one upstream account in the quota overview.
type QuotaRow struct {
	Name string
	// Plan is the short label under the name, such as the platform and account type.
	Plan    string
	Enabled bool
	// State is the scheduling badge next to the name.
	State string
	Tags  []string
	// Weekly and FiveHour are nil when the upstream reports no such window.
	Weekly   *QuotaWindow
	FiveHour *QuotaWindow
	Requests *int64
	Cards    *QuotaCards
	// Issue replaces the time axes when quota data is unavailable.
	Issue string
}

// QuotaWindow is one upstream quota period.
type QuotaWindow struct {
	UsedPercent *float64
	ResetsAt    time.Time
	Length      time.Duration
}

// QuotaCards is the account's reset card inventory. A nil value means the
// account type has no reset cards.
type QuotaCards struct {
	Error string
	Cards []QuotaCard
}

// QuotaCard is one available reset card.
type QuotaCard struct {
	Type      string
	ExpiresAt time.Time
}

// quotaWindow is one quota period in display form.
type quotaWindow struct {
	label  string
	used   *float64 // percent used
	length *float64 // seconds
	reset  *float64 // unix seconds
	start  *float64 // unix seconds, reset minus length
	// end is when a reset card or the plan expiry discards the quota before
	// the reset; endsBy names which.
	end     *float64
	endsBy  string
	reserve float64 // fraction of the quota held back
	detail  string
}

// pace returns the even pace and time elapsed as fractions of the window.
func (w *quotaWindow) pace(now float64) (pace, elapsed float64, ok bool) {
	if w.start == nil || w.reset == nil || *w.reset <= *w.start {
		return 0, 0, false
	}
	end := *w.reset
	if w.end != nil {
		end = min(end, *w.end)
	}
	share := 1.0
	if end > *w.start {
		share = min(1, max(0, (now-*w.start)/(end-*w.start)))
	}
	elapsed = min(1, max(0, (now-*w.start)/(*w.reset-*w.start)))
	return (1 - w.reserve) * share, elapsed, true
}

type resetPool struct {
	label   string
	count   int
	expires []float64
}

// channel is an account row in display form.
type channel struct {
	name, status, plan string
	state              string
	organizations      []string
	weekly, fiveHour   *quotaWindow
	resetNote          string
	pools              []resetPool
	issue              string
	requests           *int64
}

func (c channel) versionLabel() string { return c.plan }

func unix(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}
	v := float64(t.UnixNano()) / 1e9
	return &v
}

func adaptWindow(w *QuotaWindow, label string) *quotaWindow {
	if w == nil {
		return nil
	}
	out := &quotaWindow{label: label, used: w.UsedPercent, reset: unix(w.ResetsAt)}
	if w.Length > 0 {
		length := w.Length.Seconds()
		out.length = &length
		if out.reset != nil {
			start := *out.reset - length
			out.start = &start
		}
	}
	return out
}

var resetCardLabels = map[string]string{"WEEK": "周重置卡", "FIVE_HOUR": "5h 重置卡"}

func resetCards(inventory *QuotaCards, now float64) (string, []resetPool) {
	switch {
	case inventory == nil:
		return "不支持重置卡", nil
	case inventory.Error != "":
		return "重置卡查询失败", nil
	}
	pools := map[string]*resetPool{}
	for _, card := range inventory.Cards {
		expires := unix(card.ExpiresAt)
		if expires != nil && *expires <= now {
			continue
		}
		label, ok := resetCardLabels[card.Type]
		if !ok {
			label = clean(card.Type, 32)
		}
		pool := pools[label]
		if pool == nil {
			pool = &resetPool{label: label}
			pools[label] = pool
		}
		pool.count++
		if expires != nil {
			pool.expires = append(pool.expires, *expires)
		}
	}
	ordered := make([]resetPool, 0, len(pools))
	for _, pool := range pools {
		sort.Float64s(pool.expires)
		ordered = append(ordered, *pool)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].label < ordered[j].label })
	if len(ordered) == 0 {
		ordered = append(ordered, resetPool{label: "重置卡"})
	}
	return "", ordered
}

func adapt(row QuotaRow, now float64) channel {
	c := channel{
		name:     clean(row.Name, 128),
		plan:     clean(row.Plan, 48),
		status:   "手动禁用",
		state:    row.State,
		weekly:   adaptWindow(row.Weekly, "周额度"),
		fiveHour: adaptWindow(row.FiveHour, "5 小时"),
		requests: row.Requests,
		issue:    row.Issue,
	}
	if row.Enabled {
		c.status = "启用"
	}
	for _, name := range row.Tags {
		if name != "" {
			c.organizations = append(c.organizations, clean(name, 128))
		}
	}
	c.resetNote, c.pools = resetCards(row.Cards, now)
	return c
}

// Quota draws the account overview. The title, when present, follows the
// timestamp in the heading.
func Quota(rows []QuotaRow, location *time.Location, title string, now time.Time) ([]byte, error) {
	at := float64(now.UnixNano()) / 1e9
	channels := make([]channel, len(rows))
	for i, row := range rows {
		channels[i] = adapt(row, at)
	}
	if title != "" {
		title = clean(title, 24)
	}
	png, err := drawOverview(channels, at, location, title)
	if err != nil {
		return nil, err
	}
	if len(png) > 2<<20 {
		return nil, ErrTooLarge
	}
	return png, nil
}
