package bidi

import "sort"

// Port of JTOpen BidiOrder.java: the Unicode-bidi based reordering used by
// BidiText.transform. Java byte arrays and variables are kept as int8/byte and
// Java int variables as int, so the arithmetic wraps the same way.
//
// The Java class is not reentrant (it keeps its state in fields); the state lives
// in the BidiTransform that owns the order object, exactly as in Java.

// Unicode bidi classes as used by BidiOrder.java (UBAT_*).
const (
	ubatB   = 0  // block separator
	ubatS   = 1  // segment separator
	ubatL   = 2  // left to right
	ubatR   = 3  // right to left
	ubatEN  = 4  // European digit
	ubatAN  = 5  // Arabic-Indic digit
	ubatW   = 6  // white space
	ubatN   = 7  // neutral
	ubatBD  = 8  // bidi special codes
	ubatAL  = 9  // Arabic letter
	ubatET  = 10 // European digit terminator
	ubatES  = 11 // European digit separator
	ubatCS  = 12 // common digit separator
	ubatNSM = 13 // non spacing mark

	ubatNSwap int8 = -ubatN // type marked for symmetric swapping
)

const (
	itil   = 8 // impTab column: level increment
	itcond = 9 // impTab column: conditional flag

	impLTR = 4 // impToImpOrient values
	impRTL = 8

	origIdx  = 0 // typeArray: original type
	finalIdx = 1 // typeArray: type after the weak/neutral rules

	lrm     uint16 = 0x200E
	rlm     uint16 = 0x200F
	before         = 0
	after          = 1 << 16
	noDelim uint16 = 0xFFFF
)

// bidiOrder holds the working state of BidiOrder.java.
type bidiOrder struct {
	ucbIx      int
	ucbOutLev  int8
	ucbBasLev  int8
	ucbCurLev  int8
	ucbImpSta  int
	ucbCondPos int
	ucbXType   int
	ucbWTarget int8

	icsNumFlag     BidiFlag
	icsSymmetric   bool
	icsOrientIn    BidiFlag
	icsOrientOut   BidiFlag
	icsTypeIn      BidiFlag
	icsTypeOut     BidiFlag
	myBdx          *BidiTransform
	icsSize        int
	icsBufferIn    []uint16
	icsBufferOut   []uint16
	invertInput    bool
	visToVis       bool
	impTab         [][10]int16
	typeArray      [][2]int8
	insertMarkers  bool
	insertCnt      int
	removeCnt      int
	startL2EN      int
	lastStrongRTL  int
	reqImpToImp    bool
	impToImpOrient int
	impToImpPhase  int
	delim1         uint16
	delim2         uint16
}

func odd(n int) bool { return n&1 == 1 }

func abs8(x int8) int8 {
	if x < 0 {
		return -x
	}
	return x
}

// invertInts reverses buffer[lower..upper] in place (Java invertMap(int[], ...)).
func invertInts(buffer []int, lower, upper int) {
	for ; lower < upper; lower, upper = lower+1, upper-1 {
		buffer[lower], buffer[upper] = buffer[upper], buffer[lower]
	}
}

// invertBytes reverses buffer[lower..upper] in place (Java invertMap(byte[], ...)).
func invertBytes(buffer []byte, lower, upper int) {
	for ; lower < upper; lower, upper = lower+1, upper-1 {
		buffer[lower], buffer[upper] = buffer[upper], buffer[lower]
	}
}

// setVectorSize is Vector.setSize. Shrinking is the only case the algorithm reaches;
// growing pads with zeros (Java pads with null, which the algorithm never reads).
func setVectorSize(v []int64, n int) []int64 {
	if n <= len(v) {
		return v[:n]
	}
	for len(v) < n {
		v = append(v, 0)
	}
	return v
}

// ucqsymm returns the symmetric counterpart of a bracket-like character (UCQSYMM).
func ucqsymm(x uint16) uint16 {
	low, high := 0, 23
	for low <= high {
		mid := (low + high) / 2
		c := symPairs[mid][0]
		if x < c {
			high = mid - 1
		} else if x > c {
			low = mid + 1
		} else {
			return symPairs[mid][1]
		}
	}
	return x
}

// addPoint records an LRM/RLM insertion point (BidiOrder.addPoint). The key packs the
// position, the after flag and the character exactly like the Java Long, including the
// 32-bit int arithmetic of the Java expression.
func (o *bidiOrder) addPoint(pos, aft int, insert uint16) {
	bdx := o.myBdx
	if bdx.insertPoints == nil {
		bdx.insertPoints = make([]int64, 0, 10)
	}
	key := int32(pos)<<17 + int32(aft) + int32(insert)
	bdx.insertPoints = append(bdx.insertPoints, int64(key))
}

func (o *bidiOrder) afterAN(i int) int {
	for i < o.icsSize && o.typeArray[i][finalIdx] == ubatAN {
		i++
	}
	return i
}

