package bidi

// ArabicOption is one predefined Arabic shaping option. Port of ArabicOption.java.
type ArabicOption struct {
	value int32
	mask  int32 // makes ArabicOptionSet.setOneOption efficient
}

// Masks and values from ArabicOptionSet.java.
const (
	lamalefMask       int32 = 0x0F000000
	ilamalefAuto      int32 = 0x01000000
	ilamalefNear      int32 = 0x03000000
	ilamalefAtBegin   int32 = 0x05000000
	ilamalefAtEnd     int32 = 0x07000000
	ilamalefResizeBuf int32 = 0x09000000

	seenMask     int32 = 0x00700000
	iseenAuto    int32 = 0x00100000
	iseenAtBegin int32 = 0x00300000
	iseenAtEnd   int32 = 0x00500000
	iseenNear    int32 = 0x00700000

	yehhamzaMask            int32 = 0x000F0000
	iyehhamzaAuto           int32 = 0x00010000
	iyehhamzaTwoCellAtBegin int32 = 0x00030000
	iyehhamzaTwoCellAtEnd   int32 = 0x00050000
	iyehhamzaTwoCellNear    int32 = 0x00070000
	iyehhamzaOneCell        int32 = 0x00090000

	tashkeelMask                     int32 = 0x00000F00
	itashkeelAuto                    int32 = 0x00000100
	itashkeelCustomizedWithZeroWidth int32 = 0x00000300
	itashkeelCustomizedWithWidth     int32 = 0x00000500
	itashkeelCustomizedAtBegin       int32 = 0x00000700
	itashkeelCustomizedAtEnd         int32 = 0x00000900
	itashkeelKeep                    int32 = 0x00000B00

	// Default: lamalef auto, seen auto, yeh hamza auto, tashkeel auto.
	arabicOptionSetDefault int32 = ilamalefAuto | iseenAuto | iyehhamzaAuto | itashkeelAuto
)

// Predefined ArabicOption values (ArabicOption.java). Java names are in comments.
var (
	LamAlefResizeBuffer = ArabicOption{ilamalefResizeBuf, lamalefMask} // LAMALEF_RESIZE_BUFFER
	LamAlefNear         = ArabicOption{ilamalefNear, lamalefMask}      // LAMALEF_NEAR
	LamAlefAtBegin      = ArabicOption{ilamalefAtBegin, lamalefMask}   // LAMALEF_ATBEGIN
	LamAlefAtEnd        = ArabicOption{ilamalefAtEnd, lamalefMask}     // LAMALEF_ATEND
	LamAlefAuto         = ArabicOption{ilamalefAuto, lamalefMask}      // LAMALEF_AUTO

	SeenNear    = ArabicOption{iseenNear, seenMask}    // SEEN_NEAR
	SeenAtBegin = ArabicOption{iseenAtBegin, seenMask} // SEEN_ATBEGIN
	SeenAtEnd   = ArabicOption{iseenAtEnd, seenMask}   // SEEN_ATEND
	SeenAuto    = ArabicOption{iseenAuto, seenMask}    // SEEN_AUTO

	YehHamzaTwoCellNear    = ArabicOption{iyehhamzaTwoCellNear, yehhamzaMask}    // YEHHAMZA_TWO_CELL_NEAR
	YehHamzaTwoCellAtBegin = ArabicOption{iyehhamzaTwoCellAtBegin, yehhamzaMask} // YEHHAMZA_TWO_CELL_ATBEGIN
	YehHamzaTwoCellAtEnd   = ArabicOption{iyehhamzaTwoCellAtEnd, yehhamzaMask}   // YEHHAMZA_TWO_CELL_ATEND
	YehHamzaOneCell        = ArabicOption{iyehhamzaOneCell, yehhamzaMask}        // YEHHAMZA_ONE_CELL
	YehHamzaAuto           = ArabicOption{iyehhamzaAuto, yehhamzaMask}           // YEHHAMZA_AUTO

	TashkeelKeep                    = ArabicOption{itashkeelKeep, tashkeelMask}                    // TASHKEEL_KEEP
	TashkeelCustomizedWithZeroWidth = ArabicOption{itashkeelCustomizedWithZeroWidth, tashkeelMask} // TASHKEEL_CUSTOMIZED_WITHZEROWIDTH
	TashkeelCustomizedWithWidth     = ArabicOption{itashkeelCustomizedWithWidth, tashkeelMask}     // TASHKEEL_CUSTOMIZED_WITHWIDTH
	TashkeelCustomizedAtBegin       = ArabicOption{itashkeelCustomizedAtBegin, tashkeelMask}       // TASHKEEL_CUSTOMIZED_ATBEGIN
	TashkeelCustomizedAtEnd         = ArabicOption{itashkeelCustomizedAtEnd, tashkeelMask}         // TASHKEEL_CUSTOMIZED_ATEND
	TashkeelAuto                    = ArabicOption{itashkeelAuto, tashkeelMask}                    // TASHKEEL_AUTO
)

