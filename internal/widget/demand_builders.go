package widget

import (
	"math"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
)

// chartRows is how many bars a demand chart draws before it says it stopped.
// The table behind it stays complete.
const chartRows = 6

func demandMixDef() Definition {
	schema := OptionSchema{
		"days":               {Kind: KindInt, Default: 30, Doc: "Window for the growth figure."},
		"requestorsTermId":   {Kind: KindString, Required: true, Doc: "Label for the requestor count."},
		"growthTermId":       {Kind: KindString, Required: true, Doc: "Label for the demand trend."},
		"bySectorTermId":     {Kind: KindString, Required: true, Doc: "Title of the by-sector chart."},
		"byRequestorTermId":  {Kind: KindString, Required: true, Doc: "Title of the by-requestor chart."},
		"byPublisherTermId":  {Kind: KindString, Required: true, Doc: "Title of a requestor's chart, receiving {publisher}."},
		"sectorColTermId":    {Kind: KindString, Required: true, Doc: "Header of the sector column."},
		"requestorColTermId": {Kind: KindString, Required: true, Doc: "Header of the requestor column."},
		"countColTermId":     {Kind: KindString, Required: true, Doc: "Header of the count column."},
		"shareColTermId":     {Kind: KindString, Required: true, Doc: "Header of the share column."},
		"tableTermId":        {Kind: KindString, Required: true, Doc: "Label of the data-table disclosure."},
		"truncatedTermId":    {Kind: KindString, Required: true, Doc: "Note receiving {shown}, {total} and {share}."},
		"issuerTermId":       {Kind: KindString, Required: true, Doc: "Summary for an issuer, receiving {name}, {vol} and {reqs}."},
		"requestorTermId":    {Kind: KindString, Required: true, Doc: "Summary for a requestor, receiving {share} and {publisher}."},
		"noneTermId":         {Kind: KindString, Required: true, Doc: "Summary for a service nobody has onboarded against."},
	}
	return Definition{
		Type: "demand-mix", Template: "demand-mix", Schema: schema,
		Sources: []string{SourceServiceDemand},
		Doc:     "Who pulls this service, as shares of its demand.",
		Build: func(c Context, o Options) (any, error) {
			if c.Service == nil {
				return DemandView{}, nil
			}
			sv := *c.Service
			all := c.Snapshot.Services
			days := o.Int(schema, "days")

			v := DemandView{
				TableToggle: c.Text.Text(o.String(schema, "tableTermId"), nil),
				CountHeader: c.Text.Text(o.String(schema, "countColTermId"), nil),
				ShareHeader: c.Text.Text(o.String(schema, "shareColTermId"), nil),
			}

			if sv.CallsKey != "" {
				buildRequestorDemand(c, o, schema, &v, all, sv, days)
			} else {
				buildIssuerDemand(c, o, schema, &v, all, sv, days)
			}
			return v, nil
		},
	}
}

// buildIssuerDemand answers "who pulls this document" for a published service.
func buildIssuerDemand(c Context, o Options, schema OptionSchema, v *DemandView, all []model.Service, sv model.Service, days int) {
	callers := callersOf(all, sv)
	issued := sv.Metrics.Volume.Success

	v.Stats = []DemandStat{
		{
			Label: c.Text.Text(o.String(schema, "requestorsTermId"), nil),
			Value: c.Text.Number(float64(len(callers)), 0),
			// A live API nobody has onboarded against is the finding, not an
			// absence of one, so the figure says so rather than sitting neutral.
			Tone: toneIf(len(callers) == 0, "partial"),
		},
		growthStat(c, o, schema, callers, days),
	}

	if len(callers) == 0 {
		v.Summary = c.Text.Text(o.String(schema, "noneTermId"), nil)
		return
	}

	v.Summary = c.Text.Text(o.String(schema, "issuerTermId"), map[string]any{
		"name": c.Text.Text(sv.NameTermID, nil),
		"vol":  c.Text.Number(float64(issued), 0),
		"reqs": len(callers),
	})

	var total int64
	bySector := map[string]int64{}
	for _, r := range callers {
		bySector[r.SectorID] += r.Metrics.Volume.Success
		total += r.Metrics.Volume.Success
	}

	// Two readings of the same demand: which sectors it serves, and which named
	// organisations are actually pulling it. The first only says anything when
	// there is more than one sector in it.
	if len(bySector) > 1 {
		sectors := make([]labelledShare, 0, len(bySector))
		for id, vol := range bySector {
			sectors = append(sectors, labelledShare{Label: c.Text.Text(id, nil), Key: id, Volume: vol})
		}
		bars := shareBars(c, sortShares(sectors), total, issued)
		v.Charts = append(v.Charts, DemandChart{
			Title:      c.Text.Text(o.String(schema, "bySectorTermId"), nil),
			NameHeader: c.Text.Text(o.String(schema, "sectorColTermId"), nil),
			Bars:       bars,
			TableBars:  bars,
		})
	}

	ranked := byVolumeDesc(callers)
	named := make([]labelledShare, 0, len(ranked))
	for _, r := range ranked {
		named = append(named, labelledShare{Label: serviceLabel(c, r), Key: r.ID, Volume: r.Metrics.Volume.Success})
	}
	v.Charts = append(v.Charts, demandChart(c, o, schema,
		c.Text.Text(o.String(schema, "byRequestorTermId"), nil),
		c.Text.Text(o.String(schema, "requestorColTermId"), nil),
		named, total, issued, ""))
}