func (o *bidiOrder) afterENAN(i int) int {
	for i < o.icsSize && (o.typeArray[i][finalIdx] == ubatEN || o.typeArray[i][finalIdx] == ubatAN) {
		i++
	}
	return i
}

func (o *bidiOrder) beforeENAN(i int) int {
	for i >= 0 && (o.typeArray[i][finalIdx] == ubatEN || o.typeArray[i][finalIdx] == ubatAN) {
		i--
	}
	return i
}

// symmetricSwap toggles the N / N_SWAP marking of position i when its level parity changed.
func (o *bidiOrder) symmetricSwap(i int, oldLevel, level int8) {
	if o.icsSymmetric && odd(int(oldLevel)^int(level)) {
		pos := i
		if o.impToImpPhase == 2 {
			pos = o.myBdx.DstToSrcMap[i]
		}
		if o.typeArray[pos][origIdx] == ubatN {
			o.typeArray[pos][origIdx] = ubatNSwap
		} else if o.typeArray[pos][origIdx] == ubatNSwap {
			o.typeArray[pos][origIdx] = ubatN
		}
	}
}

// implicitProcessing computes the implicit level of the current character
// (BidiOrder.implicitProcessing).
func (o *bidiOrder) implicitProcessing() {
	bdx := o.myBdx
	newIS := int(o.impTab[o.ucbImpSta][o.ucbXType])
	special := newIS >> 4 // 4 high bits
	newIS &= 0x0F         // 4 low bits
	newIL := int(o.impTab[newIS][itil])
	newLevel := int8(int(o.ucbCurLev) + newIL)

	if special > 0 {
		switch special {
		case 1: // set conditional run to level 1
			level := int8(int(o.ucbCurLev) + 1)
			for i := o.ucbCondPos; i < o.ucbIx; i++ {
				oldLevel := bdx.PropertyMap[i]
				bdx.PropertyMap[i] = byte(level)
				o.symmetricSwap(i, int8(oldLevel), level)
			}
			o.ucbCondPos = -1
		case 2: // confirm the conditional run
			o.ucbCondPos = -1
		case 3: // L after R/AL + possible EN/AN
			if o.startL2EN >= 0 {
				o.addPoint(o.startL2EN, before, lrm)
			}
			o.startL2EN = -1 // not within previous if since could also be -2
			if bdx.insertPoints == nil || len(bdx.insertPoints) <= o.insertCnt {
				o.lastStrongRTL = -1
				break
			}
			for i := o.lastStrongRTL + 1; i < o.ucbIx; i++ {
				bdx.PropertyMap[i] = byte(newLevel)
				o.typeArray[i][origIdx] = abs8(o.typeArray[i][origIdx])
			}
			o.insertCnt = len(bdx.insertPoints)
			o.lastStrongRTL = -1
		case 4: // R/AL after possible relevant EN/AN
			if bdx.insertPoints != nil {
				bdx.insertPoints = setVectorSize(bdx.insertPoints, o.insertCnt)
			}
			o.startL2EN = -1
			o.lastStrongRTL = o.ucbIx
		case 5: // EN/AN after R/AL + possible cont
			o.ucbCondPos = -1
			if o.ucbXType == ubatAN && o.typeArray[o.ucbIx][origIdx] == ubatAN && !bdx.WinCompatible {
				if o.startL2EN == -1 { // if no relevant EN already found
					o.lastStrongRTL = o.afterAN(o.ucbIx) - 1
					break
				}
				if o.startL2EN >= 0 { // after EN, no AN
					o.addPoint(o.startL2EN, before, lrm)
					o.startL2EN = -2
				}
				o.addPoint(o.ucbIx, before, lrm)
				break
			}
			if o.startL2EN == -1 {
				o.startL2EN = o.ucbIx
			}
		case 6: // note location of latest R/AL
			o.lastStrongRTL = o.ucbIx
		case 7: // R followed by EN/AN followed by L
			i := o.beforeENAN(o.ucbIx - 1)
			if i < 0 { // Bidi-HCG
				break
			}
			o.addPoint(i, before, rlm) // add RLM before (Bidi-HCG)
			o.insertCnt = len(bdx.insertPoints)
		case 8: // L possibly followed by N then by AN
			pos := o.afterENAN(o.ucbIx)
			i := o.beforeENAN(o.ucbIx)
			if (pos >= o.icsSize || o.typeArray[pos][finalIdx] == ubatL || o.typeArray[pos][finalIdx] == ubatR) &&
				(i >= 0 && o.typeArray[i][finalIdx] == ubatL) {
				break
			}
			o.addPoint(pos-1, after, lrm) // add LRM after
			o.addPoint(i+1, before, lrm)  // add LRM before
		case 9: // L followed by N followed by R
			if bdx.insertPoints != nil {
				bdx.insertPoints = setVectorSize(bdx.insertPoints, o.insertCnt) // infirm inserts
			}
			o.ucbCondPos = -1 // confirm possible cont run as RTL
		case 10: // L followed by AN followed by L
			if bdx.insertPoints != nil {
				o.insertCnt = len(bdx.insertPoints) // confirm inserts
			}
		default:
			panic("bidi: invalid action number")
		}
	}

	sCond := o.impTab[newIS][itcond]
	if sCond == 0 {
		if o.ucbCondPos > -1 {
			for i := o.ucbCondPos; i < o.ucbIx; i++ {
				oldLevel := bdx.PropertyMap[i]
				bdx.PropertyMap[i] = byte(newLevel)
				o.symmetricSwap(i, int8(oldLevel), newLevel)
			}
			o.ucbCondPos = -1
		}
	} else if o.ucbCondPos == -1 {
		o.ucbCondPos = o.ucbIx
	}
	o.ucbImpSta = newIS
	if o.ucbXType == ubatB {
		o.ucbWTarget = 0
	} else {
		o.ucbWTarget = newLevel
	}
}

