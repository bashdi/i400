package bidi

// Port of JTOpen AS400BidiTransform.java (the parts used by the conversion path):
// the CCSID to string type table, the string type to Bidi flags mapping, and the
// layout transformations toJavaLayout / toAS400Layout.
//
// Exported entry points for the driver, with the Java call sequence they mirror
// (ConvTableBidiMap.java):
//
//	ToLogical(text, ccsid, props)  = ConvTableBidiMap.byteArrayToCharArray, after the table mapping:
//	    type := props.StringType
//	    if type == NONE: return text (the Java dest array, no layout)
//	    abt := new AS400BidiTransform(ccsid)
//	    abt.setBidiConversionProperties(props)
//	    abt.setJavaStringType(props.StringType)
//	    abt.setAS400StringType(getStringType(ccsid))
//	    return abt.toJavaLayout(text)
//
//	ToHost(text, ccsid, props)     = ConvTableBidiMap.charArrayToByteArray, before the table mapping:
//	    abt := new AS400BidiTransform(ccsid)
//	    abt.setBidiConversionProperties(props)
//	    abt.setJavaStringType(props.StringType)
//	    abt.setAS400StringType(getStringType(ccsid))
//	    return abt.toAS400Layout(text)
//	  (toAS400Layout itself returns the text unchanged for NONE; the table mapping that follows is
//	  done by the caller, one unit at a time, over the full length of the returned slice.)
//
//	StringTypeForCCSID(ccsid)      = AS400BidiTransform.getStringType(ccsid)
//
// Quirks of JTOpen reproduced on purpose:
//   - BidiStringType.DEFAULT (0) is not the CCSID type: on the Java side it maps to the
//     default flag set (ST5 flags), and it is only skipped when the CCSID has no string type.
//   - The returned data of toJavaLayout / toAS400Layout is the complete data array of the
//     destination. When LRM/RLM markers were removed and no shaping ran, its length is the
//     length of the source, and the tail holds stale characters. Callers must use the length
//     of the result, as JTOpen does, not the count of valid characters.
//   - Deviation: the flag sets of a string type are cached (FLAG_SET), but each transform gets a
//     copy of the cached set, so numeral shaping (setOneFlag in copyOptionsTo) does not leak into
//     later transforms, and the transform is safe for concurrent use. JTOpen shares the object.

// Internal string types that AS400BidiTransform uses in its CCSID table (ST1..ST14).
const (
	ast1  = 1
	ast2  = 2
	ast3  = 3
	ast4  = 4
	ast5  = 5
	ast6  = 6
	ast7  = 7
	ast8  = 8
	ast9  = 9
	ast10 = 10
	ast11 = 11
	ast12 = 12
	ast13 = 13
	ast14 = 14
)

// flagSetCache holds the flag set of each string type (AS400BidiTransform.FLAG_SET). Filled once
// at package init, so it is read-only afterwards and safe for concurrent use. Callers never get
// the cached object: setAS400StringType takes a copy (deviation from JTOpen, see layout.go), so
// numeral shaping on one transform cannot leak into another.
var flagSetCache [15]*BidiFlagSet

func init() {
	for i := range flagSetCache {
		flagSetCache[i] = initFlagSet(i)
	}
}

// Predefined flag values used by the string types.
func mustFlagSet(flags ...BidiFlag) *BidiFlagSet {
	fs, err := NewBidiFlagSetFromFlags(flags...)
	if err != nil {
		panic(err) // the string type flag sets do not conflict
	}
	return fs
}

// initFlagSet returns a new flag set for a string type (AS400BidiTransform.initFlagSet).
// Unknown string types, including DEFAULT, get the ST5-like default set.
func initFlagSet(stringType int) *BidiFlagSet {
	switch stringType {
	case ast4:
		return mustFlagSet(typeVisual, numeralsNominal, orientationLTR, textShaped, swapNo)
	case ast5:
		return mustFlagSet(typeImplicit, numeralsNominal, orientationLTR, textNominal, swapYes)
	case ast6:
		return mustFlagSet(typeImplicit, numeralsNominal, orientationRTL, textNominal, swapYes)
	case ast7:
		return mustFlagSet(typeVisual, numeralsNominal, orientationContextLTR, textNominal, swapNo)
	case ast8:
		return mustFlagSet(typeVisual, numeralsNominal, orientationRTL, textShaped, swapNo)
	case ast9:
		return mustFlagSet(typeVisual, numeralsNominal, orientationRTL, textShaped, swapYes)
	case ast10:
		return mustFlagSet(typeImplicit, numeralsNominal, orientationContextLTR, textNominal, swapYes)
	case ast11:
		return mustFlagSet(typeImplicit, numeralsNominal, orientationContextRTL, textNominal, swapYes)
	default:
		return mustFlagSet(typeImplicit, numeralsNominal, orientationLTR, textNominal, swapYes)
	}
}

// StringTypeForCCSID returns the default string type of a CCSID, or 0 when the CCSID has
// none (AS400BidiTransform.getStringType). Binary search as in JTOpen.
func StringTypeForCCSID(ccsid int) int {
	low := 0
	high := len(ccsidTable) - 1
	for low < high {
		mid := (low + high) >> 1
		if ccsid < ccsidTable[mid][0] {
			high = mid - 1
		} else if ccsid > ccsidTable[mid][0] {
			low = mid + 1
		} else {
			return ccsidTable[mid][1]
		}
	}
	if low == high && ccsid == ccsidTable[low][0] {
		return ccsidTable[low][1]
	}
	return 0
}

