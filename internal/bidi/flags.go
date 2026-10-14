package bidi

import (
	"errors"
	"strings"
)

// ErrIllegalArgument mirrors java.lang.IllegalArgumentException as thrown by
// the BidiFlagSet constructors and setters of JTOpen.
var ErrIllegalArgument = errors.New("bidi: illegal argument")

// BidiFlag is a single predefined value of one of the 5 Bidi attributes.
// Port of BidiFlag.java. The predefined values are the package variables
// below (Java: BidiFlag.TYPE_IMPLICIT and so on). Comparing two BidiFlag
// values with == is equivalent to Java identity comparison because each
// predefined value is unique.
type BidiFlag struct {
	value int32
	mask  int32 // makes BidiFlagSet.setOneFlag efficient
}

// Predefined BidiFlag values (BidiFlag.java).
var (
	typeImplicit = BidiFlag{itypeImplicit, typeMask}
	typeVisual   = BidiFlag{itypeVisual, typeMask}

	orientationLTR        = BidiFlag{iorientationLTR, orientationMask}
	orientationRTL        = BidiFlag{iorientationRTL, orientationMask}
	orientationContextLTR = BidiFlag{iorientationContextLTR, orientationMask}
	orientationContextRTL = BidiFlag{iorientationContextRTL, orientationMask}

	swapYes = BidiFlag{iswapYes, swapMask}
	swapNo  = BidiFlag{iswapNo, swapMask}

	numeralsNominal    = BidiFlag{inumeralsNominal, numeralsMask}
	numeralsNational   = BidiFlag{inumeralsNational, numeralsMask}
	numeralsContextual = BidiFlag{inumeralsContextual, numeralsMask}
	numeralsAny        = BidiFlag{inumeralsAny, numeralsMask}

	textNominal  = BidiFlag{itextNominal, textMask}
	textShaped   = BidiFlag{itextShaped, textMask}
	textInitial  = BidiFlag{itextInitial, textMask}
	textMiddle   = BidiFlag{itextMiddle, textMask}
	textFinal    = BidiFlag{itextFinal, textMask}
	textIsolated = BidiFlag{itextIsolated, textMask}
)

// Exported aliases so that code outside this package can name the values
// (the Java names are kept in comments).
var (
	TypeImplicit = typeImplicit // BidiFlag.TYPE_IMPLICIT
	TypeVisual   = typeVisual   // BidiFlag.TYPE_VISUAL

	OrientationLTR        = orientationLTR        // BidiFlag.ORIENTATION_LTR
	OrientationRTL        = orientationRTL        // BidiFlag.ORIENTATION_RTL
	OrientationContextLTR = orientationContextLTR // BidiFlag.ORIENTATION_CONTEXT_LTR
	OrientationContextRTL = orientationContextRTL // BidiFlag.ORIENTATION_CONTEXT_RTL

	SwapYes = swapYes // BidiFlag.SWAP_YES
	SwapNo  = swapNo  // BidiFlag.SWAP_NO

	NumeralsNominal    = numeralsNominal    // BidiFlag.NUMERALS_NOMINAL
	NumeralsNational   = numeralsNational   // BidiFlag.NUMERALS_NATIONAL
	NumeralsContextual = numeralsContextual // BidiFlag.NUMERALS_CONTEXTUAL
	NumeralsAny        = numeralsAny        // BidiFlag.NUMERALS_ANY

	TextNominal  = textNominal  // BidiFlag.TEXT_NOMINAL
	TextShaped   = textShaped   // BidiFlag.TEXT_SHAPED
	TextInitial  = textInitial  // BidiFlag.TEXT_INITIAL
	TextMiddle   = textMiddle   // BidiFlag.TEXT_MIDDLE
	TextFinal    = textFinal    // BidiFlag.TEXT_FINAL
	TextIsolated = textIsolated // BidiFlag.TEXT_ISOLATED
)

