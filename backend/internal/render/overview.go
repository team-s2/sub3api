package render

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The quota overview has name and request-count columns, followed by week,
// 5h and reset-card time axes sharing one "now".

const (
	day  = 86400.0
	hour = 3600.0

	panel    = Color(0xFFFFFFFF)
	rule     = Color(0xFFEDEFF3)
	grid     = Color(0xFFF1F2F5)
	sub      = Color(0xFF6B7280)
	muted    = Color(0xFFA3A9B5)
	today    = Color(0xFFF6F7FB)
	nowColor = Color(0xFFF08A24)
	// paceColor marks the even pace where a reserve or cut moves it off now;
	// cutColor names a cut and points the pace ahead.
	paceColor = Color(0xFF1D2230)
	cutColor  = Color(0xFF1F8A5B)
	over      = Color(0xFFE5484D)
	warn      = Color(0xFFEC8E2C)
	weekC     = Color(0xFF6B5FD3)
	weekPale  = Color(0xFFE7E4F8)
	five      = Color(0xFF12998F)
	fivePale  = Color(0xFFDDF1EE)
	card      = Color(0xFF5B5BD6)
	cardLane  = Color(0xFFE1E1F7)
	enabledC  = Color(0xFF2F9E6E)
	pageBG    = Color(0xFFF4F5F7)

	width  = 1436.0
	margin = 24.0
	pad    = 20.0
	left   = margin + pad
	right  = width - margin - pad
	nameW  = 256.0
	// Right edges of the value cells, then the time axes they introduce.
	requestValue   = 428.0
	weekValue      = 514.0
	weekX0, weekX1 = 528.0, 846.0
	fiveValue      = 914.0
	fiveX0, fiveX1 = 928.0, 1126.0
	cardLabel      = 1140.0
	cardValue      = 1172.0
	cardX0, cardX1 = 1184.0, right
	titleH         = 60.0
	headH          = 34.0
	rowH           = 52.0
	laneDY         = 9.0 // Zhipu's week and 5h card lanes sit this far above/below centre
	chipGap        = 10.0
	barH           = 12.0
	tagH           = 15.0
	tagPad         = 4.0
	tagGap         = 4.0
	tagLineH       = 20.0
	maxHeight      = 20000.0
)

var tagColors = [][2]Color{
	{0xFFE7E4F8, 0xFF4F43B8},
	{0xFFDDF1EE, 0xFF0B7A72},
	{0xFFFCEBDD, 0xFFB15E12},
	{0xFFE3EEFB, 0xFF2463AE},
	{0xFFF8E3EC, 0xFFB0336A},
	{0xFFEBEDF1, 0xFF4B5262},
}

// overview draws one table for every row at the scale of the original.
type overview struct {
	painter
	loc *time.Location
	now float64
}

func (o overview) at(ts float64) time.Time {
	sec, frac := math.Modf(ts)
	return time.Unix(int64(sec), int64(frac*1e9)).In(o.loc)
}

func (o overview) monthDay(ts float64) string { return o.at(ts).Format("01/02") }

func (o overview) dayStart(ts float64) float64 {
	t := o.at(ts)
	return float64(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, o.loc).Unix())
}

// durationText shows the two most significant units, e.g. 4天21时.
func durationText(seconds float64) string {
	minutes := max(1, int(seconds/60))
	days, rest := minutes/1440, minutes%1440
	hours, mins := rest/60, rest%60
	type part struct {
		value int
		unit  string
	}
	parts := []part{{hours, "时"}, {mins, "分"}}
	if days > 0 {
		parts = []part{{days, "天"}, {hours, "时"}}
	} else if hours == 0 {
		parts = []part{{mins, "分"}}
	}
	if len(parts) == 2 && parts[1].value == 0 {
		parts = parts[:1]
	}
	texts := make([]string, len(parts))
	for i, p := range parts {
		texts[i] = strconv.Itoa(p.value) + unitGap + p.unit
	}
	return strings.Join(texts, unitGap)
}

// axis is a linear time scale from start to end over pixels x0..x1.
type axis struct{ start, end, x0, x1 float64 }

func (a axis) x(ts float64) float64 { return a.x0 + (ts-a.start)/(a.end-a.start)*(a.x1-a.x0) }

