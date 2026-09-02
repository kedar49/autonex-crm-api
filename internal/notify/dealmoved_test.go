package notify

import (
	"context"
	"strings"
	"testing"
)

func testLabel(stage string) string {
	switch stage {
	case "prospect":
		return "Prospect"
	case "negotiation":
		return "Negotiation"
	}
	return stage
}

func move() DealMove {
	return DealMove{
		DealID:     "d1",
		Title:      "Acme rollout",
		FromStage:  "prospect",
		ToStage:    "negotiation",
		CompanyID:  "c1",
		Amount:     1250,
		StageLabel: testLabel,
	}
}

func TestSubjectNamesTheDealAndDestination(t *testing.T) {
	got := dealMovedSubject(move())
	want := "Acme rollout moved to Negotiation"
	if got != want {
		t.Errorf("subject = %q, want %q", got, want)
	}
}

func TestSubjectFallsBackToTheRawStage(t *testing.T) {
	// StageLabel is optional; without it the raw value must still read sensibly
	// rather than producing an empty destination.
	mv := move()
	mv.StageLabel = nil
	if got := dealMovedSubject(mv); !strings.HasSuffix(got, "moved to negotiation") {
		t.Errorf("subject = %q, want it to end with the raw stage", got)
	}
}

func TestBodyCarriesTheChangeAndTheLink(t *testing.T) {
	n := &Notifier{webAppURL: "https://crm.example.com/"}
	body := n.dealMovedBody(move(), "Acme Ltd", "INR")

	for _, want := range []string{
		"Acme rollout",
		"Acme Ltd",
		"INR 1250.00",
		"Prospect → Negotiation",
		// The trailing slash on webAppURL must not produce a double slash.
		"https://crm.example.com/app/deals",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body is missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "com//app") {
		t.Errorf("double slash in the board link:\n%s", body)
	}
}

func TestBodyOmitsEmptyFields(t *testing.T) {
	n := &Notifier{}
	mv := move()
	mv.Amount = 0
	body := n.dealMovedBody(mv, "", "USD")

	if strings.Contains(body, "Company:") {
		t.Errorf("company line rendered with no company:\n%s", body)
	}
	if strings.Contains(body, "Value:") {
		t.Errorf("value line rendered for a zero amount:\n%s", body)
	}
	if strings.Contains(body, "Open the board") {
		t.Errorf("board link rendered with no web app URL:\n%s", body)
	}
	if !strings.HasPrefix(body, "Someone moved a deal") {
		t.Errorf("actor should fall back to %q:\n%s", "Someone", body)
	}
}

func TestNilNotifierAndSameStageAreNoOps(t *testing.T) {
	// A nil notifier is the "email not wired up" case and must not panic at the
	// call site; a same-stage move is a reorder, which is not news.
	var n *Notifier
	n.DealMoved(context.Background(), "org", "actor", move())

	mv := move()
	mv.ToStage = mv.FromStage
	(&Notifier{}).DealMoved(context.Background(), "org", "actor", mv)
}