// getChTypeWB is BidiOrder.getChType(char, boolean).
func getChTypeWB(x uint16, wordBreak bool) int8 {
	if wordBreak && x == 0x0020 {
		return ubatS
	}
	return getChType(x)
}

// getChType returns the bidi class of a character (BidiOrder.getChType(char)).
func getChType(x uint16) int8 {
	if x == 0x000A || x == 0x000D || (x >= 0x001C && x <= 0x001E) || x == 0x0085 || x == 0x2029 {
		return ubatB
	}
	if x == 0x0009 || x == 0x000B || x == 0x001F {
		return ubatS
	}
	if (x >= 0x0041 && x <= 0x005A) ||
		(x >= 0x0061 && x <= 0x007A) ||
		(x >= 0x00C0 && x <= 0x00D6) ||
		(x >= 0x00D8 && x <= 0x00F6) ||
		(x >= 0x00F8 && x <= 0x058F) ||
		(x >= 0x0900 && x <= 0x10FF) ||
		x == 0x200E ||
		(x >= 0x20D0 && x <= 0x20FF) ||
		(x >= 0x2160 && x <= 0x2182) ||
		(x >= 0x3040 && x <= 0x9FFF) ||
		(x >= 0xF900 && x <= 0xFB17) ||
		x == 0xFE60 ||
		x == 0xFE6B ||
		x == 0xFF06 ||
		(x >= 0xFF20 && x <= 0xFF3A) ||
		(x >= 0xFF41 && x <= 0xFF5A) ||
		(x >= 0xFF60 && x <= 0xFFDF) {
		return ubatL
	}
	if (x >= 0x0591 && x <= 0x05FF) || x == 0x200F || (x >= 0xFB1D && x <= 0xFB4F) {
		return ubatR
	}
	if (x >= 0x0600 && x <= 0x065F) ||
		(x >= 0x066E && x <= 0x06EF) ||
		(x >= 0x06FA && x <= 0x08FF) ||
		(x >= 0xFB50 && x <= 0xFDFF) ||
		(x >= 0xFE70 && x <= 0xFEFC) {
		return ubatAL
	}
	if (x >= 0x0030 && x <= 0x0039) ||
		(x >= 0x00B2 && x <= 0x00B3) ||
		x == 0x00B9 ||
		(x >= 0x06F0 && x <= 0x06F9) ||
		x == 0x2070 ||
		(x >= 0x2074 && x <= 0x2079) ||
		(x >= 0x2080 && x <= 0x2089) ||
		(x >= 0x2460 && x <= 0x249B) ||
		x == 0x24EA ||
		(x >= 0xFF10 && x <= 0xFF19) {
		return ubatEN
	}
	if (x >= 0x0660 && x <= 0x0669) || (x >= 0x066B && x <= 0x066C) {
		return ubatAN
	}
	if (x >= 0x0023 && x <= 0x0025) ||
		(x >= 0x00A2 && x <= 0x00A5) ||
		(x >= 0x00B0 && x <= 0x00B1) ||
		x == 0x066A ||
		(x >= 0x09F2 && x <= 0x09F3) ||
		x == 0x0AF1 ||
		x == 0x0BF9 ||
		x == 0x0E3F ||
		x == 0x17DB ||
		(x >= 0x2030 && x <= 0x2034) ||
		(x >= 0x207A && x <= 0x207B) ||
		(x >= 0x208A && x <= 0x208B) ||
		(x >= 0x20A0 && x <= 0x20B1) ||
		x == 0x212E ||
		(x >= 0x2212 && x <= 0x2213) ||
		x == 0xFB29 ||
		x == 0xFE5F ||
		(x >= 0xFE62 && x <= 0xFE63) ||
		(x >= 0xFE69 && x <= 0xFE6A) ||
		(x >= 0xFF03 && x <= 0xFF05) ||
		x == 0xFF0B ||
		x == 0xFF0D ||
		(x >= 0xFFE0 && x <= 0xFFE1) ||
		(x >= 0xFFE5 && x <= 0xFFE6) {
		return ubatET
	}
	if x == 0x002B || x == 0x002D || x == 0x002F || x == 0xFF0F {
		return ubatES
	}
	if x == 0x002C || x == 0x002E || x == 0x003A || x == 0x00A0 || x == 0x060C ||
		x == 0xFE50 || x == 0xFE52 || x == 0xFE55 || x == 0xFF0C || x == 0xFF0E || x == 0xFF1A {
		return ubatCS
	}
	if x == 0x000C || x == 0x0020 || x == 0x1680 || x == 0x180E || (x >= 0x2000 && x <= 0x200A) ||
		x == 0x2028 || x == 0x202F || x == 0x205F || x == 0x3000 {
		return ubatW
	}
	if (x >= 0x202A && x <= 0x202E) || (x >= 0x206C && x <= 0x206F) {
		return ubatBD
	}
	return ubatN
}