// buildRequestorDemand answers "how much of this publisher's demand am I" for
// an organisation calling one.
func buildRequestorDemand(c Context, o Options, schema OptionSchema, v *DemandView, all []model.Service, sv model.Service, days int) {
	publisher := publisherOf(all, sv)
	siblings := siblingsOf(all, sv, publisher)

	var total int64
	for _, r := range siblings {
		total += r.Metrics.Volume.Success
	}
	// The bars are shares of what the publisher actually served, so they can
	// never claim more pulls than the API handled.
	issued := total
	publisherName := c.Text.Text(sv.SectorID, nil)
	if publisher != nil {
		issued = publisher.Metrics.Volume.Success
		publisherName = c.Text.Text(publisher.NameTermID, nil)
	}

	share := 0.0
	if total > 0 {
		share = float64(sv.Metrics.Volume.Success) / float64(total) * 100
	}
	v.Summary = c.Text.Text(o.String(schema, "requestorTermId"), map[string]any{
		"share":     c.Text.Percent(share, 1),
		"publisher": publisherName,
	})
	v.Stats = []DemandStat{
		{
			Label: c.Text.Text(o.String(schema, "requestorsTermId"), nil),
			Value: c.Text.Number(float64(len(siblings)), 0),
		},
		growthStat(c, o, schema, []model.Service{sv}, days),
	}

	if len(siblings) <= 1 {
		return
	}

	ranked := byVolumeDesc(siblings)
	named := make([]labelledShare, 0, len(ranked))
	for _, r := range ranked {
		named = append(named, labelledShare{
			Label: serviceLabel(c, r), Key: r.ID,
			Volume: r.Metrics.Volume.Success, Mine: r.ID == sv.ID,
		})
	}
	v.Charts = append(v.Charts, demandChart(c, o, schema,
		c.Text.Text(o.String(schema, "byPublisherTermId"), map[string]any{"publisher": publisherName}),
		c.Text.Text(o.String(schema, "requestorColTermId"), nil),
		named, total, issued, sv.ID))
}

// labelledShare is one row of a demand chart before it is formatted.
type labelledShare struct {
	Label  string
	Key    string
	Volume int64
	Mine   bool
}

func sortShares(list []labelledShare) []labelledShare {
	out := append([]labelledShare(nil), list...)
	// By volume, then by key, so a map's iteration order cannot reorder a
	// chart between two identical loads.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			a, b := out[j-1], out[j]
			if a.Volume > b.Volume || (a.Volume == b.Volume && a.Key <= b.Key) {
				break
			}
			out[j-1], out[j] = b, a
		}
	}
	return out
}

// demandChart draws the busiest few and keeps the whole list behind them.
//
// The open record always appears, even outside the top few: a chart that drops
// "you are here" is describing someone else's demand. It is appended rather
// than substituted, so the "top 6 of 19" note stays literally true.
func demandChart(c Context, o Options, schema OptionSchema, title, nameHeader string, rows []labelledShare, total, issued int64, mineID string) DemandChart {
	shown := rows
	if len(rows) > chartRows {
		shown = append([]labelledShare(nil), rows[:chartRows]...)
		for i, r := range rows {
			if i >= chartRows && r.Key == mineID {
				r.Label = "#" + c.Text.Number(float64(i+1), 0) + " " + r.Label
				shown = append(shown, r)
			}
		}
	}

	out := DemandChart{
		Title:      title,
		NameHeader: nameHeader,
		Bars:       shareBars(c, shown, total, issued),
		TableBars:  shareBars(c, rows, total, issued),
	}
	if len(rows) > chartRows && total > 0 {
		var head int64
		for _, r := range rows[:chartRows] {
			head += r.Volume
		}
		out.Note = c.Text.Text(o.String(schema, "truncatedTermId"), map[string]any{
			"shown": chartRows,
			"total": len(rows),
			"share": c.Text.Percent(float64(head)/float64(total)*100, 1),
		})
	}
	return out
}

