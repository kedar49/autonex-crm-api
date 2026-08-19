package invoices

import (
	"testing"
)

func TestCalculateGST(t *testing.T) {
	items := []GSTItemInput{
		{Quantity: 2, UnitPrice: 1000, DiscountPercent: 10, TaxPercent: 18}, // 2000 - 200 = 1800 taxable, 18% = 324 tax
	}

	t.Run("Intra-state CGST+SGST split", func(t *testing.T) {
		res := CalculateGST(items, "27", "27")
		if res.IsInterstate {
			t.Errorf("expected intra-state, got interstate")
		}
		if res.TaxableValue != 1800 {
			t.Errorf("expected taxable value 1800, got %f", res.TaxableValue)
		}
		if res.CGSTAmount != 162 || res.SGSTAmount != 162 {
			t.Errorf("expected CGST 162 & SGST 162, got CGST %f & SGST %f", res.CGSTAmount, res.SGSTAmount)
		}
		if res.IGSTAmount != 0 {
			t.Errorf("expected IGST 0, got %f", res.IGSTAmount)
		}
	})

	t.Run("Inter-state IGST", func(t *testing.T) {
		res := CalculateGST(items, "27", "07")
		if !res.IsInterstate {
			t.Errorf("expected inter-state, got intra-state")
		}
		if res.IGSTAmount != 324 {
			t.Errorf("expected IGST 324, got %f", res.IGSTAmount)
		}
		if res.CGSTAmount != 0 || res.SGSTAmount != 0 {
			t.Errorf("expected CGST & SGST 0, got CGST %f & SGST %f", res.CGSTAmount, res.SGSTAmount)
		}
	})
}