// firstStrong returns the class of the first strong character of src (BidiOrder.firstStrong).
func firstStrong(src *BidiText) int {
	lim := src.Offset + src.Count
	for i := src.Offset; i < lim; i++ {
		t := getChType(src.Data[i])
		if t == ubatL {
			return ubatL
		}
		if t == ubatR || t == ubatAL {
			return ubatR
		}
	}
	return ubatN
}

// lastStrong returns the class of the last strong character of src (BidiOrder.lastStrong).
func lastStrong(src *BidiText) int {
	lim := src.Offset + src.Count
	for i := lim - 1; i >= src.Offset; i-- {
		t := getChType(src.Data[i])
		if t == ubatL {
			return ubatL
		}
		if t == ubatR || t == ubatAL {
			return ubatR
		}
	}
	return ubatN
}

// fillTypeArray computes the FINAL types of the source characters (BidiOrder.fillTypeArray).
func (o *bidiOrder) fillTypeArray() {
	isArabic := false
	delimState := 0 // 0 = before delim1, 1 = after delim1, 2 = got pair of delim2 after delim1
	ta := o.typeArray
	for i := 0; i < o.icsSize; i++ {
		c := o.icsBufferIn[i]
		cType := getChTypeWB(c, o.myBdx.WordBreak)
		ta[i][origIdx] = cType
		ta[i][finalIdx] = ubatN
		if o.delim1 != noDelim {
			if delimState == 0 {
				if c == o.delim1 {
					delimState = 1
				}
				cType = ubatB
			} else if delimState == 1 {
				if c == o.delim2 {
					if (i+1) < o.icsSize && o.icsBufferIn[i+1] == o.delim2 {
						delimState = 2
					} else {
						delimState = 0
						cType = ubatB
					}
				}
			} else { // delimState == 2
				delimState = 1
			}
		}
		if o.visToVis {
			continue
		}
		switch cType {
		case ubatB:
			isArabic = false
			ta[i][finalIdx] = ubatB
		case ubatS:
			ta[i][finalIdx] = ubatS
		case ubatL:
			isArabic = false
			ta[i][finalIdx] = ubatL
		case ubatR:
			isArabic = false
			ta[i][finalIdx] = ubatR
		case ubatAL:
			isArabic = true
			ta[i][finalIdx] = ubatR
		case ubatEN:
			var wType int8
			if isArabic {
				wType = ubatAN
			} else {
				wType = ubatEN
				if i >= 2 && ta[i-1][origIdx] == ubatES && ta[i-2][origIdx] == ubatEN {
					ta[i-1][finalIdx] = ubatEN
				}
				prev := i - 1
				for prev >= 0 && ta[prev][origIdx] == ubatET {
					ta[prev][finalIdx] = ubatEN
					prev--
				}
			}
			if i >= 2 && ta[i-1][origIdx] == ubatCS && ta[i-2][origIdx] == ubatEN {
				ta[i-1][finalIdx] = wType
			}
			ta[i][finalIdx] = wType
		case ubatAN:
			if i >= 2 && ta[i-1][origIdx] == ubatCS && ta[i-2][finalIdx] == ubatAN {
				ta[i-1][finalIdx] = ubatAN
			}
			ta[i][finalIdx] = ubatAN
		case ubatW:
			ta[i][finalIdx] = ubatW
		case ubatET:
			if i > 0 && ta[i-1][finalIdx] == ubatEN {
				ta[i][finalIdx] = ubatEN
			}
		case ubatNSM:
			// NSMs following a character of another type take that type. A leading NSM
			// keeps N. (Java comment: no requirement for other combinations.)
			if i <= 0 {
				break
			}
			ta[i][finalIdx] = ta[i-1][finalIdx]
		}
	}
}