// Masks and values from BidiFlagSet.java.
const (
	typeMask        int32 = 0x03000000
	itypeImplicit   int32 = 0x01000000
	itypeVisual     int32 = 0x03000000
	orientationMask int32 = 0x00700000

	iorientationLTR        int32 = 0x00100000
	iorientationRTL        int32 = 0x00300000
	iorientationContextLTR int32 = 0x00500000
	iorientationContextRTL int32 = 0x00700000

	swapMask int32 = 0x00030000
	iswapYes int32 = 0x00010000
	iswapNo  int32 = 0x00030000

	numeralsMask        int32 = 0x00000700
	inumeralsNominal    int32 = 0x00000100
	inumeralsNational   int32 = 0x00000300
	inumeralsContextual int32 = 0x00000500
	inumeralsAny        int32 = 0x00000700

	textMask      int32 = 0x0000000F
	itextNominal  int32 = 0x00000001
	itextShaped   int32 = 0x00000003
	itextInitial  int32 = 0x00000005
	itextMiddle   int32 = 0x00000007
	itextFinal    int32 = 0x00000009
	itextIsolated int32 = 0x0000000B

	// Default value of an uninitialized BidiFlagSet:
	// Type implicit, Orientation LTR, Swapping YES, Numerals nominal, Text nominal.
	bidiFlagSetDefault int32 = itypeImplicit | iorientationLTR | iswapYes | inumeralsNominal | itextNominal
)

// BidiFlagSet holds the 5 Bidi attribute values. Port of BidiFlagSet.java.
// The zero value is not the Java default; use NewBidiFlagSet.
type BidiFlagSet struct {
	value int32
}

// NewBidiFlagSet returns a BidiFlagSet with the default value
// (implicit, LTR, swapping yes, nominal numerals, nominal text).
// Java: new BidiFlagSet()
func NewBidiFlagSet() *BidiFlagSet {
	return &BidiFlagSet{value: bidiFlagSetDefault}
}

// NewBidiFlagSetFromSet copies an existing BidiFlagSet.
// Java: new BidiFlagSet(BidiFlagSet model)
func NewBidiFlagSetFromSet(model *BidiFlagSet) *BidiFlagSet {
	return &BidiFlagSet{value: model.value}
}

// NewBidiFlagSetFromFlag builds a set from one flag; the others are defaults.
// Java: new BidiFlagSet(BidiFlag flag)
func NewBidiFlagSetFromFlag(flag BidiFlag) *BidiFlagSet {
	return &BidiFlagSet{value: flag.value | bidiFlagSetDefault}
}

// NewBidiFlagSetFromFlags is the Go counterpart of the Java constructors that
// take 2 to 5 BidiFlag arguments. It reproduces each Java overload exactly,
// including the one quirk: the 5-flag constructor does not OR in the default.
// Returns ErrIllegalArgument where Java throws IllegalArgumentException.
// Java: new BidiFlagSet(f1, f2), (f1, f2, f3), (f1, ..., f4), (f1, ..., f5)
func NewBidiFlagSetFromFlags(flags ...BidiFlag) (*BidiFlagSet, error) {
	switch len(flags) {
	case 1:
		return NewBidiFlagSetFromFlag(flags[0]), nil
	case 2:
		v := flags[0].value
		if v&flags[1].value != 0 {
			return nil, ErrIllegalArgument
		}
		v |= flags[1].value | bidiFlagSetDefault
		return &BidiFlagSet{value: v}, nil
	case 3:
		v := flags[0].value
		if v&flags[1].value != 0 {
			return nil, ErrIllegalArgument
		}
		v |= flags[1].value
		if v&flags[2].value != 0 {
			return nil, ErrIllegalArgument
		}
		v |= flags[2].value | bidiFlagSetDefault
		return &BidiFlagSet{value: v}, nil
	case 4:
		v := flags[0].value
		if v&flags[1].value != 0 {
			return nil, ErrIllegalArgument
		}
		v |= flags[1].value
		if v&flags[2].value != 0 {
			return nil, ErrIllegalArgument
		}
		v |= flags[2].value
		if v&flags[3].value != 0 {
			return nil, ErrIllegalArgument
		}
		v |= flags[3].value | bidiFlagSetDefault
		return &BidiFlagSet{value: v}, nil
	case 5:
		v := flags[0].value
		for i := 1; i < 5; i++ {
			if v&flags[i].value != 0 {
				return nil, ErrIllegalArgument
			}
			v |= flags[i].value
		}
		return &BidiFlagSet{value: v}, nil
	}
	return nil, ErrIllegalArgument
}

