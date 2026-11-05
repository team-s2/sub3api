package render

import (
	"errors"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

// The flow image is a three-stage Sankey of API-equivalent USD: users, models,
// subscriptions. Amounts are aggregated exactly; floats are used only for
// pixel geometry. Unknown usage and unknown prices never get invented ribbons.

var palette = []Color{
	0xFF48C98E, 0xFF2F72F6, 0xFFF9C51B, 0xFF42C6E9,
	0xFFFF8B18, 0xFFD2B5F3, 0xFF8252DF, 0xFF9FC5F8,
	0xFFFFC565, 0xFFB8E8CD, 0xFFFFE578, 0xFF38557D,
}

const (
	flowBG          = Color(0xFFF4F5F7)
	trunk           = Color(0xFF9AA3B2)
	flowFont        = 40.0
	nodeWidth       = 56.0
	nodeGap         = 14.0
	labelPadding    = 8.0
	flowMargin      = 80.0
	flowTop         = 300.0
	columnGap       = 120.0
	maxImagePixels  = 32_000_000
	labelLineHeight = 58.0
)

// FlowResult is the management API's flow, with actor names resolved by
// the bot.
type FlowResult struct {
	Period string      `json:"period"`
	Items  []FlowItem  `json:"items"`
	Errors *FlowErrors `json:"errors"`
}

type FlowErrors struct {
	Models        map[string]int64 `json:"models"`
	Subscriptions map[string]int64 `json:"subscriptions"`
}

type FlowItem struct {
	ActorID              string  `json:"actor_id"`
	ActorName            string  `json:"actor_name"`
	Model                string  `json:"model"`
	SubscriptionID       string  `json:"subscription_id"`
	SubscriptionName     string  `json:"subscription_name"`
	Requests             int64   `json:"requests"`
	PricedRequests       int64   `json:"priced_requests"`
	UnknownUsageRequests int64   `json:"unknown_usage_requests"`
	UnpricedRequests     int64   `json:"unpriced_requests"`
	CostUSD              *string `json:"cost_usd"`
	TotalTokens          *string `json:"total_tokens"`
	InputTokens          *string `json:"input_tokens"`
	CachedTokens         *string `json:"cached_tokens"`
}

var errInvalidFlow = errors.New("invalid flow data")

// flowNode aggregates one user, model or subscription.
type flowNode struct {
	label                      string
	cost                       *big.Rat
	requests, priced, unpriced int64
	tokens                     *int64
	cacheInput, cacheRead      *int64
	errors                     *int64
	votes                      map[Color]*big.Rat
}

func (n *flowNode) amount() string {
	if n.priced > 0 {
		return money(n.cost)
	}
	return "—"
}

func (n *flowNode) stats(stage int) string {
	parts := []string{"请求数 " + strconv.FormatInt(n.requests, 10), "Tokens " + tokenCount(n.tokens)}
	if stage == 0 {
		hit := "—"
		if n.cacheInput != nil && n.cacheRead != nil && *n.cacheInput > 0 {
			hit = strconv.FormatFloat(float64(*n.cacheRead)/float64(*n.cacheInput)*100, 'f', 1, 64) + "%"
		}
		parts = append(parts, "缓存命中 "+hit)
	} else {
		count := "—"
		if n.errors != nil {
			count = strconv.FormatInt(*n.errors, 10)
		}
		parts = append(parts, "错误数 "+count)
	}
	if n.unpriced > 0 {
		parts = append(parts, "未计价 "+strconv.FormatInt(n.unpriced, 10))
	}
	return strings.Join(parts, " · ")
}

func flowCount(raw *string) (*int64, error) {
	if raw == nil {
		return nil, nil
	}
	v, err := strconv.ParseInt(strings.TrimSpace(*raw), 10, 64)
	if err != nil || v < 0 {
		return nil, errInvalidFlow
	}
	return &v, nil
}

func addFlowCount(total **int64, value *int64) error {
	if value == nil {
		return nil
	}
	v := *value
	if *total != nil {
		if v > math.MaxInt64-**total {
			return errInvalidFlow
		}
		v += **total
	}
	*total = &v
	return nil
}

type pair struct{ source, target string }

type flowGraph struct {
	stages    [3]map[string]*flowNode
	links     [2]map[pair]*big.Rat
	linkVotes [2]map[pair]map[Color]*big.Rat
	requests  int64
	total     *big.Rat
}

func addVote(votes map[Color]*big.Rat, c Color, cost *big.Rat) {
	if votes[c] == nil {
		votes[c] = new(big.Rat)
	}
	votes[c].Add(votes[c], cost)
}

// dominant is the color with the most cost, ties going to the larger value.
func dominant(votes map[Color]*big.Rat) Color {
	best, found := trunk, false
	for c, v := range votes {
		if !found {
			best, found = c, true
			continue
		}
		if cmp := v.Cmp(votes[best]); cmp > 0 || cmp == 0 && c > best {
			best = c
		}
	}
	return best
}

func flowClean(value string) string { return strings.TrimSpace(clean(value, 0)) }

// orderNodes sorts by cost descending, then label and ID.
func orderNodes(stage map[string]*flowNode) []string {
	keys := make([]string, 0, len(stage))
	for key := range stage {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := stage[keys[i]], stage[keys[j]]
		if cmp := a.cost.Cmp(b.cost); cmp != 0 {
			return cmp > 0
		}
		if a.label != b.label {
			return a.label < b.label
		}
		return keys[i] < keys[j]
	})
	return keys
}