func shareBars(c Context, rows []labelledShare, total, issued int64) []DemandBar {
	out := make([]DemandBar, 0, len(rows))
	for i, r := range rows {
		share := 0.0
		if total > 0 {
			share = float64(r.Volume) / float64(total) * 100
		}
		out = append(out, DemandBar{
			Label: r.Label,
			// The count is the share applied to what the issuer actually
			// served, so the column can never add up to more than the API
			// handled.
			Count:     c.Text.Number(math.Round(share/100*float64(issued)), 0),
			Share:     c.Text.Percent(share, 1),
			Percent:   share,
			Highlight: r.Mine || (mineUnset(rows) && i == 0),
		})
	}
	return out
}

// mineUnset reports whether no row is the open record, in which case the
// largest share is the one worth marking.
func mineUnset(rows []labelledShare) bool {
	for _, r := range rows {
		if r.Mine {
			return false
		}
	}
	return true
}

func growthStat(c Context, o Options, schema OptionSchema, list []model.Service, days int) DemandStat {
	st := DemandStat{Label: c.Text.Text(o.String(schema, "growthTermId"), nil), Value: "—"}
	delta, ok := demandDelta(list, days)
	if !ok {
		return st
	}
	st.Value = c.Text.Percent(delta, 1)
	if delta > 0 {
		st.Value = "+" + st.Value
		st.Tone = "ok"
	} else if delta < 0 {
		st.Tone = "partial"
	}
	return st
}

// serviceLabel names a record in a list. State instances share a use-case name,
// so the region is what tells them apart.
func serviceLabel(c Context, sv model.Service) string {
	name := c.Text.Text(sv.NameTermID, nil)
	if sv.Scope == scopeState {
		return name + " · " + c.Text.Text(sv.RegionID, nil)
	}
	return name
}

func toneIf(cond bool, tone string) string {
	if cond {
		return tone
	}
	return ""
}

// --- peers ------------------------------------------------------------------

func peerBarsDef() Definition {
	schema := OptionSchema{
		"titleTermId":      {Kind: KindString, Required: true, Doc: "Title of the block."},
		"issuanceTermId":   {Kind: KindString, Required: true, Doc: "Title of the traffic comparison."},
		"requestorsTermId": {Kind: KindString, Required: true, Doc: "Title of the requestor-count comparison."},
		"thisTermId":       {Kind: KindString, Required: true, Doc: "Label of the open record's bar."},
		"medianTermId":     {Kind: KindString, Required: true, Doc: "Label of the peer median."},
		"bestTermId":       {Kind: KindString, Required: true, Doc: "Label of the peer best."},
		"ruleTermId":       {Kind: KindString, Required: true, Doc: "Sentence naming the peer set, receiving {count}, {group} and {scope}."},
		"noPeersTermId":    {Kind: KindString, Required: true, Doc: "Sentence for a set of one, receiving {group}."},
	}
	return Definition{
		Type: "peer-bars", Template: "peer-bars", Schema: schema,
		Sources: []string{SourceServicePeers},
		Doc:     "How the open service compares with everything like it.",
		Build: func(c Context, o Options) (any, error) {
			if c.Service == nil {
				return PeerView{}, nil
			}
			sv := *c.Service
			all := c.Snapshot.Services
			peers := peersOf(all, sv)

			group := c.Text.Text(sv.CategoryID, nil)
			if sv.CallsKey != "" {
				group = c.Text.Text(sv.SectorID, nil)
			}

			v := PeerView{Title: c.Text.Text(o.String(schema, "titleTermId"), nil)}

			// A set of one has no median and no best. Say that, rather than
			// drawing two zeroes that look like a finding.
			if len(peers) == 0 {
				v.Rule = c.Text.Text(o.String(schema, "noPeersTermId"), map[string]any{"group": group})
				return v, nil
			}
			v.Rule = c.Text.Text(o.String(schema, "ruleTermId"), map[string]any{
				"count": len(peers),
				"group": group,
				"scope": c.Text.Text("chrome.scope."+sv.Scope, nil),
			})

			vols := make([]int64, 0, len(peers))
			for _, p := range peers {
				vols = append(vols, p.Metrics.Volume.Success)
			}
			v.Groups = append(v.Groups, PeerGroup{
				Title: c.Text.Text(o.String(schema, "issuanceTermId"), nil),
				Bars:  peerTrio(c, o, schema, sv.Metrics.Volume.Success, vols),
			})

			// Requestor counts only mean something on the supply side: a
			// requestor has no requestors.
			if sv.CallsKey == "" {
				counts := make([]int64, 0, len(peers))
				for _, p := range peers {
					counts = append(counts, int64(len(callersOf(all, p))))
				}
				v.Groups = append(v.Groups, PeerGroup{
					Title: c.Text.Text(o.String(schema, "requestorsTermId"), nil),
					Bars:  peerTrio(c, o, schema, int64(len(callersOf(all, sv))), counts),
				})
			}
			return v, nil
		},
	}
}

