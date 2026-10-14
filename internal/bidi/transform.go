package bidi

// Port of JTOpen BidiTransform.java: the options and the auxiliary outputs of
// BidiText.Transform. The work itself is done by BidiText.Transform (text.go)
// and bidiOrder.order (order.go).
//
// Java fields keep their names (exported). Java's String delimiters are a
// []uint16 here; a nil or empty slice means no delimiters.

// BidiTransform defines a layout transformation. Port of BidiTransform.java.
type BidiTransform struct {
	// Flags are the Bidi flags of the destination BidiText (Java: flags).
	// The Java default is a default BidiFlagSet.
	Flags *BidiFlagSet

	// ImpToImp enables implicit to implicit transformations (Java default true).
	ImpToImp bool
	// RoundTrip uses the "round trip" algorithm for reordering.
	RoundTrip bool
	// WinCompatible uses the "Windows compatible" reordering algorithm.
	WinCompatible bool
	// InsertMarkers adds LRM and RLM markers when needed for round trip (visual to logical).
	InsertMarkers bool
	// RemoveMarkers removes LRM and RLM markers from the destination after the transformation.
	RemoveMarkers bool
	// Delimiters: when of length >= 1, only the text between starting and ending delimiters is transformed.
	Delimiters []uint16
	// Options are the Arabic shaping options; nil means the defaults.
	Options *ArabicOptionSet
	// WordBreak makes white space always follow the base orientation.
	WordBreak bool
	// DestinationRequired is kept for compatibility; the Java transform ignores it.
	DestinationRequired bool
	// SrcToDstMapRequired and DstToSrcMapRequired request the index maps.
	SrcToDstMapRequired bool
	DstToSrcMapRequired bool
	// PropertyMapRequired requests the property map (levels and the new-cell bit).
	PropertyMapRequired bool
	// Continuation is kept for compatibility; the Java transform ignores it.
	Continuation bool

	// Outputs of the last transform.
	InpCount    int
	OutCount    int
	SrcToDstMap []int
	DstToSrcMap []int
	PropertyMap []byte

	myOrder      *bidiOrder
	myShape      *BidiShape
	flags1       *BidiFlagSet
	flags2       *BidiFlagSet
	insertPoints []int64 // Vector of LRM/RLM insertion points; nil is Java null
}

// NewBidiTransform returns a BidiTransform with the Java defaults: a default flag set,
// implicit to implicit transformations enabled, destination required.
func NewBidiTransform() *BidiTransform {
	return &BidiTransform{
		Flags:               NewBidiFlagSet(),
		ImpToImp:            true,
		DestinationRequired: true,
	}
}
