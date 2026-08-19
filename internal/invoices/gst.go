package invoices

type GSTSummary struct {
	Subtotal     float64 `json:"subtotal"`
	Discount     float64 `json:"discount"`
	TaxableValue float64 `json:"taxable_value"`
	CGSTAmount   float64 `json:"cgst_amount"`
	SGSTAmount   float64 `json:"sgst_amount"`
	IGSTAmount   float64 `json:"igst_amount"`
	TotalTax     float64 `json:"total_tax"`
	GrandTotal   float64 `json:"grand_total"`
	IsInterstate bool    `json:"is_interstate"`
}

type GSTItemInput struct {
	Quantity        float64 `json:"quantity"`
	UnitPrice       float64 `json:"unit_price"`
	DiscountPercent float64 `json:"discount_percent"`
	TaxPercent      float64 `json:"tax_percent"`
}

func CalculateGST(items []GSTItemInput, sellerStateCode, buyerStateCode string) GSTSummary {
	var summary GSTSummary
	summary.IsInterstate = (sellerStateCode != "" && buyerStateCode != "" && sellerStateCode != buyerStateCode)

	for _, item := range items {
		itemSubtotal := item.Quantity * item.UnitPrice
		itemDiscount := itemSubtotal * (item.DiscountPercent / 100.0)
		taxable := itemSubtotal - itemDiscount

		tax := taxable * (item.TaxPercent / 100.0)

		summary.Subtotal += itemSubtotal
		summary.Discount += itemDiscount
		summary.TaxableValue += taxable
		summary.TotalTax += tax

		if summary.IsInterstate {
			summary.IGSTAmount += tax
		} else {
			summary.CGSTAmount += tax / 2.0
			summary.SGSTAmount += tax / 2.0
		}
	}

	summary.GrandTotal = summary.TaxableValue + summary.TotalTax
	return summary
}
