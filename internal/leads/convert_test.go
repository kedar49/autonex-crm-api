package leads

import "testing"

func TestDealTitlePrefersOverrideThenCompanyThenName(t *testing.T) {
	tests := []struct {
		name      string
		override  *string
		company   *string
		firstName string
		lastName  *string
		want      string
	}{
		{"explicit override wins", ptr("Q3 renewal"), ptr("Acme"), "Ada", ptr("Lovelace"), "Q3 renewal"},
		{"blank override falls through", ptr("   "), ptr("Acme"), "Ada", ptr("Lovelace"), "Acme"},
		{"company when no override", nil, ptr("Acme"), "Ada", ptr("Lovelace"), "Acme"},
		{"blank company falls through", nil, ptr("  "), "Ada", ptr("Lovelace"), "Ada Lovelace"},
		{"full name when no company", nil, nil, "Ada", ptr("Lovelace"), "Ada Lovelace"},
		// A lead only requires a first name, so this is a real case.
		{"first name alone", nil, nil, "Ada", nil, "Ada"},
		{"empty surname is not appended", nil, nil, "Ada", ptr(""), "Ada"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dealTitle(tt.override, tt.company, tt.firstName, tt.lastName)
			if got != tt.want {
				t.Errorf("dealTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidDealStagesMatchTheDealsPipeline(t *testing.T) {
	// This map is a deliberate copy of deals.Stages (importing the deals package
	// would make the two modules mutually dependent). If they drift, conversion
	// starts rejecting valid stages — or worse, relying on the DB CHECK to fail.
	for _, stage := range []string{"lead", "qualified", "proposal", "won", "lost"} {
		if !validDealStages[stage] {
			t.Errorf("deal stage %q missing from validDealStages", stage)
		}
	}
	if len(validDealStages) != 5 {
		t.Errorf("len(validDealStages) = %d, want 5", len(validDealStages))
	}
	// "contacted" is a lead stage; a deal must never accept it.
	if validDealStages["contacted"] {
		t.Error(`"contacted" is a lead stage and must not be a valid deal stage`)
	}
	// A converted lead starts at the beginning of the deal pipeline.
	if defaultDealStage != "lead" {
		t.Errorf("defaultDealStage = %q, want lead", defaultDealStage)
	}
}