// prepareGraph aggregates every row. Distinct IDs stay distinct even with
// equal names.
func prepareGraph(items []FlowItem) (*flowGraph, error) {
	g := &flowGraph{total: new(big.Rat)}
	for i := range g.stages {
		g.stages[i] = map[string]*flowNode{}
	}
	type path struct {
		ids  [3]string
		cost *big.Rat
	}
	var paths []path
	for _, row := range items {
		requests, priced, unknown, unpriced := row.Requests, row.PricedRequests, row.UnknownUsageRequests, row.UnpricedRequests
		// Free models can have a known zero price while usage is unknown.
		// Pricing and usage coverage overlap; only unpriced known usage is
		// disjoint from both priced and unknown usage.
		for _, v := range []int64{requests, priced, unknown, unpriced} {
			if v < 0 || v > requests {
				return nil, errInvalidFlow
			}
		}
		if priced+unpriced > requests || unknown+unpriced > requests || priced+unknown+unpriced < requests {
			return nil, errInvalidFlow
		}
		cost := new(big.Rat)
		if row.CostUSD != nil {
			if _, ok := cost.SetString(*row.CostUSD); !ok {
				return nil, errInvalidFlow
			}
		} else if priced > 0 {
			return nil, errInvalidFlow
		}
		if cost.Sign() < 0 || priced == 0 && cost.Sign() != 0 {
			return nil, errInvalidFlow
		}
		tokens, err := flowCount(row.TotalTokens)
		if err != nil {
			return nil, err
		}
		input, err := flowCount(row.InputTokens)
		if err != nil {
			return nil, err
		}
		cached, err := flowCount(row.CachedTokens)
		if err != nil || input != nil && cached != nil && *cached > *input {
			return nil, errInvalidFlow
		}
		ids := [3]string{row.ActorID, row.Model, row.SubscriptionID}
		labels := [3]string{row.ActorName, row.Model, row.SubscriptionName}
		for i, fallback := range [3]string{"未知用户", "未知模型", "未知账号"} {
			if labels[i] == "" {
				labels[i] = fallback
			}
		}
		for i, key := range ids {
			node := g.stages[i][key]
			if node == nil {
				node = &flowNode{label: flowClean(labels[i]), cost: new(big.Rat), votes: map[Color]*big.Rat{}}
				g.stages[i][key] = node
			}
			node.cost.Add(node.cost, cost)
			node.requests += requests
			if err := addFlowCount(&node.tokens, tokens); err != nil {
				return nil, err
			}
			if input != nil && cached != nil {
				if err := addFlowCount(&node.cacheInput, input); err != nil {
					return nil, err
				}
				if err := addFlowCount(&node.cacheRead, cached); err != nil {
					return nil, err
				}
			}
			node.priced += priced
			node.unpriced += unpriced
		}
		paths = append(paths, path{ids, cost})
		g.requests += requests
		g.total.Add(g.total, cost)
	}

	// Color by the first stage that branches; shared nodes and ribbons adopt
	// their largest contributor's color.
	colorStage := 0
	for i, stage := range g.stages {
		positive := 0
		for _, node := range stage {
			if node.cost.Sign() > 0 {
				positive++
			}
		}
		if positive > 1 {
			colorStage = i
			break
		}
	}
	var colored []string
	for _, key := range orderNodes(g.stages[colorStage]) {
		if g.stages[colorStage][key].cost.Sign() > 0 {
			colored = append(colored, key)
		}
	}
	colors := map[string]Color{}
	for i := range colored {
		// Palette order runs from the smallest contributor.
		colors[colored[len(colored)-1-i]] = palette[i%len(palette)]
	}
	for i := range g.links {
		g.links[i] = map[pair]*big.Rat{}
		g.linkVotes[i] = map[pair]map[Color]*big.Rat{}
	}
	for _, p := range paths {
		c, ok := colors[p.ids[colorStage]]
		if !ok {
			c = trunk
		}
		for i, key := range p.ids {
			vote := trunk
			if i >= colorStage {
				vote = c
			}
			addVote(g.stages[i][key].votes, vote, p.cost)
		}
		if p.cost.Sign() > 0 {
			for i := range g.links {
				link := pair{p.ids[i], p.ids[i+1]}
				if g.links[i][link] == nil {
					g.links[i][link] = new(big.Rat)
					g.linkVotes[i][link] = map[Color]*big.Rat{}
				}
				g.links[i][link].Add(g.links[i][link], p.cost)
				addVote(g.linkVotes[i][link], c, p.cost)
			}
		}
	}
	return g, nil
}

