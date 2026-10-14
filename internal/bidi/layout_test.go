package bidi

import (
	"math/rand"
	"testing"
)

// TestNumeralShapingDoesNotLeak checks deviation 4a: numeral shaping of one transform does not
// change the flag set of the string type seen by later transforms (JTOpen shares it).
func TestNumeralShapingDoesNotLeak(t *testing.T) {
	before := flagSetCache[StringTypeST5].GetNumerals()
	for i := 0; i < 3; i++ {
		props := DefaultProperties()
		props.NumeralShaping = NumeralShapingAny
		props.StringType = StringTypeST5
		_, _ = toLogicalCount([]uint16{'a', ' ', '1', '2'}, 424, props)
		_, _ = toHostCount([]uint16{'a', ' ', '1', '2'}, 424, props)
	}
	if got := flagSetCache[StringTypeST5].GetNumerals(); got != before {
		t.Fatalf("shared ST5 numerals changed from %v to %v", before, got)
	}
	// A transform without numeral shaping sees the unchanged cached set.
	a := newAS400Transform(424)
	if a.bdxJ2A.Flags == flagSetCache[a.as400Type] {
		t.Fatal("transform holds the cached flag set object, not a copy")
	}
}

// TestPaddedStaleTail checks deviation 4b: the characters after the valid count are replaced by
// spaces, and the valid characters are those of the faithful result.
func TestPaddedStaleTail(t *testing.T) {
	stale := 0
	for _, ccsid := range []int{420, 424, 425, 862, 864, 916, 1046, 1255, 1256} {
		for st := StringTypeST4; st <= StringTypeST11; st++ {
			stale += paddedStaleCase(t, ccsid, st)
		}
	}
	if stale == 0 {
		t.Fatal("no input left a stale tail; the padding test is vacuous")
	}
}

// paddedStaleCase checks the padding for one CCSID and string type and returns the count of
// inputs that left a stale tail.
func paddedStaleCase(t *testing.T, ccsid, st int) int {
	t.Helper()
	alphabet := []rune("abc 123אבגاب‎‏")
	rnd := rand.New(rand.NewSource(int64(ccsid*31 + st)))
	props := DefaultProperties()
	props.StringType = st
	props.RemoveMarksOnImplicitToVisual = true
	stale := 0
	for n := 0; n < 400; n++ {
		size := rnd.Intn(20)
		text := make([]uint16, size)
		for i := range text {
			text[i] = uint16(alphabet[rnd.Intn(len(alphabet))])
		}
		faithful := append([]uint16(nil), text...)
		raw, valid := toHostCount(faithful, ccsid, props)
		if valid >= len(raw) {
			continue
		}
		stale++
		padded := ToHostPadded(append([]uint16(nil), text...), ccsid, props)
		if len(padded) != len(raw) {
			t.Fatalf("padded length %d, want %d", len(padded), len(raw))
		}
		for i := 0; i < valid; i++ {
			if padded[i] != raw[i] {
				t.Fatalf("valid character %d changed by padding: %04X, want %04X", i, padded[i], raw[i])
			}
		}
		for i := valid; i < len(padded); i++ {
			if padded[i] != ' ' {
				t.Fatalf("stale character %d is %04X, want space", i, padded[i])
			}
		}
	}
	return stale
}

// TestTransformStringBasics checks the string helpers on simple input.
func TestTransformStringBasics(t *testing.T) {
	text := []uint16{'a', 'b'}
	if got := TransformString(text, StringTypeNone, StringTypeST5); len(got) != 2 || got[0] != 'a' {
		t.Fatalf("NONE input changed: %v", got)
	}
	if got := TransformString(nil, StringTypeST4, StringTypeST5); len(got) != 0 {
		t.Fatalf("empty input returned %v", got)
	}
	in := []uint16{'x', '\'', 'y', '\''}
	if got := SQLStatementReordering(in, StringTypeST5, StringTypeST4); &got[0] == &in[0] {
		t.Fatal("SQLStatementReordering aliases its input")
	}
}

func TestStringTypeForSystem(t *testing.T) {
	sys := SystemInfo{Present: true, CCSID: 420, VRM: 0x050400}
	if got := StringTypeForSystem(13488, sys); got != StringTypeST10 {
		t.Fatalf("13488 on bidi server = %d, want ST10", got)
	}
	sys.VRM = 0x050100 // V5R1
	if got := StringTypeForSystem(13488, sys); got != StringTypeST5 {
		t.Fatalf("13488 on V5R1 = %d, want ST5", got)
	}
	if got := StringTypeForSystem(37, SystemInfo{}); got != 0 {
		t.Fatalf("37 without server info = %d, want 0", got)
	}
}
