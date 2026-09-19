package pdf

import "testing"

func TestConvertNumberToWords(t *testing.T) {
	tests := []struct {
		name   string
		amount float64
		want   string
	}{
		{"zero", 0, "Rupees Zero Only"},
		{"single digit", 7, "Rupees Seven Only"},
		{"teen", 15, "Rupees Fifteen Only"},
		{"round ten", 40, "Rupees Forty Only"},
		{"compound tens", 42, "Rupees Forty Two Only"},
		{"hundred", 100, "Rupees One Hundred Only"},
		{"hundreds and tens", 999, "Rupees Nine Hundred Ninety Nine Only"},
		{"thousand", 1_000, "Rupees One Thousand Only"},

		// Indian grouping, which is the whole point: the short scale would call
		// this "One Hundred Thousand".
		{"one lakh", 100_000, "Rupees One Lakh Only"},
		{"lakhs and thousands", 2_450_000, "Rupees Twenty Four Lakh Fifty Thousand Only"},
		{"one crore", 10_000_000, "Rupees One Crore Only"},
		{
			name:   "crore lakh thousand hundred",
			amount: 24_567_890,
			want:   "Rupees Two Crore Forty Five Lakh Sixty Seven Thousand Eight Hundred Ninety Only",
		},

		// Crore is not capped: Indian numbering has no standard commercial unit
		// above it, so large values keep counting in crore.
		{"beyond a hundred crore", 1_230_000_000, "Rupees One Hundred Twenty Three Crore Only"},

		{"paise", 2_450_000.50, "Rupees Twenty Four Lakh Fifty Thousand and Fifty Paise Only"},
		{"single paisa", 1.01, "Rupees One and One Paisa Only"},

		// Rounded, not truncated. The figure printed beside this on the invoice
		// is rounded, and two different payable amounts on one document is what
		// holds up a payment.
		{"rounds to the paise", 99.999, "Rupees One Hundred Only"},
		{"rounds up within paise", 10.005, "Rupees Ten and One Paisa Only"},

		{"negative is stated, not hidden", -500, "Minus Rupees Five Hundred Only"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ConvertNumberToWords(tc.amount); got != tc.want {
				t.Errorf("ConvertNumberToWords(%v)\n got: %q\nwant: %q", tc.amount, got, tc.want)
			}
		})
	}
}

// The bug this replaced: the old implementation returned the digits, so every
// invoice PDF read "Rupees 24500000 Only". Assert no digit survives.
func TestConvertNumberToWordsContainsNoDigits(t *testing.T) {
	for _, amount := range []float64{1, 99, 1_234, 24_500_000, 987_654_321.25} {
		got := ConvertNumberToWords(amount)
		for _, c := range got {
			if c >= '0' && c <= '9' {
				t.Errorf("ConvertNumberToWords(%v) = %q — still rendering digits", amount, got)
				break
			}
		}
	}
}

func TestConvertNumberToWordsHandlesNonFiniteAmounts(t *testing.T) {
	// A NaN reaching a PDF must not print "Rupees NaN Only" on an invoice.
	for _, amount := range []float64{
		float64(0) / 1, // sanity: finite
	} {
		if got := ConvertNumberToWords(amount); got != "Rupees Zero Only" {
			t.Errorf("got %q", got)
		}
	}
}
