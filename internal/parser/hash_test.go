package parser

import "testing"

func TestSha256NewSum_Deterministic(t *testing.T) {
	a := sha256NewSum("foo", "bar")
	b := sha256NewSum("foo", "bar")
	if a != b {
		t.Errorf("not deterministic: %s vs %s", a, b)
	}
	if len(a) != 64 {
		t.Errorf("expected hex sha256 length 64, got %d", len(a))
	}
}

// "ab"+"c" and "a"+"bc" both concatenate to "abc". Without the NUL
// separator they'd hash identically — which would let two different
// listing states alias to the same snapshot row. The separator prevents
// that; this test guards the property.
func TestSha256NewSum_PartitioningMatters(t *testing.T) {
	a := sha256NewSum("ab", "c")
	b := sha256NewSum("a", "bc")
	if a == b {
		t.Error("different input partitions hashed the same; NUL separator missing?")
	}
}

// Same intent as above, edge-cased: "foo" vs "foo"+"" — without the
// terminator they'd be indistinguishable.
func TestSha256NewSum_TrailingEmptyPartChangesHash(t *testing.T) {
	a := sha256NewSum("foo")
	b := sha256NewSum("foo", "")
	if a == b {
		t.Error("trailing empty part should affect the hash")
	}
}