func peerTrio(c Context, o Options, schema OptionSchema, mine int64, peers []int64) []PeerBar {
	scale := max(mine, maxOf(peers), 1)
	bar := func(term string, value int64, highlight bool) PeerBar {
		return PeerBar{
			Label:     c.Text.Text(o.String(schema, term), nil),
			Value:     c.Text.Number(float64(value), 0),
			Percent:   float64(value) / float64(scale) * 100,
			Highlight: highlight,
		}
	}
	return []PeerBar{
		bar("thisTermId", mine, true),
		bar("medianTermId", medianOf(peers), false),
		bar("bestTermId", maxOf(peers), false),
	}
}

// --- the requestor's own record ---------------------------------------------

// RequestorFact is one line of what a requestor is, rather than how it is doing.
type RequestorFact struct {
	Label string
	Value string
	Note  string
}

// RequestorView is the block that only a requestor has: what it calls, how it
// is subscribed, and whose errors its error rate is counting.
type RequestorView struct {
	Facts []RequestorFact
	// Summary and Rule explain the error split, which is the figure a requestor
	// can actually act on: its own malformed requests, not the publisher's
	// failures.
	Title   string
	Summary string
	Rule    string
}

func requestorDetailDef() Definition {
	schema := OptionSchema{
		"callsTermId":        {Kind: KindString, Required: true, Doc: "Label for the volume line."},
		"subscriptionTermId": {Kind: KindString, Required: true, Doc: "Label for the subscription type."},
		"ownErrorsTermId":    {Kind: KindString, Required: true, Doc: "Label for the requestor's own share of failures."},
		"ownDenomTermId":     {Kind: KindString, Required: true, Doc: "What that share is a share of."},
		"publisherTermId":    {Kind: KindString, Required: true, Doc: "Label for the publisher's share of failures."},
		"titleTermId":        {Kind: KindString, Required: true, Doc: "Title of the explanation."},
		"explainTermId":      {Kind: KindString, Required: true, Doc: "Explanation, receiving {errorRate}, {own} and {publisher}."},
		"ruleTermId":         {Kind: KindString, Required: true, Doc: "The rule behind the split."},
	}
	return Definition{
		Type: "requestor-detail", Template: "requestor-detail", Schema: schema,
		Sources: []string{SourceServiceDemand},
		Doc:     "What a requestor is: what it calls, how it subscribes, and whose failures its error rate counts.",
		Build: func(c Context, o Options) (any, error) {
			// An issuer requests nothing, so there is nothing here to say about
			// it. The widget renders as nothing rather than as an empty block.
			if c.Service == nil || c.Service.CallsKey == "" {
				return RequestorView{}, nil
			}
			sv := *c.Service

			publisher := ""
			if p := publisherOf(c.Snapshot.Services, sv); p != nil {
				publisher = c.Text.Text(p.NameTermID, nil)
			}

			own := c.Text.Percent(sv.OwnErrorShare, 1)
			theirs := c.Text.Percent(100-sv.OwnErrorShare, 1)

			return RequestorView{
				Facts: []RequestorFact{
					{
						Label: c.Text.Text(o.String(schema, "callsTermId"), nil),
						Value: c.Text.Number(float64(sv.Metrics.Volume.Total), 0),
						Note:  publisher,
					},
					{
						Label: c.Text.Text(o.String(schema, "subscriptionTermId"), nil),
						Value: c.Text.Text("req.sub."+sv.SubscriptionType, nil),
					},
					{
						Label: c.Text.Text(o.String(schema, "ownErrorsTermId"), nil),
						Value: own,
						Note:  c.Text.Text(o.String(schema, "ownDenomTermId"), nil),
					},
					{
						Label: c.Text.Text(o.String(schema, "publisherTermId"), nil),
						Value: theirs,
						Note:  c.Text.Text(o.String(schema, "ownDenomTermId"), nil),
					},
				},
				Title: c.Text.Text(o.String(schema, "titleTermId"), nil),
				Summary: c.Text.Text(o.String(schema, "explainTermId"), map[string]any{
					"errorRate": c.Text.Percent(sv.Metrics.ErrorRate, 2),
					"own":       own,
					"publisher": theirs,
				}),
				Rule: c.Text.Text(o.String(schema, "ruleTermId"), nil),
			}, nil
		},
	}
}
