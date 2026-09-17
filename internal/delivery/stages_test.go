package delivery

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// recorder captures the arguments of the one Exec the sync issues.
type recorder struct {
	sql      string
	args     []any
	affected int64
}

func (r *recorder) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	r.sql = sql
	r.args = args
	if r.affected > 0 {
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	return pgconn.NewCommandTag("UPDATE 0"), nil
}

func (r *recorder) QueryRow(context.Context, string, ...any) pgx.Row {
	return fakeRow{}
}

// Every deal stage must display as something. A stage with no tracker label
// would leave the column blank for every deal sitting in it.
func TestEveryDealStageHasATrackerLabel(t *testing.T) {
	for _, stage := range []string{
		"discovery", "site_assessment", "quote_sent", "negotiation",
		"delivery", "post_delivery", "won",
	} {
		if MapDealStageToTracker(stage) == "" {
			t.Errorf("deal stage %q has no tracker label", stage)
		}
	}
}

// Every offered label must move the deal somewhere, or picking it on a linked
// row would silently do nothing.
func TestEveryTrackerLabelResolvesToADealStage(t *testing.T) {
	for label := range dealStageByTracker {
		if MapTrackerStageToDeal(label) == "" {
			t.Errorf("tracker label %q maps to no deal stage", label)
		}
	}
}

// The canonical labels must round-trip. The sub-stages deliberately do not —
// that is what makes them sub-stages — so they are excluded here rather than
// asserted to be lossless.
func TestCanonicalStagesRoundTrip(t *testing.T) {
	for _, stage := range []string{
		"discovery", "site_assessment", "quote_sent", "delivery",
		"post_delivery", "won",
	} {
		label := MapDealStageToTracker(stage)
		if back := MapTrackerStageToDeal(label); back != stage {
			t.Errorf("%s → %q → %s, want %s back", stage, label, back, stage)
		}
	}
}

// negotiation is the exception, and the one the guards exist for: it shows as
// "Quotation Sent", which reads back as quote_sent.
func TestNegotiationCollapsesOntoQuotationSent(t *testing.T) {
	if got := MapDealStageToTracker("negotiation"); got != "Quotation Sent" {
		t.Fatalf("negotiation displays as %q, want \"Quotation Sent\"", got)
	}
	if got := MapTrackerStageToDeal("Quotation Sent"); got != "quote_sent" {
		t.Fatalf("\"Quotation Sent\" reads back as %q, want quote_sent", got)
	}

	// So both stages must be excluded from a move to that label, or re-saving
	// the cell on a negotiating deal would drag it backwards to quote_sent.
	stages := dealStagesFor("Quotation Sent")
	if len(stages) != 2 || stages[0] != "negotiation" || stages[1] != "quote_sent" {
		t.Errorf("dealStagesFor(\"Quotation Sent\") = %v, want [negotiation quote_sent]", stages)
	}
}

// The mirror case: a deal in site_assessment is honestly described by both its
// canonical label and "NDA / Demo", so a deal write must leave either alone.
func TestSubStagesCountAsTheirParent(t *testing.T) {
	labels := trackerLabelsFor("site_assessment")
	if len(labels) != 2 || labels[0] != "NDA / Demo" || labels[1] != "Use Case Discussion" {
		t.Errorf("trackerLabelsFor(site_assessment) = %v, want [NDA / Demo, Use Case Discussion]", labels)
	}

	if got := trackerLabelsFor("quote_sent"); len(got) != 2 {
		t.Errorf("trackerLabelsFor(quote_sent) = %v, want PoC and Quotation Sent", got)
	}
}

// Moving a deal to won must put "PO Received" in the tracker's stage column.
func TestSyncFromDealPushesTheMappedLabel(t *testing.T) {
	rec := &recorder{}
	stage := "won"

	if err := SyncFromDeal(context.Background(), rec, "deal-1", DealFields{Stage: &stage}); err != nil {
		t.Fatalf("SyncFromDeal: %v", err)
	}

	label, ok := rec.args[4].(string)
	if !ok || label != "PO Received" {
		t.Fatalf("pushed label = %v, want \"PO Received\"", rec.args[4])
	}
	// And the labels it must not overwrite travel with it.
	if equivalent, ok := rec.args[5].([]string); !ok || len(equivalent) == 0 {
		t.Fatalf("no equivalent labels passed: %v", rec.args[5])
	}
}

// A write that is not about the pipeline must leave the stage column alone. The
// statement is still issued — products and cameras still need writing — so the
// empty label is what makes the CASE fall through to current_stages.
func TestSyncFromDealWithNoStageLeavesTheColumn(t *testing.T) {
	rec := &recorder{}

	if err := SyncFromDeal(context.Background(), rec, "deal-1", DealFields{}); err != nil {
		t.Fatalf("SyncFromDeal: %v", err)
	}
	if label := rec.args[4].(string); label != "" {
		t.Errorf("pushed label = %q, want empty so the column is untouched", label)
	}
}

// Picking "Deployment" moves the deal to delivery.
func TestSyncStageToDealMovesTheDeal(t *testing.T) {
	rec := &recorder{affected: 1}

	moved, err := SyncStageToDeal(context.Background(), rec, "deal-1", "Deployment")
	if err != nil {
		t.Fatalf("SyncStageToDeal: %v", err)
	}
	if !moved {
		t.Error("moved = false, want true when a row was updated")
	}
	if target := rec.args[1].(string); target != "delivery" {
		t.Errorf("target stage = %q, want delivery", target)
	}
}

// A label with no kanban meaning — a free-text value from an old import — must
// not issue a statement at all.
func TestSyncStageToDealIgnoresAnUnmappedLabel(t *testing.T) {
	rec := &recorder{affected: 1}

	moved, err := SyncStageToDeal(context.Background(), rec, "deal-1", "Waiting on legal")
	if err != nil {
		t.Fatalf("SyncStageToDeal: %v", err)
	}
	if moved {
		t.Error("moved = true for a label with no deal stage")
	}
	if rec.sql != "" {
		t.Error("an unmapped label still issued an UPDATE")
	}
}

// The guard that stops a negotiating deal being dragged backwards: the excluded
// stage list must reach the statement.
func TestSyncStageToDealExcludesEquivalentStages(t *testing.T) {
	rec := &recorder{}

	if _, err := SyncStageToDeal(context.Background(), rec, "deal-1", "Quotation Sent"); err != nil {
		t.Fatalf("SyncStageToDeal: %v", err)
	}

	excluded, ok := rec.args[2].([]string)
	if !ok {
		t.Fatalf("excluded stages = %v, want a []string", rec.args[2])
	}
	var sawNegotiation bool
	for _, s := range excluded {
		if s == "negotiation" {
			sawNegotiation = true
		}
	}
	if !sawNegotiation {
		t.Errorf("excluded = %v, want negotiation among them", excluded)
	}
}