func (a axis) clamp(x float64) float64 { return min(a.x1, max(a.x0, x)) }

// weekAxis covers every live weekly window and today in whole local days.
func (o overview) weekAxis(rows []channel) axis {
	stamps := []float64{o.now}
	for _, row := range rows {
		if w := row.weekly; w != nil && w.reset != nil && *w.reset > o.now {
			stamps = append(stamps, *w.reset)
			if w.start != nil {
				stamps = append(stamps, *w.start)
			}
		}
	}
	start := o.dayStart(minOf(stamps))
	end := o.dayStart(maxOf(stamps)) + day
	return axis{start, max(end, start+8*day), weekX0, weekX1}
}

// fiveAxis spans five hours either side of now, so every live 5h window fits.
func (o overview) fiveAxis() axis {
	return axis{o.now - 5*hour, o.now + 5*hour, fiveX0, fiveX1}
}

// cardAxis covers today (or the oldest listed card) to the last card.
func (o overview) cardAxis(rows []channel) axis {
	stamps := []float64{o.now}
	for _, row := range rows {
		for _, pool := range row.pools {
			stamps = append(stamps, pool.expires...)
		}
	}
	start := o.dayStart(minOf(stamps))
	end := max(o.dayStart(maxOf(stamps))+day, start+14*day)
	return axis{start, end, cardX0, cardX1}
}

func minOf(values []float64) float64 {
	m := values[0]
	for _, v := range values[1:] {
		m = min(m, v)
	}
	return m
}

func maxOf(values []float64) float64 {
	m := values[0]
	for _, v := range values[1:] {
		m = max(m, v)
	}
	return m
}

// tagColor is stable across runs: it depends only on the name's bytes.
func tagColor(organization string) [2]Color {
	sum := 0
	for _, b := range []byte(organization) {
		sum += int(b)
	}
	return tagColors[sum%len(tagColors)]
}

type tag struct {
	name  string
	width float64
}

// tagLines wraps organization tags onto further lines rather than hiding any.
func (o overview) tagLines(row channel) [][]tag {
	if len(row.organizations) == 0 {
		return nil
	}
	metadata := o.fit(row.versionLabel(), nameW-16, 11, Regular)
	x := left + 22 + o.measure(metadata, 11, Regular)
	continuation, limit := left+22, left+nameW
	var lines [][]tag
	var current []tag
	for _, name := range row.organizations {
		w := o.measure(name, 10.5, Bold) + 2*tagPad
		if len(current) > 0 && x+w > limit {
			lines = append(lines, current)
			current = nil
			x = continuation
		}
		if len(current) == 0 && x+w > limit {
			x = continuation
		}
		current = append(current, tag{name, w})
		x += w + tagGap
	}
	if len(current) > 0 {
		lines = append(lines, current)
	}
	return lines
}

func (o overview) rowHeight(row channel) float64 {
	lines := len(o.tagLines(row))
	height := max(rowH, 52+float64(max(0, lines-1))*tagLineH)
	return height
}

// subscriptionBadge translates the same admission states as the WebUI.
func subscriptionBadge(state string) (string, Color) {
	switch state {
	case "ready":
		return "", enabledC
	case "disabled":
		return "已禁用", muted
	case "invalid":
		return "凭据失效", over
	case "quota_exhausted":
		return "额度耗尽", warn
	case "peak_paused":
		return "高峰暂停", warn
	case "network_error":
		return "网络错误", over
	default:
		return "状态未知", muted
	}
}

