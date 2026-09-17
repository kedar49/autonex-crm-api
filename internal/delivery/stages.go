package delivery

import "sort"

// The tracker and the kanban name the same pipeline differently: the tracker
// kept the sheet's wording, the board uses the deal stages the database
// enforces. These two tables are the translation, and they are deliberately not
// one-to-one in either direction.
//
// Two things follow from that, and both are why the sync needs more care than
// "write the mapped value":
//
//   - Several tracker labels collapse onto one deal stage. "NDA / Demo" and
//     "Use Case Discussion" are both site_assessment; "PoC" and "Quotation Sent"
//     are both quote_sent. The tracker's extra detail is real to the people
//     using it, so a deal write must not flatten a sub-stage back to its parent.
//   - Several deal stages collapse onto one tracker label. negotiation has no
//     wording of its own and shows as "Quotation Sent". So a tracker write must
//     not read that label back as quote_sent and drag the deal out of
//     negotiation.
//
// Hence trackerLabelsFor / dealStagesFor below: each direction asks "does the
// other side already say this?" before writing, rather than comparing the two
// vocabularies directly.
var trackerStageByDeal = map[string]string{
	"discovery":       "Lead / Intro Call",
	"site_assessment": "Use Case Discussion",
	"quote_sent":      "Quotation Sent",
	"negotiation":     "Quotation Sent",
	"won":             "PO Received",
	"delivery":        "Deployment",
	"post_delivery":   "Deployed / Live",
}

var dealStageByTracker = map[string]string{
	"Lead / Intro Call":   "discovery",
	"Use Case Discussion": "site_assessment",
	"NDA / Demo":          "site_assessment",
	"Quotation Sent":      "quote_sent",
	"PoC":                 "quote_sent",
	"PO Received":         "won",
	"Deployment":          "delivery",
	"Deployed / Live":     "post_delivery",
}

// MapDealStageToTracker gives the tracker label a deal stage displays as.
// Empty means the stage has no tracker equivalent, and nothing should be written.
func MapDealStageToTracker(dealStage string) string {
	return trackerStageByDeal[dealStage]
}

// MapTrackerStageToDeal gives the deal stage a tracker label moves a deal to.
// Sub-stages resolve to their parent; empty means no move.
func MapTrackerStageToDeal(trackerStage string) string {
	return dealStageByTracker[trackerStage]
}

// trackerLabelsFor lists every tracker label that represents this deal stage,
// the canonical one included.
//
// Used to leave a sub-stage alone: a deal sitting in site_assessment is honestly
// described by both "Use Case Discussion" and "NDA / Demo", so a deal write that
// finds either must not replace it with the canonical one.
func trackerLabelsFor(dealStage string) []string {
	canonical := trackerStageByDeal[dealStage]
	if canonical == "" {
		return nil
	}

	out := []string{canonical}
	for label, stage := range dealStageByTracker {
		if stage == dealStage && label != canonical {
			out = append(out, label)
		}
	}
	sort.Strings(out)
	return out
}

// dealStagesFor lists every deal stage that displays as this tracker label.
//
// Used to leave a deal alone: negotiation and quote_sent both read as
// "Quotation Sent", so re-saving that cell on a deal already in negotiation must
// not drag it back to quote_sent.
func dealStagesFor(trackerStage string) []string {
	if dealStageByTracker[trackerStage] == "" {
		return nil
	}

	// Every stage that already *displays* as this label. Picking a label the deal
	// is already shown as is not a move, so those stages are left alone; picking
	// any other label is a deliberate move and goes through.
	out := make([]string, 0, 2)
	for stage, label := range trackerStageByDeal {
		if label == trackerStage {
			out = append(out, stage)
		}
	}
	sort.Strings(out)
	return out
}