// NewBidiFlagSetFromChars builds a set from UTF-16 code units following the
// BIDI environment variable syntax (only characters 1 to 5 are used).
// Port of BidiFlagSet(char[] chars).
//
//	char 1: I (implicit) or V (visual)
//	char 2: L (LTR), R (RTL), C (contextual LTR), D (contextual RTL)
//	char 3: Y (swapping on) or N (off)
//	char 4: N, S, I, M, F, B (nominal, shaped, initial, middle, final, isolated)
//	char 5: N (nominal), H (national), C (contextual), A (any)
func NewBidiFlagSetFromChars(chars []uint16) *BidiFlagSet {
	newValue := bidiFlagSetDefault
	n := len(chars)
	for n > 0 {
		if 'V' == chars[0] {
			newValue = (newValue & ^typeMask) | itypeVisual
		} else if 'I' == chars[0] {
			newValue = (newValue & ^typeMask) | itypeImplicit
		}

		if n <= 1 {
			break
		}
		if 'L' == chars[1] {
			newValue = (newValue & ^orientationMask) | iorientationLTR
		} else if 'R' == chars[1] {
			newValue = (newValue & ^orientationMask) | iorientationRTL
		} else if 'C' == chars[1] {
			newValue = (newValue & ^orientationMask) | iorientationContextLTR
		} else if 'D' == chars[1] {
			newValue = (newValue & ^orientationMask) | iorientationContextRTL
		}

		if n <= 2 {
			break
		}
		if 'Y' == chars[2] {
			newValue = (newValue & ^swapMask) | iswapYes
		} else if 'N' == chars[2] {
			newValue = (newValue & ^swapMask) | iswapNo
		}

		if n <= 3 {
			break
		}
		if 'N' == chars[3] {
			newValue = (newValue & ^textMask) | itextNominal
		} else if 'S' == chars[3] {
			newValue = (newValue & ^textMask) | itextShaped
		} else if 'I' == chars[3] {
			newValue = (newValue & ^textMask) | itextInitial
		} else if 'M' == chars[3] {
			newValue = (newValue & ^textMask) | itextMiddle
		} else if 'F' == chars[3] {
			newValue = (newValue & ^textMask) | itextFinal
		} else if 'B' == chars[3] {
			newValue = (newValue & ^textMask) | itextIsolated
		}

		if n <= 4 {
			break
		}
		if 'N' == chars[4] {
			newValue = (newValue & ^numeralsMask) | inumeralsNominal
		} else if 'H' == chars[4] {
			newValue = (newValue & ^numeralsMask) | inumeralsNational
		} else if 'C' == chars[4] {
			newValue = (newValue & ^numeralsMask) | inumeralsContextual
		} else if 'A' == chars[4] {
			newValue = (newValue & ^numeralsMask) | inumeralsAny
		}
		break
	}
	return &BidiFlagSet{value: newValue}
}

// NewBidiFlagSetFromString builds a set from the X/Open "Portable Layout
// Services" modifier syntax ("typeoftext=visual, orientation=rtl, ...").
// Port of BidiFlagSet(String str); returns ErrIllegalArgument on bad syntax.
func NewBidiFlagSetFromString(str string) (*BidiFlagSet, error) {
	n := parseBidiFlagSet(str)
	if n < 0 {
		return nil, ErrIllegalArgument
	}
	return &BidiFlagSet{value: n}, nil
}

// Equals compares two sets by their 5 flag values. Port of equals(Object).
// A nil argument is never equal.
func (f *BidiFlagSet) Equals(other *BidiFlagSet) bool {
	if other == nil {
		return false
	}
	return f.value == other.value
}

// getNumerals returns the numeral shapes flag. Port of getNumerals().
func (f *BidiFlagSet) GetNumerals() BidiFlag {
	switch f.value & numeralsMask {
	case inumeralsNominal:
		return numeralsNominal
	case inumeralsNational:
		return numeralsNational
	case inumeralsContextual:
		return numeralsContextual
	case inumeralsAny:
		return numeralsAny
	}
	return numeralsNominal
}

// GetOrientation returns the orientation flag. Port of getOrientation().
func (f *BidiFlagSet) GetOrientation() BidiFlag {
	switch f.value & orientationMask {
	case iorientationLTR:
		return orientationLTR
	case iorientationRTL:
		return orientationRTL
	case iorientationContextLTR:
		return orientationContextLTR
	case iorientationContextRTL:
		return orientationContextRTL
	}
	return orientationLTR
}