func (o overview) drawName(top float64, row channel) {
	cy := top + 17
	enabled := row.status == "启用"
	dot, nameInk := muted, sub
	if enabled {
		dot, nameInk = enabledC, ink
	}
	o.circle(left+4, cy, 4, dot)
	badge, fill := subscriptionBadge(row.state)
	badgeW := 0.0
	if badge != "" {
		badgeW = o.measure(badge, 10, Bold) + 10
	}
	name := o.fit(row.name, nameW-22-badgeW, 15, Bold)
	nameWidth := o.text(left+16, cy, name, 15, nameInk, Bold, alignLeft)
	if badgeW > 0 {
		x := left + 22 + nameWidth
		o.rect(x, cy-8, x+badgeW, cy+8, fill, 8)
		o.text(x+badgeW/2, cy, badge, 10, panel, Bold, alignCenter)
	}
	meta := o.fit(row.versionLabel(), nameW-16, 11, Regular)
	metaY := cy + 19
	o.text(left+16, metaY, meta, 11, sub, Regular, alignLeft)
	continuation := left + 22
	for i, line := range o.tagLines(row) {
		x := continuation
		if i == 0 {
			if x = left + 22 + o.measure(meta, 11, Regular); x >= left+nameW {
				x = continuation
			}
		}
		dy := float64(i) * tagLineH
		for _, t := range line {
			colors := tagColor(t.name)
			o.rect(x, metaY-tagH/2+dy, x+t.width, metaY+tagH/2+dy, colors[0], 4)
			o.text(x+t.width/2, metaY+dy, t.name, 10.5, colors[1], Bold, alignCenter)
			x += t.width + tagGap
		}
	}
}

// windowBar lays a window's quota over its time span, as the WebUI's quota
// bar does: the fill is quota used and the tick is the even pace, the share an
// even spend of the usable quota would have used by now. Use ahead of the pace
// is red. The reserve is hatched at the end of the bar and holds the pace
// back; a reset card or plan expiry that discards the quota before the reset
// moves it ahead. Without either the pace is the now line. An arrow under the
// bar shows how far they move the pace from now.
func (o overview) windowBar(w *quotaWindow, cy float64, a axis, used, pale Color) {
	if w == nil || w.reset == nil || *w.reset <= o.now {
		return
	}
	if w.start == nil || w.used == nil {
		if x := a.x(*w.reset); a.x0 <= x && x <= a.x1 {
			var marker path
			marker.moveTo(x, cy-4)
			marker.lineTo(x+4, cy)
			marker.lineTo(x, cy+4)
			marker.lineTo(x-4, cy)
			marker.close()
			o.FillPath(&marker, used)
		}
		return
	}
	raw0, raw1 := a.x(*w.start), a.x(*w.reset)
	x0, x1 := a.clamp(raw0), a.clamp(raw1)
	if x1-x0 < 1 {
		return
	}
	at := func(fraction float64) float64 { return raw0 + (raw1-raw0)*fraction }
	pace, elapsed, _ := w.pace(o.now)
	paceX, nowX := at(pace), at(elapsed)
	top, bottom, radius := cy-barH/2, cy+barH/2, barH/2
	o.rect(x0, top, x1, bottom, pale, radius)
	o.ClipRRect(x0, top, x1, bottom, radius)
	if w.reserve > 0 {
		o.hatch(max(x0, at(1-w.reserve)), top, x1, bottom, used)
	}
	if *w.used > 0 {
		usedX := at(min(*w.used, 100) / 100)
		o.rect(x0, top, min(usedX, paceX), bottom, used, 0)
		if usedX > paceX {
			o.rect(paceX, top, usedX, bottom, over, 0)
		}
	}
	o.Restore()
	if math.Abs(paceX-nowX) < 1.5 {
		return
	}
	if x0 <= paceX && paceX <= x1 {
		o.line(paceX, top-3, paceX, bottom+3, paceColor, 2)
	}
	// The arrow runs from now to the pace: green ahead for a cut, grey back
	// for the reserve.
	from, to := a.clamp(nowX), a.clamp(paceX)
	if math.Abs(to-from) < 4 {
		return
	}
	arrowY, c, dir := bottom+5, cutColor, 1.0
	if to < from {
		c, dir = muted, -1
	}
	o.line(from, arrowY, to-dir*4, arrowY, c, 1.5)
	var head path
	head.moveTo(to, arrowY)
	head.lineTo(to-dir*5, arrowY-3)
	head.lineTo(to-dir*5, arrowY+3)
	head.close()
	o.FillPath(&head, c)
}