// as400Transform is the port of an AS400BidiTransform object.
type as400Transform struct {
	as400Ccsid int            // server CCSID
	as400Type  int            // string type of the server data
	javaType   int            // string type of the Java data
	bdxJ2A     *BidiTransform // from Java to server
	bdxA2J     *BidiTransform // from server to Java
}

// newAS400Transform is new AS400BidiTransform(ccsid).
func newAS400Transform(ccsid int) *as400Transform {
	a := &as400Transform{bdxJ2A: NewBidiTransform(), bdxA2J: NewBidiTransform()}
	a.bdxA2J.Flags = NewBidiFlagSetFromSet(initFlagSet(0))
	a.setAS400Ccsid(ccsid)
	a.setJavaStringType(StringTypeDefault)
	return a
}

func (a *as400Transform) setAS400Ccsid(ccsid int) {
	a.as400Ccsid = ccsid
	a.setAS400StringType(StringTypeForCCSID(ccsid))
}

func (a *as400Transform) setAS400StringType(stringType int) {
	a.as400Type = stringType
	if stringType < 0 || stringType >= len(flagSetCache) {
		a.bdxJ2A.Flags = initFlagSet(stringType)
		return
	}
	// Copy on read: JTOpen hands out the shared FLAG_SET object itself, and numeral shaping
	// changes it for every later transform of that string type.
	a.bdxJ2A.Flags = NewBidiFlagSetFromSet(flagSetCache[stringType])
}

func (a *as400Transform) setJavaStringType(javaType int) {
	a.javaType = javaType
	a.bdxA2J.Flags = initFlagSet(javaType)
}

// setBidiConversionProperties is AS400BidiTransform.setBidiConversionProperties.
func (a *as400Transform) setBidiConversionProperties(p Properties) {
	a.setJavaStringType(p.StringType)
	p.copyOptionsTo(a.bdxJ2A)
	p.copyOptionsTo(a.bdxA2J)
	if p.RemoveMarksOnImplicitToVisual {
		a.bdxJ2A.RemoveMarkers = true
		a.bdxA2J.RemoveMarkers = false
	}
}

// toJavaLayout converts IBM i layout text to Java layout (AS400BidiTransform.toJavaLayout(char[])).
func (a *as400Transform) toJavaLayout(text []uint16) []uint16 {
	data, _ := a.toJavaLayoutCount(text)
	return data
}

// toJavaLayoutCount is toJavaLayout that also returns the count of valid characters of the
// result (BidiText.count). The data may be longer than the count (see the quirks above).
func (a *as400Transform) toJavaLayoutCount(text []uint16) ([]uint16, int) {
	if a.as400Type == StringTypeNone || a.javaType == StringTypeNone || a.javaType == a.as400Type {
		return text, len(text)
	}
	src := NewBidiTextFromData(a.bdxJ2A.Flags, text)
	dst := src.Transform(a.bdxA2J)
	return dst.Data, dst.Count
}

// toAS400Layout converts Java layout text to IBM i layout (AS400BidiTransform.toAS400Layout(char[])).
func (a *as400Transform) toAS400Layout(text []uint16) []uint16 {
	data, _ := a.toAS400LayoutCount(text)
	return data
}

// toAS400LayoutCount is toAS400Layout that also returns the count of valid characters.
func (a *as400Transform) toAS400LayoutCount(text []uint16) ([]uint16, int) {
	if a.as400Type == StringTypeNone || a.javaType == StringTypeNone || a.javaType == a.as400Type {
		return text, len(text)
	}
	src := NewBidiTextFromData(a.bdxA2J.Flags, text)
	dst := src.Transform(a.bdxJ2A)
	return dst.Data, dst.Count
}

// ToLogical converts text in the IBM i (host) layout of ccsid to the Java/Go layout, after
// the single-byte table mapping. See the call sequence in the package comment.
// The result may be text itself when no layout is applied. The result is JTOpen's complete
// data array, including any stale tail; use ToLogicalPadded in the driver.
func ToLogical(text []uint16, ccsid int, props Properties) []uint16 {
	data, _ := toLogicalCount(text, ccsid, props)
	return data
}

// ToHost converts text in the Java/Go layout to the IBM i (host) layout of ccsid, before the
// single-byte table mapping. The result is the complete destination array (see the quirks above).
// The driver uses ToHostPadded.
func ToHost(text []uint16, ccsid int, props Properties) []uint16 {
	data, _ := toHostCount(text, ccsid, props)
	return data
}

func toLogicalCount(text []uint16, ccsid int, props Properties) ([]uint16, int) {
	if props.StringType == StringTypeNone {
		return text, len(text)
	}
	a := newAS400Transform(ccsid)
	a.setBidiConversionProperties(props)
	a.setJavaStringType(props.StringType)
	a.setAS400StringType(StringTypeForCCSID(ccsid))
	return a.toJavaLayoutCount(text)
}

func toHostCount(text []uint16, ccsid int, props Properties) ([]uint16, int) {
	a := newAS400Transform(ccsid)
	a.setBidiConversionProperties(props)
	a.setJavaStringType(props.StringType)
	a.setAS400StringType(StringTypeForCCSID(ccsid))
	return a.toAS400LayoutCount(text)
}