// GetSwap returns the symmetric swapping flag. Port of getSwap().
func (f *BidiFlagSet) GetSwap() BidiFlag {
	switch f.value & swapMask {
	case iswapYes:
		return swapYes
	case iswapNo:
		return swapNo
	}
	return swapYes
}

// GetText returns the text shapes flag. Port of getText().
func (f *BidiFlagSet) GetText() BidiFlag {
	switch f.value & textMask {
	case itextNominal:
		return textNominal
	case itextShaped:
		return textShaped
	case itextInitial:
		return textInitial
	case itextMiddle:
		return textMiddle
	case itextFinal:
		return textFinal
	case itextIsolated:
		return textIsolated
	}
	return textNominal
}

// GetType returns the type-of-text flag. Port of getType().
func (f *BidiFlagSet) GetType() BidiFlag {
	switch f.value & typeMask {
	case itypeImplicit:
		return typeImplicit
	case itypeVisual:
		return typeVisual
	}
	return typeImplicit
}

// HashCode returns the hash of the set, which is its value. Port of hashCode().
func (f *BidiFlagSet) HashCode() int32 {
	return f.value
}

// parseBidiFlagSet is the port of the private static parseBidiFlagSet. It
// returns the value, or a negative number whose absolute value is the ordinal
// (from 1) of the first invalid word token.
func parseBidiFlagSet(str string) int32 {
	var newValue int32 = 0
	var counter int32 = 0
	keyword := ""
	tokens := strings.FieldsFunc(str, func(r rune) bool {
		return r == '\t' || r == ' ' || r == ',' || r == '='
	})

	for _, token := range tokens {
		counter--
		if token == "@ls" {
			continue
		}
		if token == "implicitalg" || token == "checkmode" || token == "shapcharset" {
			keyword = "allowed"
			continue
		}
		if token == "typeoftext" || token == "orientation" || token == "context" ||
			token == "swapping" || token == "numerals" || token == "shaping" {
			keyword = token
			continue
		}
		if token == "visual" {
			if keyword != "typeoftext" {
				return counter
			}
			newValue = (newValue & ^typeMask) | itypeVisual
			continue
		}
		if token == "implicit" {
			if keyword != "typeoftext" {
				return counter
			}
			newValue = (newValue & ^typeMask) | itypeImplicit
			continue
		}
		if token == "ltr" {
			if keyword == "orientation" {
				newValue = (newValue & ^orientationMask) | iorientationLTR
				continue
			}
			if keyword == "context" {
				if (newValue&orientationMask) != iorientationLTR && (newValue&orientationMask) != iorientationRTL {
					newValue = (newValue & ^orientationMask) | iorientationContextLTR
				}
				continue
			}
			return counter
		}
		if token == "rtl" {
			if keyword == "orientation" {
				newValue = (newValue & ^orientationMask) | iorientationRTL
				continue
			}
			if keyword == "context" {
				if (newValue&orientationMask) != iorientationLTR && (newValue&orientationMask) != iorientationRTL {
					newValue = (newValue & ^orientationMask) | iorientationContextRTL
				}
				continue
			}
			return counter
		}
		if token == "contextual" {
			if keyword == "orientation" {
				if (newValue & orientationMask) == iorientationContextRTL {
					continue
				}
				newValue = (newValue & ^orientationMask) | iorientationContextLTR
				continue
			}
			if keyword == "numerals" {
				newValue = (newValue & ^numeralsMask) | inumeralsContextual
				continue
			}
			return counter
		}
		if token == "yes" {
			if keyword != "swapping" {
				return counter
			}
			newValue = (newValue & ^swapMask) | iswapYes
			continue
		}
		if token == "no" {
			if keyword != "swapping" {
				return counter
			}
			newValue = (newValue & ^swapMask) | iswapNo
			continue
		}
		if token == "nominal" {
			if keyword == "numerals" {
				newValue = (newValue & ^numeralsMask) | inumeralsNominal
				continue
			}
			if keyword == "shaping" {
				newValue = (newValue & ^textMask) | itextNominal
				continue
			}
			return counter
		}
		if token == "national" {
			if keyword != "numerals" {
				return counter
			}
			newValue = (newValue & ^numeralsMask) | inumeralsNational
			continue
		}
		if token == "any" {
			if keyword != "numerals" {
				return counter
			}
			newValue = (newValue & ^numeralsMask) | inumeralsAny
			continue
		}
		if token == "shaped" {
			if keyword != "shaping" {
				return counter
			}
			newValue = (newValue & ^textMask) | itextShaped
			continue
		}
		if token == "shform1" {
			if keyword != "shaping" {
				return counter
			}
			newValue = (newValue & ^textMask) | itextInitial
			continue
		}
		if token == "shform2" {
			if keyword != "shaping" {
				return counter
			}
			newValue = (newValue & ^textMask) | itextMiddle
			continue
		}
		if token == "shform3" {
			if keyword != "shaping" {
				return counter
			}
			newValue = (newValue & ^textMask) | itextFinal
			continue
		}
		if token == "shform4" {
			if keyword != "shaping" {
				return counter
			}
			newValue = (newValue & ^textMask) | itextIsolated
			continue
		}
		if keyword == "allowed" {
			continue
		}
		return counter
	}
	return newValue | bidiFlagSetDefault
}