// hatch fills a rectangle with thin diagonal lines inside the current clip.
func (o overview) hatch(x0, top, x1, bottom float64, c Color) {
	h := bottom - top
	for x := x0 - h; x < x1; x += 4 {
		// Each line rises from (x, bottom) to (x+h, top); clip it to x0..x1.
		t0, t1 := max(0, (x0-x)/h), min(1, (x1-x)/h)
		if t0 < t1 {
			o.line(x+t0*h, bottom-t0*h, x+t1*h, bottom-t1*h, c.alpha(0x8C), 1)
		}
	}
}

func (o overview) remaining(w *quotaWindow) (string, Color) {
	if w.used == nil {
		return "?", muted
	}
	left := max(0, 100-*w.used)
	if left < 10 {
		return percentText(left), over
	}
	return percentText(left), ink
}

// countdown runs to the reset, or in green to a reset card or plan expiry
// that discards the quota sooner.
func (o overview) countdown(w *quotaWindow) (string, Color) {
	switch {
	case w.reset == nil:
		return "—", sub
	case *w.reset <= o.now:
		return "待刷新", sub
	case w.end != nil && *w.end > o.now:
		cause := "卡"
		if w.endsBy == "plan_expiry" {
			cause = "到期"
		}
		return cause + unitGap + durationText(*w.end-o.now), cutColor
	}
	return durationText(*w.reset - o.now), sub
}

func (o overview) valueCell(rightX, cy float64, w *quotaWindow) {
	if w == nil {
		o.text(rightX, cy, "—", 14, muted, Bold, alignRight)
		return
	}
	value, c := o.remaining(w)
	o.text(rightX, cy-7, value, 14, c, Bold, alignRight)
	countdown, c := o.countdown(w)
	o.text(rightX, cy+9, countdown, 11, c, Regular, alignRight)
}

// floorMod is Python's modulo: the result has the divisor's sign.
func floorMod(a, b int) int { return ((a % b) + b) % b }

func (o overview) weekHead(a axis, cy, top, bottom float64) {
	days := int(math.RoundToEven((a.end - a.start) / day))
	todayIndex := int(math.RoundToEven((o.dayStart(o.now) - a.start) / day))
	w := (a.x1 - a.x0) / float64(days)
	o.rect(a.x0+float64(todayIndex)*w, top, a.x0+float64(todayIndex+1)*w, bottom, today, 0)
	step := int(math.Ceil(44 / w)) // room for one MM/DD label
	for d := 0; d <= days; d++ {
		x := a.x0 + float64(d)*w
		labelled := floorMod(d-todayIndex, step) == 0
		if w >= 12 || labelled {
			o.line(x, top, x, bottom, grid, 1)
		}
		if d < days && labelled {
			label := "今天"
			if d != todayIndex {
				label = o.monthDay(a.start + float64(d)*day)
			}
			o.text(x+w/2, cy, label, 11, sub, Regular, alignCenter)
		}
	}
}

func (o overview) fiveHead(a axis, cy, top, bottom float64) {
	for h := math.Ceil(a.start/hour) * hour; h <= a.end; h += hour {
		x := a.x(h)
		o.line(x, top, x, bottom, grid, 1)
		o.line(x, top, x, top+4, muted, 1)
		if int(math.RoundToEven(h/hour))%2 == 0 && a.x0+14 <= x && x <= a.x1-14 {
			o.text(x, cy, fmt.Sprintf("%02d:00", o.at(h).Hour()), 11, sub, Regular, alignCenter)
		}
	}
}

func (o overview) nowLine(a axis, top, bottom float64) {
	x := a.x(o.now)
	if a.x0 <= x && x <= a.x1 {
		o.line(x, top, x, bottom, nowColor, 1.5)
		var capPath path
		capPath.moveTo(x-4, top-5)
		capPath.lineTo(x+4, top-5)
		capPath.lineTo(x, top)
		capPath.close()
		o.FillPath(&capPath, nowColor)
	}
}

func poolLabel(pool resetPool) string {
	switch {
	case strings.HasPrefix(pool.label, "周"):
		return "周"
	case strings.HasPrefix(pool.label, "5h"):
		return "5h"
	}
	return ""
}

type chip struct {
	x       float64
	count   int
	soonest float64
}