// fillTypeArray2 is the same computation in the destination order, used for the
// second phase of implicit-to-implicit reordering (BidiOrder.fillTypeArray2).
func (o *bidiOrder) fillTypeArray2() {
	isArabic := false
	ta := o.typeArray
	d2s := o.myBdx.DstToSrcMap
	for i := 0; i < o.icsSize; i++ {
		k := d2s[i]
		cType := ta[k][origIdx] // ok even for ubatNSwap
		if o.delim1 != noDelim && ta[k][finalIdx] == ubatB {
			cType = ubatB
		} else {
			ta[k][finalIdx] = ubatN
		}
		switch cType {
		case ubatB:
			isArabic = false
			ta[k][finalIdx] = ubatB
		case ubatS:
			ta[k][finalIdx] = ubatS
		case ubatL:
			isArabic = false
			ta[k][finalIdx] = ubatL
		case ubatR:
			isArabic = false
			ta[k][finalIdx] = ubatR
		case ubatAL:
			isArabic = true
			ta[k][finalIdx] = ubatR
		case ubatEN:
			var wType int8
			if isArabic {
				wType = ubatAN
			} else {
				wType = ubatEN
				if i >= 2 && ta[d2s[i-1]][origIdx] == ubatES && ta[d2s[i-2]][origIdx] == ubatEN {
					ta[d2s[i-1]][finalIdx] = ubatEN
				}
				prev := i - 1
				for prev >= 0 && ta[d2s[prev]][origIdx] == ubatET {
					ta[d2s[prev]][finalIdx] = ubatEN
					prev--
				}
			}
			if i >= 2 && ta[d2s[i-1]][origIdx] == ubatCS && ta[d2s[i-2]][origIdx] == ubatEN {
				ta[d2s[i-1]][finalIdx] = wType
			}
			ta[k][finalIdx] = wType
		case ubatAN:
			if i >= 2 && ta[d2s[i-1]][origIdx] == ubatCS && ta[d2s[i-2]][origIdx] == ubatAN {
				ta[d2s[i-1]][finalIdx] = ubatAN
			}
			ta[k][finalIdx] = ubatAN
		case ubatW:
			ta[k][finalIdx] = ubatW
		case ubatET:
			if i > 0 && ta[d2s[i-1]][finalIdx] == ubatEN {
				ta[k][finalIdx] = ubatEN
			}
		case ubatNSM:
			// NSMs at a LTR/RTL boundary keep the type given in the first phase.
		}
	}
}

// baseLvl selects the implicit table and the base levels (BidiOrder.BaseLvl).
func (o *bidiOrder) baseLvl() {
	bdx := o.myBdx
	o.visToVis = false
	if o.icsOrientIn == orientationRTL {
		o.ucbBasLev = 1 // 0 = LTR, 1 = RTL
	} else {
		o.ucbBasLev = 0
	}
	if o.icsOrientOut == orientationRTL {
		o.ucbOutLev = 1
	} else {
		o.ucbOutLev = 0
	}
	o.ucbCurLev = o.ucbBasLev
	if o.ucbBasLev == 0 && o.ucbOutLev == 1 {
		o.ucbCurLev = 2
	}
	if o.icsTypeIn == typeVisual && o.icsTypeOut == typeVisual {
		o.visToVis = true
	}
	rtl := o.ucbBasLev == 1
	switch {
	case bdx.RoundTrip:
		if rtl {
			o.impTab = impTabRTLr[:]
		} else {
			o.impTab = impTabLTRr[:]
		}
	case bdx.WinCompatible:
		if o.insertMarkers {
			if rtl {
				o.impTab = impTabRTLm[:]
			} else {
				o.impTab = impTabLTRwm[:]
			}
			o.startL2EN = -1
			o.lastStrongRTL = -1
			if bdx.insertPoints != nil {
				bdx.insertPoints = setVectorSize(bdx.insertPoints, 0)
			}
		} else {
			if rtl {
				o.impTab = impTabRTL[:]
			} else {
				o.impTab = impTabLTRw[:]
			}
		}
	case o.insertMarkers:
		if rtl {
			o.impTab = impTabRTLm[:]
		} else {
			o.impTab = impTabLTRm[:]
		}
		o.startL2EN = -1
		o.lastStrongRTL = -1
		if bdx.insertPoints != nil {
			bdx.insertPoints = setVectorSize(bdx.insertPoints, 0)
		}
	default:
		if rtl {
			o.impTab = impTabRTL[:]
		} else {
			o.impTab = impTabLTR[:]
		}
	}
	o.insertCnt = 0
	o.removeCnt = 0
	o.ucbImpSta = 0
	o.ucbCondPos = -1
}

