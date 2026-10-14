package bidi

// Port of the parts of JTOpen BidiConversionProperties.java that the conversion
// path (AS400BidiTransform, ConvTableBidiMap) uses.
//
// Java's BidiConversionProperties is a bean with setters. Here it is a plain value:
// the zero value of each boolean is the Java default except ImplicitReordering,
// which is inverted (NoImplicitReordering) so that the zero Properties is the Java
// default. Use DefaultProperties for clarity.
//
// Not ported: the Serializable/trace parts, the output maps (they are only
// returned through the BidiTransform API), the lastTransform bookkeeping of
// getBidiConversionProperties, and the options (ArabicOptionSet) property, which
// JTOpen only sets from a transform; its effect through expandLamAlef is kept.

// Numeral shaping values of BidiConversionProperties (NUMERALS_*).
const (
	NumeralShapingDefault    = 0
	NumeralShapingNominal    = 1
	NumeralShapingNational   = 2
	NumeralShapingContextual = 3
	NumeralShapingAny        = 4
)

// Properties controls the conversion of character data between the IBM i layout and
// the Java layout. Port of BidiConversionProperties.java.
type Properties struct {
	// StringType is the Bidi string type (StringTypeDefault, StringTypeNone or StringTypeST4..ST11).
	StringType int
	// NoImplicitReordering turns off implicit to implicit reordering (Java impToImp_ false).
	NoImplicitReordering bool
	// NumericOrderingRoundTrip selects the numeric ordering round trip reordering (Java roundTrip_).
	NumericOrderingRoundTrip bool
	// WinCompatible selects the Windows compatible reordering (Java winCompatible_).
	WinCompatible bool
	// InsertMarkers inserts LRM/RLM when going from visual to implicit (Java insertMarkers_).
	InsertMarkers bool
	// RemoveMarkers removes LRM/RLM in the transformation (Java removeMarkers_).
	RemoveMarkers bool
	// RemoveMarksOnImplicitToVisual removes the markers only when going from implicit to visual.
	// JTOpen turns RemoveMarkers off in the other direction when this is set.
	RemoveMarksOnImplicitToVisual bool
	// WordBreak makes white space always follow the base orientation (Java wordBreak_).
	WordBreak bool
	// NumeralShaping selects the numerals shapes (NumeralShapingDefault takes them from the string type).
	NumeralShaping int
	// ExpandLamAlef decomposes lam-alef ligatures when going from visual to logical (Java expandLamAlef).
	ExpandLamAlef bool
}

// DefaultProperties returns the properties of a new BidiConversionProperties.
func DefaultProperties() Properties {
	return Properties{StringType: StringTypeDefault}
}

// copyOptionsTo copies the options to a transform (BidiConversionProperties.copyOptionsTo).
func (p *Properties) copyOptionsTo(dst *BidiTransform) {
	dst.ImpToImp = !p.NoImplicitReordering
	dst.RoundTrip = p.NumericOrderingRoundTrip
	dst.WinCompatible = p.WinCompatible
	dst.InsertMarkers = p.InsertMarkers
	dst.RemoveMarkers = p.RemoveMarkers
	// JTOpen: options are null unless set; the Properties type has no option set,
	// so the Java value of aos.value is 0 here.
	dst.Options = nil
	aos := &ArabicOptionSet{value: 0}
	if p.ExpandLamAlef {
		aos.SetOneOption(LamAlefResizeBuffer)
		dst.Options = NewArabicOptionSetFromSet(aos)
	}
	dst.WordBreak = p.WordBreak
	dst.DestinationRequired = true
	dst.SrcToDstMapRequired = false
	dst.DstToSrcMapRequired = false
	dst.PropertyMapRequired = false
	dst.Continuation = false
	p.setNumeralShapingOnTransform(dst)
}

// setNumeralShapingOnTransform sets the numerals flag of the transform (BidiConversionProperties).
// Note: the destination flag set may be the shared flag set of the string type (see
// as400transform.go); JTOpen modifies it the same way.
func (p *Properties) setNumeralShapingOnTransform(dst *BidiTransform) {
	switch p.NumeralShaping {
	case NumeralShapingNominal:
		dst.Flags.SetOneFlag(numeralsNominal)
	case NumeralShapingNational:
		dst.Flags.SetOneFlag(numeralsNational)
	case NumeralShapingContextual:
		dst.Flags.SetOneFlag(numeralsContextual)
	case NumeralShapingAny:
		dst.Flags.SetOneFlag(numeralsAny)
	case NumeralShapingDefault:
	}
}