// cardChips groups expiries into bins of binDays days on the card axis.
func (o overview) cardChips(pool resetPool, a axis, binDays int, span float64) []chip {
	bins := map[int]*chip{}
	var order []int
	for _, ts := range pool.expires {
		index := int(math.Floor(math.RoundToEven((o.dayStart(ts)-a.start)/day) / float64(binDays)))
		c, ok := bins[index]
		if !ok {
			c = &chip{x: a.x0 + (float64(index)+0.5)*span, soonest: ts}
			bins[index] = c
			order = append(order, index)
		}
		c.count++
		c.soonest = min(c.soonest, ts)
	}
	sort.Ints(order)
	chips := make([]chip, len(order))
	for i, index := range order {
		chips[i] = *bins[index]
	}
	return chips
}

// cardCell shows the count per card type, then expiry chips on the card axis.
func (o overview) cardCell(cy float64, row channel, a axis) {
	anyCount := false
	for _, pool := range row.pools {
		anyCount = anyCount || pool.count > 0
	}
	if row.resetNote != "" && !anyCount {
		x := min(a.clamp(a.x(o.now))+8, right-80)
		c := over
		if row.resetNote == "不支持重置卡" {
			c = muted
		}
		o.text(x, cy, o.fit(row.resetNote, right-x, 12, Regular), 12, c, Regular, alignLeft)
		return
	}
	pools := row.pools
	if len(pools) > 2 {
		pools = pools[:2]
	}
	days := math.RoundToEven((a.end - a.start) / day)
	dayW := (a.x1 - a.x0) / days
	binDays := max(1, int(math.Ceil(chipGap/dayW)))
	nowX := a.x(o.now)
	for i, pool := range pools {
		offset := 0.0
		if len(pools) > 1 {
			offset = laneDY
			if i == 0 {
				offset = -laneDY
			}
		}
		y := cy + offset
		if len(pools) > 1 {
			o.text(cardLabel, y, poolLabel(pool), 11, muted, Regular, alignLeft)
		}
		c := muted
		if pool.count > 0 {
			c = ink
		}
		o.text(cardValue, y, strconv.Itoa(pool.count), 13, c, Bold, alignRight)
		chips := o.cardChips(pool, a, binDays, float64(binDays)*dayW)
		if len(chips) == 0 {
			continue
		}
		o.line(min(nowX, chips[0].x), y, chips[len(chips)-1].x, y, cardLane, 2)
		for _, ch := range chips {
			remaining := ch.soonest - o.now
			color := card
			if remaining < day {
				color = over
			} else if remaining < 3*day {
				color = warn
			}
			radius := 4.0
			if ch.count > 1 {
				radius = 6.5
			}
			o.circle(ch.x, y, radius+1.5, panel)
			o.circle(ch.x, y, radius, color)
			if ch.count > 1 {
				o.text(ch.x, y, strconv.Itoa(ch.count), 9, panel, Bold, alignCenter)
			}
			if 0 < remaining && remaining < day {
				// Away from the other lane: above the top one, below the bottom.
				direction := -1.0
				if offset > 0 {
					direction = 1
				}
				o.text(ch.x, y+(radius+6)*direction, durationText(remaining), 9.5, over, Bold, alignCenter)
			}
		}
	}
}

