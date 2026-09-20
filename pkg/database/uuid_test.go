package database

import "testing"

func TestIsUUID(t *testing.T) {
	valid := []string{
		"3f2504e0-4f89-11d3-9a0c-0305e82c3301",
		"3F2504E0-4F89-11D3-9A0C-0305E82C3301",
	}
	for _, v := range valid {
		if !IsUUID(v) {
			t.Errorf("IsUUID(%q) = false, want true", v)
		}
	}

	invalid := []string{
		"", "abc", "not-a-uuid-at-all-really-nope-nope",
		"3f2504e0-4f89-11d3-9a0c-0305e82c330",   // too short
		"3f2504e0-4f89-11d3-9a0c-0305e82c33011", // too long
		"3f2504e04f8911d39a0c0305e82c3301",      // no dashes
		"3f2504e0-4f89-11d3-9a0c-0305e82c330g",  // non-hex
		"3f2504e0_4f89-11d3-9a0c-0305e82c3301",  // wrong separator
	}
	for _, v := range invalid {
		if IsUUID(v) {
			t.Errorf("IsUUID(%q) = true, want false", v)
		}
	}
}

// parseFilter is the only thing standing between a malformed query string and a
// ::uuid cast error surfacing as a 500.