// pass2 builds the reordering map from the levels (BidiOrder.pass2).
func (o *bidiOrder) pass2() {
	bdx := o.myBdx
	var lowestLevel int8 = 100
	var highestLevel int8 = 0
	for i := 0; i < o.icsSize; i++ {
		if o.impToImpPhase != 2 {
			bdx.DstToSrcMap[i] = i
		}
		cur := int8(bdx.PropertyMap[i])
		if cur < lowestLevel {
			lowestLevel = cur
		}
		if cur > highestLevel {
			highestLevel = cur
		}
	}
	if o.reqImpToImp && o.impToImpOrient == 0 {
		return
	}
	if o.icsOrientOut == orientationRTL {
		lowestLevel = int8((int(lowestLevel) + 1) &^ 1)
	} else {
		lowestLevel |= 1
	}
	for workLevel := highestLevel; workLevel >= lowestLevel; workLevel-- {
		i := 0
		for i < o.icsSize {
			if int8(bdx.PropertyMap[i]) < workLevel {
				i++
				continue
			}
			flipFrom := i
			for i = flipFrom + 1; i < o.icsSize && int8(bdx.PropertyMap[i]) >= workLevel; i++ {
			}
			flipTo := i - 1
			invertInts(bdx.DstToSrcMap, flipFrom, flipTo)
		}
	}
}

// pass3 writes the output characters: numeral shapes and symmetric swapping (BidiOrder.pass3).
func (o *bidiOrder) pass3() {
	for i := 0; i < o.icsSize; i++ {
		logPos := o.myBdx.DstToSrcMap[i]
		xchar := o.icsBufferIn[logPos]
		xtype := o.typeArray[logPos][origIdx]
		if xtype == ubatEN {
			if o.icsNumFlag == numeralsNational ||
				(o.icsNumFlag == numeralsContextual && o.typeArray[logPos][finalIdx] == ubatAN) {
				xchar += 0x0660 - 0x0030
			}
		} else if xtype == ubatAN {
			if o.icsNumFlag == numeralsNominal {
				xchar -= 0x0660 - 0x0030
			}
		} else if xtype == ubatNSwap {
			xchar = ucqsymm(xchar)
		}
		o.icsBufferOut[i] = xchar
	}
}

// sameArray reports whether two slices share their first element (Java array identity).
func sameArray(a, b []uint16) bool {
	return len(a) > 0 && len(b) > 0 && &a[0] == &b[0]
}