// overview renders the table from display rows.
func drawOverview(rows []channel, now float64, loc *time.Location, titleSuffix string) ([]byte, error) {
	var live []channel
	for _, row := range rows {
		if row.issue == "" {
			live = append(live, row)
		}
	}
	probe, err := newCanvas(int(width), 1, 2, pageBG)
	if err != nil {
		return nil, err
	}
	measure := overview{painter{probe}, loc, now}
	heights := make([]float64, len(rows))
	body := 64.0
	if len(rows) > 0 {
		body = 0
		for i, row := range rows {
			heights[i] = measure.rowHeight(row)
			body += heights[i]
		}
	}
	probe.Close()
	height := margin + titleH + headH + body + 8 + margin
	if height > maxHeight {
		return nil, ErrTooLarge
	}
	surface, err := newCanvas(int(width), int(height), 2, pageBG)
	if err != nil {
		return nil, err
	}
	defer surface.Close()
	o := overview{painter{surface}, loc, now}

	// Title and the one legend the bars need.
	titleY := margin + 22
	titleW := o.text(margin+4, titleY, "账号额度", 24, ink, Bold, alignLeft)
	heading := o.at(now).Format("01/02 15:04")
	if titleSuffix != "" {
		heading += " · " + titleSuffix
	}
	o.text(margin+18+titleW, titleY+2, heading, 14, sub, Regular, alignLeft)
	x := width - margin - 4
	for _, item := range []struct {
		label string
		kind  string
		c     Color
	}{{"现在", "line", nowColor}, {"均速进度", "line", paceColor}, {"超出均速", "box", over}, {"已用", "box", weekC}, {"保留", "hatch", weekC}, {"剩余", "box", weekPale}} {
		x -= o.measure(item.label, 12, Regular)
		o.text(x, titleY+2, item.label, 12, sub, Regular, alignLeft)
		switch item.kind {
		case "line":
			o.line(x-8, titleY-5, x-8, titleY+9, item.c, 1.5)
			x -= 30
		case "hatch":
			o.rect(x-20, titleY-3, x-6, titleY+7, weekPale, 3)
			o.ClipRRect(x-20, titleY-3, x-6, titleY+7, 3)
			o.hatch(x-20, titleY-3, x-6, titleY+7, item.c)
			o.Restore()
			x -= 36
		default:
			o.rect(x-20, titleY-3, x-6, titleY+7, item.c, 3)
			x -= 36
		}
	}

	top := margin + titleH
	o.rect(margin, top, width-margin, height-margin, panel, 14)
	head := top + headH/2 + 1
	rowsTop := top + headH
	rowsBottom := rowsTop + body
	week, fiveA, cards := o.weekAxis(live), o.fiveAxis(), o.cardAxis(live)
	for _, h := range []struct {
		x     float64
		label string
	}{{requestValue, "今日请求"}, {weekValue, "周"}, {fiveValue, "5h"}, {cardValue, "重置卡"}} {
		o.text(h.x, head, h.label, 12, sub, Bold, alignRight)
	}
	if len(live) > 0 {
		o.weekHead(week, head, rowsTop, rowsBottom)
		o.fiveHead(fiveA, head, rowsTop, rowsBottom)
		o.weekHead(cards, head, rowsTop, rowsBottom)
	}
	y := rowsTop
	for i, row := range rows {
		o.line(left, y, right, y, rule, 1)
		cy := y + heights[i]/2
		o.drawName(y, row)
		counts, color := "—", muted
		if row.requests != nil {
			counts, color = strconv.FormatInt(*row.requests, 10), ink
		}
		size := 15.0
		available := requestValue - left - nameW - 12
		if w := o.measure(counts, size, Regular); w > available {
			size *= available / w
		}
		o.text(requestValue, cy, counts, size, color, Regular, alignRight)
		if row.issue == "" {
			o.valueCell(weekValue, cy, row.weekly)
			o.valueCell(fiveValue, cy, row.fiveHour)
			o.windowBar(row.weekly, cy, week, weekC, weekPale)
			o.windowBar(row.fiveHour, cy, fiveA, five, fivePale)
			o.cardCell(cy, row, cards)
		}
		y += heights[i]
	}
	if len(live) > 0 {
		for _, a := range []axis{week, fiveA, cards} {
			o.nowLine(a, rowsTop, rowsBottom)
		}
	}
	// A second pass for failed rows, which paint over all three axes to print
	// their error. It must follow the now lines.
	y = rowsTop
	for i, row := range rows {
		rowBottom := y + heights[i]
		blank := func(x0 float64) {
			o.rect(x0, y-1, right, rowBottom+1, panel, 0)
			o.line(x0, y, right, y, rule, 1)
			if rowBottom < rowsBottom {
				o.line(x0, rowBottom, right, rowBottom, rule, 1)
			}
		}
		if row.issue != "" {
			x0 := requestValue + 18
			blank(x0)
			o.text(x0+12, y+heights[i]/2, o.fit(row.issue, right-x0-12, 13, Regular), 13, over, Regular, alignLeft)
		}
		y += heights[i]
	}
	if len(rows) == 0 {
		o.text(width/2, y+32, "暂无可展示的账号", 15, muted, Regular, alignCenter)
	}
	return surface.PNG()
}