// wrapText breaks text between any two characters to fit width.
func wrapText(p painter, text string, width, size float64) []string {
	var lines []string
	current := ""
	for _, r := range text {
		if current != "" && p.measure(current+string(r), size, Regular) > width {
			lines = append(lines, current)
			current = ""
		}
		current += string(r)
	}
	return append(lines, current)
}

func periodLabel(period string) string {
	if period == "" {
		return "全部历史"
	}
	runes := []rune(period)
	unit := string(runes[len(runes)-1])
	if label, ok := map[string]string{"m": "分钟", "h": "小时", "d": "天", "w": "周"}[unit]; ok {
		unit = label
	}
	return "最近 " + string(runes[:len(runes)-1]) + " " + unit
}

type labelLine struct {
	text   string
	color  Color
	weight Weight
	size   float64
}

type nodeLabel struct {
	lines        []labelLine
	inlineAmount string
	height       float64
}

type placed struct {
	x, y0, y1, center float64
	node              *flowNode
	key               string
}

type ribbon struct {
	cost                       *big.Rat
	x0, x1, sy0, sy1, ty0, ty1 float64
	color                      Color
}

// Flow draws one image with every node; nothing is folded into "Other".
func Flow(result FlowResult) ([]byte, error) {
	g, err := prepareGraph(result.Items)
	if err != nil {
		return nil, err
	}
	if result.Errors != nil {
		for i, counts := range []map[string]int64{result.Errors.Models, result.Errors.Subscriptions} {
			for key, node := range g.stages[i+1] {
				count := counts[key]
				if count < 0 {
					return nil, errInvalidFlow
				}
				node.errors = &count
			}
		}
	}
	probe, err := newCanvas(1, 1, 1, flowBG)
	if err != nil {
		return nil, err
	}
	measure := painter{probe}
	labels := [3]map[string]nodeLabel{}
	var labelWidths [3]float64
	for i, stage := range g.stages {
		labels[i] = map[string]nodeLabel{}
		for key, node := range stage {
			names := wrapText(measure, node.label, 760, flowFont)
			amount := node.amount()
			// Keep a single name + bold amount line where it fits.
			inlineWidth := 0.0
			if len(names) == 1 {
				inlineWidth = measure.measure(names[0], flowFont, Regular) + 14 + measure.measure(amount, flowFont, Bold)
			}
			inline := len(names) == 1 && inlineWidth <= 1000
			var lines []labelLine
			for _, name := range names {
				lines = append(lines, labelLine{name, ink, Regular, flowFont})
			}
			if !inline {
				for _, line := range wrapText(measure, amount, 1000, flowFont) {
					lines = append(lines, labelLine{line, sub, Bold, flowFont})
				}
			}
			for _, line := range wrapText(measure, node.stats(i), 760, 30) {
				lines = append(lines, labelLine{line, sub, Regular, 30})
			}
			label := nodeLabel{lines: lines, height: float64(len(lines))*labelLineHeight + 16}
			if inline {
				label.inlineAmount = amount
				labelWidths[i] = max(labelWidths[i], inlineWidth)
			}
			for _, line := range lines {
				labelWidths[i] = max(labelWidths[i], measure.measure(line.text, line.size, line.weight))
			}
			labels[i][key] = label
		}
	}
	probe.Close()

	natural := 2*flowMargin + 3*(nodeWidth+labelPadding) + labelWidths[0] + labelWidths[1] + labelWidths[2] + 2*columnGap
	// Each stage is only as wide as its labels need.
	w := max(1800, math.Ceil(natural))
	nodeX := [3]float64{flowMargin}
	for i := 1; i < 3; i++ {
		nodeX[i] = nodeX[i-1] + nodeWidth + labelPadding + labelWidths[i-1] + columnGap
	}
	var reserved [3]float64
	for i, stage := range g.stages {
		for key := range stage {
			reserved[i] += labels[i][key].height + nodeGap
		}
	}
	maxReserved := max(reserved[0], reserved[1], reserved[2])
	h := max(2240, math.Ceil(flowTop+flowMargin+maxReserved+1120))
	if w*h > maxImagePixels {
		return nil, ErrTooLarge
	}
	surface, err := newCanvas(int(w), int(h), 1, flowBG)
	if err != nil {
		return nil, err
	}
	defer surface.Close()
	p := painter{surface}
	p.rect(32, 32, w-32, h-32, panel, 28)
	p.text(flowMargin, 104, "用量计价", 56, ink, Bold, alignLeft)
	p.text(flowMargin, 178, periodLabel(result.Period)+" · "+grouped(strconv.FormatInt(g.requests, 10))+" 次请求", 34, sub, Regular, alignLeft)
	for i, title := range []string{"用户", "模型", "账号"} {
		p.text(nodeX[i], 254, title, 34, sub, Bold, alignLeft)
	}
	if g.requests == 0 {
		p.text(w/2, h/2, "暂无用量记录", 48, sub, Regular, alignCenter)
		return surface.PNG()
	}

	// One pixels-per-dollar scale spans every stage. Minimum space belongs to
	// labels, never to bars or ribbons, so tiny costs do not look large.
	flowPixels := max(1120, h-flowTop-flowMargin-maxReserved)
	share := func(cost *big.Rat) float64 {
		if g.total.Sign() == 0 {
			return 0
		}
		f, _ := new(big.Rat).Quo(cost, g.total).Float64()
		return flowPixels * f
	}
	var positions [3]map[string]*placed
	var order [3][]string
	for i, stage := range g.stages {
		order[i] = orderNodes(stage)
		used := nodeGap * float64(max(len(order[i])-1, 0))
		slots := map[string]float64{}
		for _, key := range order[i] {
			slots[key] = max(labels[i][key].height, share(stage[key].cost))
			used += slots[key]
		}
		cursor := flowTop + (h-flowTop-flowMargin-used)/2
		positions[i] = map[string]*placed{}
		for _, key := range order[i] {
			size := share(stage[key].cost)
			center := cursor + slots[key]/2
			positions[i][key] = &placed{nodeX[i], center - size/2, center + size/2, center, stage[key], key}
			cursor += slots[key] + nodeGap
		}
	}

	var ribbons []ribbon
	var alphas [2]map[pair]float64
	for i, links := range g.links {
		sourceCursor, targetCursor := map[string]float64{}, map[string]float64{}
		for key, node := range positions[i] {
			sourceCursor[key] = node.y0
		}
		for key, node := range positions[i+1] {
			targetCursor[key] = node.y0
		}
		bySource := map[string][]pair{}
		for link := range links {
			bySource[link.source] = append(bySource[link.source], link)
		}
		alphas[i] = map[pair]float64{}
		for _, group := range bySource {
			sort.Slice(group, func(a, b int) bool {
				if cmp := links[group[a]].Cmp(links[group[b]]); cmp != 0 {
					return cmp > 0
				}
				if group[a].source != group[b].source {
					return group[a].source < group[b].source
				}
				return group[a].target < group[b].target
			})
			for rank, link := range group {
				alpha := 0.34
				if len(group) > 1 {
					alpha = 0.24 + float64(rank)/float64(len(group)-1)*0.2
				}
				alphas[i][link] = alpha
			}
		}
		pairs := make([]pair, 0, len(links))
		for link := range links {
			pairs = append(pairs, link)
		}
		sort.Slice(pairs, func(a, b int) bool {
			sa, sb := positions[i][pairs[a].source].y0, positions[i][pairs[b].source].y0
			if sa != sb {
				return sa < sb
			}
			return positions[i+1][pairs[a].target].y0 < positions[i+1][pairs[b].target].y0
		})
		for _, link := range pairs {
			a, b := positions[i][link.source], positions[i+1][link.target]
			height := share(links[link])
			color := dominant(g.linkVotes[i][link])
			alpha := Color(math.RoundToEven(alphas[i][link]*255)) << 24
			ribbons = append(ribbons, ribbon{links[link], a.x + nodeWidth, b.x,
				sourceCursor[link.source], sourceCursor[link.source] + height,
				targetCursor[link.target], targetCursor[link.target] + height,
				color&0xFFFFFF | alpha})
			sourceCursor[link.source] += height
			targetCursor[link.target] += height
		}
	}

	// Small ribbons stay on top.
	sort.SliceStable(ribbons, func(a, b int) bool { return ribbons[a].cost.Cmp(ribbons[b].cost) > 0 })
	for _, rb := range ribbons {
		control := (rb.x1 - rb.x0) * 0.48
		var outline path
		outline.moveTo(rb.x0, rb.sy0)
		outline.cubicTo(rb.x0+control, rb.sy0, rb.x1-control, rb.ty0, rb.x1, rb.ty0)
		outline.lineTo(rb.x1, rb.ty1)
		outline.cubicTo(rb.x1-control, rb.ty1, rb.x0+control, rb.sy1, rb.x0, rb.sy1)
		outline.close()
		p.FillPath(&outline, rb.color)
	}
	for i := range positions {
		for _, key := range order[i] {
			node := positions[i][key]
			if node.node.cost.Sign() != 0 {
				p.rect(node.x, node.y0, node.x+nodeWidth, node.y1, dominant(node.node.votes), min(6, (node.y1-node.y0)/2))
			} else {
				p.line(node.x, node.center, node.x+nodeWidth, node.center, trunk, 2)
			}
			label := labels[i][key]
			top := node.center - float64(len(label.lines)-1)*29
			x := node.x + nodeWidth + labelPadding
			for n, line := range label.lines {
				drawn := p.text(x, top+float64(n)*labelLineHeight, line.text, line.size, line.color, line.weight, alignLeft)
				if n == 0 && label.inlineAmount != "" {
					p.text(x+drawn+14, top, label.inlineAmount, flowFont, sub, Bold, alignLeft)
				}
			}
		}
	}
	return surface.PNG()
}
