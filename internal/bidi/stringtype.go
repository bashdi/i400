package bidi

// String types of the CDRA (Character Data Representation Architecture).
// Port of BidiStringType.java. Each string type is a predefined set of Bidi flags,
// see initFlagSet in as400transform.go.
const (
	// StringTypeDefault is the default string type for Bidi data (logical LTR); it maps to ST5.
	StringTypeDefault = 0
	// StringTypeNone is used when a conversion is wanted without swapping, shaping or transformation.
	StringTypeNone = -1

	// StringTypeST4: visual, LTR, no swapping, nominal numerals, shaped text.
	StringTypeST4 = 4
	// StringTypeST5: implicit, LTR, swapping, nominal numerals, nominal text.
	StringTypeST5 = 5
	// StringTypeST6: implicit, RTL, swapping, nominal numerals, nominal text.
	StringTypeST6 = 6
	// StringTypeST7: visual, contextual LTR, no swapping, nominal numerals, nominal text.
	StringTypeST7 = 7
	// StringTypeST8: visual, RTL, no swapping, nominal numerals, shaped text.
	StringTypeST8 = 8
	// StringTypeST9: visual, RTL, swapping, nominal numerals, shaped text.
	StringTypeST9 = 9
	// StringTypeST10: implicit, contextual LTR, swapping, nominal numerals, nominal text.
	StringTypeST10 = 10
	// StringTypeST11: implicit, contextual RTL, swapping, nominal numerals, nominal text.
	StringTypeST11 = 11
)