// ArabicOptionSet holds the four Arabic shaping modes. Port of ArabicOptionSet.java.
// The zero value is not the Java default; use NewArabicOptionSet.
type ArabicOptionSet struct {
	value int32
}

// NewArabicOptionSet returns a set with the default options. Java: new ArabicOptionSet()
func NewArabicOptionSet() *ArabicOptionSet {
	return &ArabicOptionSet{value: arabicOptionSetDefault}
}

// NewArabicOptionSetFromSet copies a set. Java: new ArabicOptionSet(ArabicOptionSet)
func NewArabicOptionSetFromSet(set *ArabicOptionSet) *ArabicOptionSet {
	return &ArabicOptionSet{value: set.value}
}

// NewArabicOptionSetFromOption builds a set from one option; the others are defaults.
// Java: new ArabicOptionSet(ArabicOption)
func NewArabicOptionSetFromOption(option ArabicOption) *ArabicOptionSet {
	return &ArabicOptionSet{value: option.value | arabicOptionSetDefault}
}

// NewArabicOptionSetFromOptions is the Go counterpart of the Java constructors
// taking 2 to 4 options. Each overload is reproduced exactly, including the
// Java quirk that the 4-option constructor does not OR in the default.
// Returns ErrIllegalArgument where Java throws IllegalArgumentException.
// Java: new ArabicOptionSet(o1, o2), (o1, o2, o3), (o1, o2, o3, o4)
func NewArabicOptionSetFromOptions(options ...ArabicOption) (*ArabicOptionSet, error) {
	switch len(options) {
	case 1:
		return NewArabicOptionSetFromOption(options[0]), nil
	case 2:
		v := options[0].value
		if v&options[1].value != 0 {
			return nil, ErrIllegalArgument
		}
		v |= options[1].value | arabicOptionSetDefault
		return &ArabicOptionSet{value: v}, nil
	case 3:
		v := options[0].value
		if v&options[1].value != 0 {
			return nil, ErrIllegalArgument
		}
		v |= options[1].value
		if v&options[2].value != 0 {
			return nil, ErrIllegalArgument
		}
		v |= options[2].value | arabicOptionSetDefault
		return &ArabicOptionSet{value: v}, nil
	case 4:
		v := options[0].value
		for i := 1; i < 4; i++ {
			if v&options[i].value != 0 {
				return nil, ErrIllegalArgument
			}
			v |= options[i].value
		}
		return &ArabicOptionSet{value: v}, nil
	}
	return nil, ErrIllegalArgument
}

// NewArabicOptionSetFromChars builds a set from UTF-16 code units: characters
// 7 to 10 (index 6 to 9) select the lam-alef, seen, yeh hamza and tashkeel modes.
// Port of ArabicOptionSet(char chars[]).
//
//	char 7:  R (resize buffer), N (near), B (begin), E (end), A (auto)
//	char 8:  N (near), B (begin), E (end), A (auto)
//	char 9:  O (one cell), N (two cell near), B (two cell begin), E (two cell end), A (auto)
//	char 10: K (keep), Z (zero width), W (width), B (begin), E (end), A (auto)
func NewArabicOptionSetFromChars(chars []uint16) *ArabicOptionSet {
	newValue := arabicOptionSetDefault
	n := len(chars)
	for n > 6 {
		switch chars[6] {
		case 'R':
			newValue = (newValue & ^lamalefMask) | ilamalefResizeBuf
		case 'N':
			newValue = (newValue & ^lamalefMask) | ilamalefNear
		case 'B':
			newValue = (newValue & ^lamalefMask) | ilamalefAtBegin
		case 'E':
			newValue = (newValue & ^lamalefMask) | ilamalefAtEnd
		case 'A':
			newValue = (newValue & ^lamalefMask) | ilamalefAuto
		}
		if n <= 7 {
			break
		}
		switch chars[7] {
		case 'N':
			newValue = (newValue & ^seenMask) | iseenNear
		case 'B':
			newValue = (newValue & ^seenMask) | iseenAtBegin
		case 'E':
			newValue = (newValue & ^seenMask) | iseenAtEnd
		case 'A':
			newValue = (newValue & ^seenMask) | iseenAuto
		}
		if n <= 8 {
			break
		}
		switch chars[8] {
		case 'O':
			newValue = (newValue & ^yehhamzaMask) | iyehhamzaOneCell
		case 'N':
			newValue = (newValue & ^yehhamzaMask) | iyehhamzaTwoCellNear
		case 'B':
			newValue = (newValue & ^yehhamzaMask) | iyehhamzaTwoCellAtBegin
		case 'E':
			newValue = (newValue & ^yehhamzaMask) | iyehhamzaTwoCellAtEnd
		case 'A':
			newValue = (newValue & ^yehhamzaMask) | iyehhamzaAuto
		}
		if n <= 9 {
			break
		}
		switch chars[9] {
		case 'K':
			newValue = (newValue & ^tashkeelMask) | itashkeelKeep
		case 'Z':
			newValue = (newValue & ^tashkeelMask) | itashkeelCustomizedWithZeroWidth
		case 'W':
			newValue = (newValue & ^tashkeelMask) | itashkeelCustomizedWithWidth
		case 'B':
			newValue = (newValue & ^tashkeelMask) | itashkeelCustomizedAtBegin
		case 'E':
			newValue = (newValue & ^tashkeelMask) | itashkeelCustomizedAtEnd
		case 'A':
			newValue = (newValue & ^tashkeelMask) | itashkeelAuto
		}
		break
	}
	return &ArabicOptionSet{value: newValue}
}