// SetAllFlags copies all flags from another set. Port of setAllFlags(BidiFlagSet).
func (f *BidiFlagSet) SetAllFlags(model *BidiFlagSet) {
	f.value = model.value
}

// SetAllFlagsFromString sets all flags from the modifier syntax. Port of
// setAllFlags(String); returns ErrIllegalArgument on bad syntax.
func (f *BidiFlagSet) SetAllFlagsFromString(str string) error {
	n := parseBidiFlagSet(str)
	if n < 0 {
		return ErrIllegalArgument
	}
	f.value = n
	return nil
}

// Set2AllFlags sets two sets from one "source:target" string, as in
// BidiFlagSet.set2AllFlags. Port of the static method, including its
// tokenizer which keeps delimiters.
func Set2AllFlags(flags1, flags2 *BidiFlagSet, str string) error {
	const delims = "\t ,=:"
	var sb1, sb2 []byte
	sb2Length := 0
	colon := false

	for _, token := range tokenizeKeepDelims(str, delims) {
		if token == " " || token == "\t" {
			sb1 = append(sb1, token...)
			sb2 = append(sb2, token...)
			continue
		}
		if token == "," || token == "=" {
			colon = false
			sb1 = append(sb1, token...)
			sb2 = append(sb2, token...)
			continue
		}
		if token == ":" {
			colon = true
			sb2 = sb2[:sb2Length]
			continue
		}
		if !colon {
			sb1 = append(sb1, token...)
		}
		sb2Length = len(sb2)
		sb2 = append(sb2, token...)
		colon = false
	}

	if err := flags1.SetAllFlagsFromString(string(sb1)); err != nil {
		return err
	}
	return flags2.SetAllFlagsFromString(string(sb2))
}

// tokenizeKeepDelims mirrors java.util.StringTokenizer with returnDelims=true:
// every delimiter character is a token of its own, and each maximal run of
// other characters is a token.
func tokenizeKeepDelims(s, delims string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(delims, s[i]) >= 0 {
			if start < i {
				out = append(out, s[start:i])
			}
			out = append(out, s[i:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// SetOneFlag replaces one attribute without changing the others. Port of setOneFlag.
func (f *BidiFlagSet) SetOneFlag(newFlag BidiFlag) {
	f.value = (f.value & ^newFlag.mask) | newFlag.value
}

// String returns the human readable form. Port of toString().
func (f *BidiFlagSet) String() string {
	return bidiFlagSetString(f.value)
}

// bidiFlagSetString is the port of the static toString(int value).
func bidiFlagSetString(value int32) string {
	typeStrings := []string{"implicit", "visual"}
	orientationStrings := []string{"ltr", "rtl", "contextual, context=ltr", "contextual, context=rtl"}
	swapStrings := []string{"yes", "no"}
	numeralsStrings := []string{"nominal", "national", "contextual", "any"}
	textStrings := []string{"nominal", "shaped", "shform1", "shform2", "shform3", "shform4", "invalid", "invalid"}

	return "typeoftext=" + typeStrings[(value&typeMask)>>25] +
		", " +
		"orientation=" + orientationStrings[(value&orientationMask)>>21] +
		", " +
		"swapping=" + swapStrings[(value&swapMask)>>17] +
		", " +
		"numerals=" + numeralsStrings[(value&numeralsMask)>>9] +
		", " +
		"shaping=" + textStrings[(value&textMask)>>1]
}