// order is the port of BidiOrder.order(src, dst, bdx).
func (o *bidiOrder) order(src, dst *BidiText, bdx *BidiTransform) {
	if src.Count < 1 {
		if dst.Data == nil {
			dst.Data = []uint16{}
		}
		bdx.InpCount = 0
		bdx.OutCount = 0
		return
	}
	o.myBdx = bdx

	o.icsOrientIn = src.Flags.GetOrientation()
	o.icsOrientOut = dst.Flags.GetOrientation()
	if o.icsOrientIn == orientationContextLTR || o.icsOrientIn == orientationContextRTL {
		switch firstStrong(src) {
		case ubatL:
			o.icsOrientIn = orientationLTR
		case ubatR:
			o.icsOrientIn = orientationRTL
		case ubatN:
			if o.icsOrientIn == orientationContextRTL {
				o.icsOrientIn = orientationRTL
			} else {
				o.icsOrientIn = orientationLTR
			}
		}
	}
	if o.icsOrientOut == orientationContextLTR || o.icsOrientOut == orientationContextRTL {
		switch firstStrong(src) {
		case ubatL:
			if lastStrong(src) == ubatR {
				o.icsOrientOut = orientationRTL
			} else {
				o.icsOrientOut = orientationLTR
			}
		case ubatR:
			o.icsOrientOut = orientationRTL
		case ubatN:
			if o.icsOrientOut == orientationContextRTL {
				o.icsOrientOut = orientationRTL
			} else {
				o.icsOrientOut = orientationLTR
			}
		}
	}

	o.icsTypeIn = src.Flags.GetType()
	o.icsTypeOut = dst.Flags.GetType()
	o.reqImpToImp = o.icsTypeIn == typeImplicit && o.icsTypeOut == typeImplicit
	if bdx.ImpToImp && o.reqImpToImp && o.icsOrientIn != o.icsOrientOut {
		if o.icsOrientIn == orientationLTR {
			o.impToImpOrient = impLTR
		} else {
			o.impToImpOrient = impRTL
		}
	} else {
		o.impToImpPhase = 0 // Bidi-HCG
		o.impToImpOrient = 0
	}

	o.delim1 = noDelim // no delimiters unless given
	if len(bdx.Delimiters) > 0 {
		o.delim1 = bdx.Delimiters[0]
		if len(bdx.Delimiters) > 1 {
			o.delim2 = bdx.Delimiters[1]
		} else {
			o.delim2 = o.delim1
		}
	}

	o.insertMarkers = bdx.InsertMarkers
	o.icsBufferIn = make([]uint16, src.Count)
	if o.icsTypeIn == typeVisual && o.icsTypeOut == typeImplicit &&
		((o.insertMarkers && o.icsOrientIn == orientationRTL) || (!o.insertMarkers && o.icsOrientIn != o.icsOrientOut)) {
		o.invertInput = true
		ofs := src.Offset + src.Count - 1
		for k := 0; k < src.Count; k++ {
			o.icsBufferIn[k] = src.Data[ofs-k]
		}
		if o.insertMarkers {
			o.icsOrientIn = orientationLTR
		} else {
			o.icsOrientIn = o.icsOrientOut
		}
	} else {
		o.invertInput = false
		copy(o.icsBufferIn, src.Data[src.Offset:src.Offset+src.Count])
	}

	if o.insertMarkers {
		if o.icsTypeIn != typeVisual || o.icsTypeOut != typeImplicit || bdx.RemoveMarkers {
			o.insertMarkers = false
		} else if o.icsOrientOut == orientationRTL {
			o.icsOrientIn = orientationRTL
			o.icsOrientOut = orientationLTR
		}
	}

	o.icsSize = src.Count
	o.icsNumFlag = dst.Flags.GetNumerals()
	if o.reqImpToImp {
		o.icsSymmetric = false
	} else {
		o.icsSymmetric = dst.Flags.GetSwap() != src.Flags.GetSwap()
	}
	o.icsBufferOut = make([]uint16, src.Count)
	o.typeArray = make([][2]int8, src.Count)
	if bdx.PropertyMap == nil || len(bdx.PropertyMap) < src.Count {
		bdx.PropertyMap = make([]byte, src.Count)
	}
	if bdx.DstToSrcMap == nil || len(bdx.DstToSrcMap) < src.Count {
		bdx.DstToSrcMap = make([]int, src.Count)
	}

	if o.impToImpOrient > 0 {
		o.impToImpPhase = 1
		origOut := o.icsOrientOut
		o.icsOrientOut = orientationLTR
		o.icsSymmetric = src.Flags.GetSwap() == swapYes
		o.baseLvl()
		o.fillTypeArray()
		for o.ucbIx = 0; o.ucbIx < o.icsSize; o.ucbIx++ {
			o.ucbXType = int(o.typeArray[o.ucbIx][finalIdx])
			o.implicitProcessing()
			bdx.PropertyMap[o.ucbIx] = byte(o.ucbWTarget)
			if o.typeArray[o.ucbIx][origIdx] == ubatN && o.icsSymmetric && odd(int(o.ucbWTarget)) {
				o.typeArray[o.ucbIx][origIdx] = ubatNSwap
			}
		}
		o.ucbIx = o.icsSize
		o.ucbXType = ubatB
		o.implicitProcessing()
		o.pass2()
		o.icsOrientOut = origOut
		o.impToImpPhase = 2
		origIn := o.icsOrientIn
		if o.impToImpOrient == impLTR {
			o.icsOrientIn = orientationRTL
			invertInts(bdx.DstToSrcMap, 0, o.icsSize-1)
		} else {
			o.icsOrientIn = orientationLTR
		}
		o.icsSymmetric = dst.Flags.GetSwap() == swapYes
		o.baseLvl()
		o.fillTypeArray2()
		for o.ucbIx = 0; o.ucbIx < o.icsSize; o.ucbIx++ {
			ipos := bdx.DstToSrcMap[o.ucbIx]
			o.ucbXType = int(o.typeArray[ipos][finalIdx])
			o.implicitProcessing()
			bdx.PropertyMap[o.ucbIx] = byte(o.ucbWTarget)
			if abs8(o.typeArray[ipos][origIdx]) == ubatN && o.icsSymmetric && odd(int(o.ucbWTarget)) {
				o.typeArray[ipos][origIdx] = -o.typeArray[ipos][origIdx]
			}
		}
		o.ucbIx = o.icsSize
		o.ucbXType = ubatB
		o.implicitProcessing()
		o.pass2()
		o.icsOrientIn = origIn
		o.icsSymmetric = true
	} else {
		o.baseLvl()
		o.fillTypeArray()
		for o.ucbIx = 0; o.ucbIx < o.icsSize; o.ucbIx++ {
			o.ucbXType = int(o.typeArray[o.ucbIx][finalIdx])
			o.implicitProcessing()
			bdx.PropertyMap[o.ucbIx] = byte(o.ucbWTarget)
			if o.typeArray[o.ucbIx][origIdx] == ubatN && o.icsSymmetric && odd(int(o.ucbWTarget)) {
				o.typeArray[o.ucbIx][origIdx] = ubatNSwap
			}
		}
		o.ucbIx = o.icsSize
		o.ucbXType = ubatB
		o.implicitProcessing()
		o.pass2()
	}

	// Reordering is done; the characters are now mapped to their logical-to-visual positions.
	o.pass3()

	dstToSrcMapRequired := bdx.DstToSrcMapRequired || bdx.SrcToDstMapRequired
	if dstToSrcMapRequired && o.invertInput {
		for i := 0; i < src.Count; i++ {
			bdx.DstToSrcMap[i] = src.Count - bdx.DstToSrcMap[i] - 1
		}
	}

	if bdx.RemoveMarkers {
		for pos := 0; pos < src.Count; pos++ {
			c := o.icsBufferOut[pos]
			if c == lrm || c == rlm {
				o.removeCnt++
				continue
			}
			if o.removeCnt > 0 {
				o.icsBufferOut[pos-o.removeCnt] = o.icsBufferOut[pos]
				bdx.DstToSrcMap[pos-o.removeCnt] = bdx.DstToSrcMap[pos]
			}
		}
	}

	dst.Count = src.Count - o.removeCnt + o.insertCnt
	if dst.Data == nil {
		if dst.Offset == 0 && o.insertCnt == 0 {
			dst.Data = o.icsBufferOut
		} else {
			dst.Data = make([]uint16, dst.Offset+dst.Count)
		}
	}
	if dst.Offset+dst.Count > len(dst.Data) {
		temp := make([]uint16, dst.Offset+dst.Count)
		if dst.Offset > 0 {
			copy(temp, dst.Data[:dst.Offset])
		}
		dst.Data = temp
	}

	if o.insertCnt > 0 { // some LRMs to insert
		pts := bdx.insertPoints
		for i := 0; i < o.insertCnt; i++ {
			iPoint := pts[i]
			ipos := int(int32(iPoint >> 17))
			aft := int(iPoint & 0x10000)
			insert := uint16(iPoint)
			if o.ucbBasLev == 1 { // Bidi-HCG: map to destination positions (RTL destination)
				for pos := 0; pos < src.Count; pos++ {
					if ipos == bdx.DstToSrcMap[pos] {
						ipos = pos
						break
					}
				}
				pts[i] = int64(int32(ipos)<<17 + int32(aft) + int32(insert))
			}
		}
		// The RTL destination case needs sorted points; the Java code sorts the whole Vector.
		if o.ucbBasLev == 1 {
			sort.SliceStable(pts, func(a, b int) bool { return pts[a] < pts[b] })
		}
		var tempMap []int
		if dstToSrcMapRequired {
			tempMap = make([]int, dst.Count)
		}
		pos := 0
		for i := 0; i < o.insertCnt; i++ {
			iPoint := pts[i]
			ipos := int(int32(iPoint >> 17))
			aft := int(iPoint & 0x10000)
			insert := uint16(iPoint)
			ipos += aft >> 16 // add 1 for after
			copy(dst.Data[dst.Offset+pos+i:dst.Offset+ipos+i], o.icsBufferOut[pos:ipos])
			dst.Data[dst.Offset+ipos+i] = insert
			if dstToSrcMapRequired {
				copy(tempMap[pos+i:pos+i+(ipos-pos)], bdx.DstToSrcMap[pos:ipos])
				tempMap[ipos+i] = -1
			}
			pos = ipos
		}
		n := dst.Count - pos - o.insertCnt
		copy(dst.Data[dst.Offset+pos+o.insertCnt:][:n], o.icsBufferOut[pos:][:n])
		if dstToSrcMapRequired {
			copy(tempMap[pos+o.insertCnt:][:n], bdx.DstToSrcMap[pos:][:n])
			bdx.DstToSrcMap = tempMap
		}
	} else {
		if !sameArray(dst.Data, o.icsBufferOut) {
			copy(dst.Data[dst.Offset:dst.Offset+dst.Count], o.icsBufferOut[:dst.Count])
		}
	}

	if bdx.SrcToDstMapRequired {
		if bdx.SrcToDstMap == nil || len(bdx.SrcToDstMap) < src.Count {
			bdx.SrcToDstMap = make([]int, src.Count)
		}
		if o.removeCnt > 0 {
			for i := 0; i < src.Count; i++ {
				bdx.SrcToDstMap[i] = -1
			}
		}
		for i := 0; i < dst.Count; i++ {
			pos := bdx.DstToSrcMap[i]
			if pos >= 0 {
				bdx.SrcToDstMap[pos] = i
			}
		}
	}

	if bdx.PropertyMapRequired {
		for i := 0; i < src.Count; i++ {
			if o.typeArray[i][origIdx] != ubatNSM {
				bdx.PropertyMap[i] |= 0x80
			}
		}
		if o.invertInput {
			invertBytes(bdx.PropertyMap, 0, src.Count-1)
		}
	}

	bdx.InpCount = src.Count
	bdx.OutCount = dst.Count
}