// Equals compares the four modes. Port of equals(Object); nil is never equal.
func (s *ArabicOptionSet) Equals(other *ArabicOptionSet) bool {
	if other == nil {
		return false
	}
	return s.GetLamAlefMode() == other.GetLamAlefMode() &&
		s.GetSeenMode() == other.GetSeenMode() &&
		s.GetYehHamzaMode() == other.GetYehHamzaMode() &&
		s.GetTashkeelMode() == other.GetTashkeelMode()
}

// GetLamAlefMode port of getLamAlefMode().
func (s *ArabicOptionSet) GetLamAlefMode() ArabicOption {
	switch s.value & lamalefMask {
	case ilamalefResizeBuf:
		return LamAlefResizeBuffer
	case ilamalefNear:
		return LamAlefNear
	case ilamalefAtBegin:
		return LamAlefAtBegin
	case ilamalefAtEnd:
		return LamAlefAtEnd
	case ilamalefAuto:
		return LamAlefAuto
	}
	return LamAlefAtBegin
}

// GetSeenMode port of getSeenMode().
func (s *ArabicOptionSet) GetSeenMode() ArabicOption {
	switch s.value & seenMask {
	case iseenNear:
		return SeenNear
	case iseenAtBegin:
		return SeenAtBegin
	case iseenAtEnd:
		return SeenAtEnd
	case iseenAuto:
		return SeenAuto
	}
	return SeenNear
}

// GetYehHamzaMode port of getYehHamzaMode().
func (s *ArabicOptionSet) GetYehHamzaMode() ArabicOption {
	switch s.value & yehhamzaMask {
	case iyehhamzaTwoCellNear:
		return YehHamzaTwoCellNear
	case iyehhamzaTwoCellAtBegin:
		return YehHamzaTwoCellAtBegin
	case iyehhamzaTwoCellAtEnd:
		return YehHamzaTwoCellAtEnd
	case iyehhamzaOneCell:
		return YehHamzaOneCell
	case iyehhamzaAuto:
		return YehHamzaAuto
	}
	return YehHamzaTwoCellNear
}

// GetTashkeelMode port of getTashkeelMode().
func (s *ArabicOptionSet) GetTashkeelMode() ArabicOption {
	switch s.value & tashkeelMask {
	case itashkeelKeep:
		return TashkeelKeep
	case itashkeelCustomizedWithZeroWidth:
		return TashkeelCustomizedWithZeroWidth
	case itashkeelCustomizedWithWidth:
		return TashkeelCustomizedWithWidth
	case itashkeelCustomizedAtBegin:
		return TashkeelCustomizedAtBegin
	case itashkeelCustomizedAtEnd:
		return TashkeelCustomizedAtEnd
	case itashkeelAuto:
		return TashkeelAuto
	}
	return TashkeelKeep
}

// HashCode returns the value. Port of hashCode().
func (s *ArabicOptionSet) HashCode() int32 {
	return s.value
}

// SetAllOptions copies all options from another set. Port of setAllOptions.
func (s *ArabicOptionSet) SetAllOptions(set *ArabicOptionSet) {
	s.value = set.value
}

// SetOneOption replaces one mode without changing the others. Port of setOneOption.
func (s *ArabicOptionSet) SetOneOption(newOption ArabicOption) {
	s.value = (s.value & ^newOption.mask) | newOption.value
}
