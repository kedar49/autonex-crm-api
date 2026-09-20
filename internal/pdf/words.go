package pdf

import (
	"math"
	"strings"
)

// Amounts spelled out for the "amount in words" line on an invoice.
//
// Indian numbering, not the short scale: an invoice raised in India reads
// "Two Crore Forty Five Lakh", and "Twenty Four Million Five Hundred Thousand"
// would be wrong on the document even though it names the same number.
//
// Grouping runs crore (10^7), lakh (10^5), thousand, hundred, then the last two
// digits — which is why the groups below are not uniform thousands.

var onesWords = [...]string{
	"", "One", "Two", "Three", "Four", "Five", "Six", "Seven", "Eight", "Nine",
	"Ten", "Eleven", "Twelve", "Thirteen", "Fourteen", "Fifteen", "Sixteen",
	"Seventeen", "Eighteen", "Nineteen",
}

var tensWords = [...]string{
	"", "", "Twenty", "Thirty", "Forty", "Fifty", "Sixty", "Seventy", "Eighty", "Ninety",
}

// twoDigits spells 0-99. The teens are irregular in English, so 10-19 comes
// from the table above rather than being composed.
func twoDigits(n int64) string {
	switch {
	case n == 0:
		return ""
	case n < 20:
		return onesWords[n]
	default:
		if unit := n % 10; unit != 0 {
			return tensWords[n/10] + " " + onesWords[unit]
		}
		return tensWords[n/10]
	}
}

// threeDigits spells 0-999, used for the hundreds group.
func threeDigits(n int64) string {
	if n == 0 {
		return ""
	}
	var parts []string
	if h := n / 100; h > 0 {
		parts = append(parts, onesWords[h]+" Hundred")
	}
	if rest := twoDigits(n % 100); rest != "" {
		parts = append(parts, rest)
	}
	return strings.Join(parts, " ")
}

// numberToWords spells a non-negative whole number in the Indian system.
func numberToWords(n int64) string {
	if n == 0 {
		return "Zero"
	}

	var parts []string
	// Crore is not capped at 99: ten thousand crore is written as such rather
	// than promoted to a larger unit, because Indian numbering has no standard
	// name above crore in commercial use.
	if crore := n / 10_000_000; crore > 0 {
		parts = append(parts, numberToWords(crore)+" Crore")
		n %= 10_000_000
	}
	if lakh := n / 100_000; lakh > 0 {
		parts = append(parts, twoDigits(lakh)+" Lakh")
		n %= 100_000
	}
	if thousand := n / 1_000; thousand > 0 {
		parts = append(parts, twoDigits(thousand)+" Thousand")
		n %= 1_000
	}
	if rest := threeDigits(n); rest != "" {
		parts = append(parts, rest)
	}
	return strings.Join(parts, " ")
}

// ConvertNumberToWords renders an amount as the words an Indian invoice prints,
// e.g. 2450000.50 -> "Rupees Twenty Four Lakh Fifty Thousand and Fifty Paise Only".
//
// Rounded to the paise rather than truncated: the figure beside it on the
// document is rounded, and the two disagreeing on a payable amount is the kind
// of discrepancy that holds up a payment.
func ConvertNumberToWords(amount float64) string {
	if math.IsNaN(amount) || math.IsInf(amount, 0) {
		return "Rupees Zero Only"
	}

	negative := amount < 0
	paise := int64(math.Round(math.Abs(amount) * 100))
	rupees, remainder := paise/100, paise%100

	out := "Rupees " + numberToWords(rupees)
	if remainder > 0 {
		// Singular is "Paisa"; only the plural is "Paise".
		unit := " Paise"
		if remainder == 1 {
			unit = " Paisa"
		}
		out += " and " + twoDigits(remainder) + unit
	}
	if negative {
		// A credit note, or a corrupt figure. Either way, saying so is better
		// than printing the absolute value as if it were payable.
		out = "Minus " + out
	}
	return out + " Only"
}
