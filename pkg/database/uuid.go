package database

// IsUUID reports whether s is a canonical 8-4-4-4-12 hex UUID.
//
// Ids from a request reach queries through `::uuid` casts, and Postgres answers
// a bad cast with an error rather than an empty result. Without this check a
// malformed id surfaces as a 500 with a driver message, instead of a 400 naming
// the field that was wrong.
//
// Deliberately not a regexp: this runs per id on list filters, and a hand-rolled
// scan avoids both the compile and the allocation.
func IsUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}
