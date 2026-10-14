package bidi

// Port of JTOpen BidiText.java: a character buffer with its Bidi flags.
//
// Java's "interesting" part of the char array is Data[Offset : Offset+Count].
// The Java constructors that copy data are the New... functions below; the
// setCharsRef-style fields are the exported fields themselves.

// BidiText is a Bidi flag set and its character data. Port of BidiText.java.
type BidiText struct {
	Flags  *BidiFlagSet
	Data   []uint16
	Offset int
	Count  int
}

// NewBidiText returns a BidiText with a default flag set and no data (Java: new BidiText()).
func NewBidiText() *BidiText {
	return &BidiText{Flags: NewBidiFlagSet()}
}

// NewBidiTextFromFlags returns a BidiText that uses initFlags without copying them,
// and has no data (Java: new BidiText(BidiFlagSet)).
func NewBidiTextFromFlags(initFlags *BidiFlagSet) *BidiText {
	return &BidiText{Flags: initFlags}
}

// NewBidiTextFromData copies the flags and the data (Java: new BidiText(BidiFlagSet, char[])).
func NewBidiTextFromData(initFlags *BidiFlagSet, initData []uint16) *BidiText {
	return NewBidiTextFromRange(initFlags, initData, 0, len(initData), len(initData))
}

// NewBidiTextFromRange copies length units of initData starting at offset into a new array
// of the given capacity (Java: new BidiText(BidiFlagSet, char[], int, int, int)).
// The Java arraycopy throws when capacity is smaller than length; so does this function.
func NewBidiTextFromRange(initFlags *BidiFlagSet, initData []uint16, offset, length, capacity int) *BidiText {
	if capacity < length {
		panic("bidi: BidiText capacity smaller than length")
	}
	t := &BidiText{Flags: NewBidiFlagSetFromSet(initFlags)}
	t.Data = make([]uint16, capacity)
	copy(t.Data, initData[offset:offset+length])
	t.Count = length
	return t
}

// SetCharsRef replaces the data reference without copying it (Java: setCharsRef).
func (t *BidiText) SetCharsRef(newData []uint16, newOffset, newLength int) {
	t.Data = newData
	t.Offset = newOffset
	t.Count = newLength
}

// ToCharArray returns a copy of the interesting data (Java: toCharArray).
func (t *BidiText) ToCharArray() []uint16 {
	out := make([]uint16, t.Count)
	copy(out, t.Data[t.Offset:t.Offset+t.Count])
	return out
}

// Equals compares the flags and the interesting data (Java: equals(BidiText)).
func (t *BidiText) Equals(other *BidiText) bool {
	if other == nil {
		return false
	}
	if t.Count != other.Count {
		return false
	}
	if t.Flags.value != other.Flags.value {
		return false
	}
	for i := 0; i < t.Count; i++ {
		if t.Data[t.Offset+i] != other.Data[other.Offset+i] {
			return false
		}
	}
	return true
}

// TransformFlags transforms to the given flags with the default options (Java: transform(BidiFlagSet)).
func (t *BidiText) TransformFlags(dstFlags *BidiFlagSet) *BidiText {
	bdx := NewBidiTransform()
	bdx.Flags.SetAllFlags(dstFlags)
	return t.Transform(bdx)
}

// Transform reorders and shapes the data according to the flags of t and of bdx, and
// returns the destination BidiText (Java: transform(BidiTransform)). t is not modified.
// The output fields of bdx (counts and maps) are set as in Java.
func (t *BidiText) Transform(bdx *BidiTransform) *BidiText {
	dst := NewBidiText()
	dst.Flags.SetAllFlags(bdx.Flags)
	if bdx.myOrder == nil {
		bdx.myOrder = &bidiOrder{}
	}
	bdx.myOrder.order(t, dst, bdx)

	if t.Flags.GetText() != dst.Flags.GetText() {
		if bdx.myShape == nil {
			bdx.myShape = &BidiShape{}
			bdx.flags1 = NewBidiFlagSet()
			bdx.flags2 = NewBidiFlagSet()
		}
		bdx.flags1.SetAllFlags(t.Flags)
		bdx.flags2.SetAllFlags(dst.Flags)
		var sData []uint16 // char array for shaping
		if dst.Offset == 0 && len(dst.Data) == dst.Count {
			sData = dst.Data
		} else {
			sData = make([]uint16, dst.Count)
			copy(sData, dst.Data[dst.Offset:dst.Offset+dst.Count])
		}
		sData = bdx.myShape.Shape(bdx.flags1, bdx.flags2, sData, bdx.Options)
		dst.Count = len(sData) // could be changed by shaping
		if dst.Offset == 0 {
			dst.Data = sData
		} else {
			if dst.Offset+dst.Count <= len(dst.Data) {
				copy(dst.Data[dst.Offset:dst.Offset+dst.Count], sData[:dst.Count])
			} else {
				tmp := make([]uint16, dst.Offset+dst.Count)
				copy(tmp, dst.Data[:dst.Offset])
				copy(tmp[dst.Offset:], sData[:dst.Count])
				dst.Data = tmp
			}
		}
	}
	return dst
}
