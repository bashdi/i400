package bidi

// Port of JTOpen BidiShape.java: Arabic shaping and deshaping, lam-alef
// handling, Seen family handling, Yeh Hamza handling and tashkeel handling.
//
// All text is []uint16, one element per UTF-16 code unit, as in the Java char[].
// The Java algorithms index the arrays directly and mutate them in place; the
// Go port does the same, so the caller's slice is modified exactly as in Java.
// Methods that change the length (lam-alef resize buffer) return a new slice.

// Tail is the marker (ZERO WIDTH SPACE) that Seen-near shaping writes after a
// Seen-family character. Java: BidiShape.Tail
const Tail uint16 = 0x200B

// Link attribute bits (BidiShape.java):
//
//	bit 0: links to the right
//	bit 1: links to the left
//	bit 2: is irrelevant to linking
//	bit 4: Lam type (Unicode)
//	bit 5: Alef type (Unicode)
//	bit 8-15: offset to presentation shapes starting at U+FE70
const (
	linkR        = 1
	linkL        = 2
	irrelevant   = 4
	lamTypeBit   = 16
	alefTypeBit  = 32
	linkFieldBit = 3
)

// BidiShape performs the Arabic shaping of a buffer. Port of BidiShape.java.
// It has no state, so one instance may be shared; the Java class is documented
// as thread safe for the same reason.
type BidiShape struct{}

// Shape is the port of BidiShape.shape(inAttr, outAttr, str, optionSet).
// It shapes (or deshapes) str in place and returns the resulting buffer, which
// is a different slice when the lam-alef resize-buffer option changed the length.
// A nil optionSet means the default options.
func (b *BidiShape) Shape(inAttr, outAttr *BidiFlagSet, str []uint16, optionSet *ArabicOptionSet) []uint16 {
	bufLen := len(str)
	if bufLen == 0 {
		return str
	}

	if optionSet == nil {
		optionSet = NewArabicOptionSet()
	}

	inTextType := inAttr.GetType()
	outTextType := outAttr.GetType()
	rtl := outAttr.GetOrientation() == OrientationRTL
	lamAlefOpt := optionSet.GetLamAlefMode()
	seenOpt := optionSet.GetSeenMode()
	yehHamzaOpt := optionSet.GetYehHamzaMode()
	tashkeelOpt := optionSet.GetTashkeelMode()

	if inTextType == TypeVisual && outTextType == TypeImplicit { // Visual to Implicit
		// Seen deshaping
		if seenOpt == SeenNear || seenOpt == SeenAuto {
			deshapeSeenNear(str, bufLen)
		}

		// Yeh Hamza deshaping
		if yehHamzaOpt == YehHamzaTwoCellNear || yehHamzaOpt == YehHamzaAuto {
			deshapeYehHamzaTwoCellNear(str, bufLen)
		}

		// Tashkeel deshaping
		if tashkeelOpt == TashkeelKeep || tashkeelOpt == TashkeelAuto {
			// No processing is done
		} else if tashkeelOpt == TashkeelCustomizedAtBegin {
			customizeTashkeelAtBegin(str, bufLen)
		} else if tashkeelOpt == TashkeelCustomizedAtEnd {
			customizeTashkeelAtEnd(str, bufLen)
		}

		// Lam-alef deshaping
		if lamAlefOpt == LamAlefResizeBuffer {
			str = deshapeLamAlefWithResizeBuffer(str, bufLen)
			bufLen = len(str)
		} else if lamAlefOpt == LamAlefNear {
			deshapeLamAlefNear(str, bufLen)
		} else if lamAlefOpt == LamAlefAtBegin {
			deshapeLamAlefAtBegin(str, bufLen)
		} else if lamAlefOpt == LamAlefAtEnd {
			deshapeLamAlefAtEnd(str, bufLen)
		} else if lamAlefOpt == LamAlefAuto {
			deshapeLamAlefAuto(str, bufLen, rtl)
		}

		b.shapingRoutine(inAttr, outAttr, str, rtl)

	} else if outTextType == TypeVisual { // Implicit to Visual
		b.shapingRoutine(inAttr, outAttr, str, rtl)

		// Seen shaping
		if seenOpt == SeenNear || seenOpt == SeenAuto {
			shapeSeenNear(str, bufLen, rtl)
		}

		// Yeh Hamza shaping
		if yehHamzaOpt == YehHamzaTwoCellNear || yehHamzaOpt == YehHamzaAuto {
			shapeYehHamzaTwoCellNear(str, bufLen, rtl)
		}

		// Tashkeel shaping
		if tashkeelOpt == TashkeelKeep || tashkeelOpt == TashkeelAuto {
			// No processing is done
		} else if tashkeelOpt == TashkeelCustomizedAtBegin {
			customizeTashkeelAtBegin(str, bufLen)
		} else if tashkeelOpt == TashkeelCustomizedAtEnd {
			customizeTashkeelAtEnd(str, bufLen)
		}

		// Lam-alef handling
		if lamAlefOpt == LamAlefResizeBuffer {
			str = handleLamAlefWithResizeBuffer(str, bufLen, rtl)
			bufLen = len(str)
		} else if lamAlefOpt == LamAlefNear {
			handleLamAlefNear(str, bufLen, rtl)
		} else if lamAlefOpt == LamAlefAtBegin {
			// In case of LTR spaces are already at the absolute beginning of buffer
			if rtl {
				handleLamAlefAtBegin(str, bufLen)
			}
		} else if lamAlefOpt == LamAlefAtEnd {
			// In case of RTL spaces are already at the absolute end of buffer
			if !rtl {
				handleLamAlefAtEnd(str, bufLen)
			}
		} else if lamAlefOpt == LamAlefAuto {
			// No processing is done
		}
	}
	return str
}

// shapingRoutine is the port of BidiShape.shapingRoutine: it chooses the
// presentation form of every Arabic letter according to its link attributes.
// Java: synchronized void shapingRoutine(BidiFlagSet inAttr, BidiFlagSet outAttr, char str[], boolean rtl)
func (b *BidiShape) shapingRoutine(inAttr, outAttr *BidiFlagSet, str []uint16, rtl bool) {
	var currLink int
	lastLink := 0
	nextLink := 0
	prevLink := 0
	var lastPos, nx, ix, iEnd, nw, step, shape int
	var wLamalef uint16
	var flag int

	bufLen := len(str)
	if bufLen == 0 {
		return
	}

	// Initialize variables
	inTextType := inAttr.GetType()
	outTextType := outAttr.GetType()
	outTextShape := outAttr.GetText()

	if inTextType == TypeVisual && outTextType == TypeImplicit { // Visual to Implicit
		for idx := 0; idx < bufLen; idx++ {
			// Convert the characters from FE to 06 range
			if str[idx] >= 0xFE70 && str[idx] <= 0xFEF4 {
				newCharValue := int(str[idx]) - 0xFE70
				str[idx] = uint16(convertFEto06[newCharValue])
			}
		}
		return
	} else if outTextType == TypeVisual {
		// Arabic characters are processed in language order: for RTL output start at the
		// beginning of str, for LTR output start at the end.
		if rtl {
			ix = 0
			iEnd = len(str)
			step = +1
		} else {
			ix = len(str) - 1
			iEnd = -1
			step = -1
		}

		prevLink = 0
		lastLink = 0
		currLink = ubaGetLink(str[ix])
		lastPos = ix
		nx = -2
		for ix != iEnd {
			if (currLink & 0xFF00) > 0 { // more than one shape
				nw = ix + step

				for nx < 0 { // we need to know about the next char
					if nw == iEnd {
						nextLink = 0
						nx = 30000 // will stay so until end of pass
					} else {
						nextLink = ubaGetLink(str[nw])
						if (nextLink & irrelevant) == 0 {
							nx = nw
						} else {
							nw += step
						}
					}
				}
				if (currLink&alefTypeBit) > 0 && (lastLink&lamTypeBit) > 0 {
					wLamalef = lamalef(str[ix]) // from 0x065C-0x065F
					if wLamalef != 0 {
						if rtl {
							str[lastPos] = wLamalef
							for h := ix; h < len(str)-1; h++ {
								str[h] = str[h+1]
							}
							str[len(str)-1] = 0x0020
							ix = lastPos
						} else { // LTR device: drop the Lam
							str[lastPos] = wLamalef
							for h := ix; h > 0; h-- {
								str[h] = str[h-1]
							}
							str[0] = 0x0020
							ix = lastPos
						}
					}

					lastLink = prevLink
					currLink = ubaGetLink(wLamalef)
					nx = -2 // force recompute of nextLink
				}

				// Get the proper shape according to link ability of neighbours and of the
				// character; depends on the order of the shapes (isolated, initial, middle,
				// final) in the compatibility area.
				flag = specialChar(str[ix])
				if outTextShape == TextInitial {
					if flag == 0 {
						shape = 2
					} else {
						shape = 0
					}
				} else if outTextShape == TextMiddle {
					if flag == 0 {
						shape = 3
					} else {
						shape = 1
					}
				} else if outTextShape == TextFinal {
					if flag == 0 {
						shape = 1
					} else {
						shape = 1
					}
				} else if outTextShape == TextIsolated {
					shape = 0
				} else {
					shape = int(shapeTable[nextLink&(linkR+linkL)][lastLink&(linkR+linkL)][currLink&(linkR+linkL)])
				}
				str[ix] = uint16(0xFE70 + (currLink >> 8) + shape)
			}
			// Move one notch forward
			if (currLink & irrelevant) == 0 {
				prevLink = lastLink
				lastLink = currLink
				lastPos = ix
			}
			// Tashkeel characters
			if (currLink&irrelevant) > 0 && 0 <= int(str[ix])-0x064B && int(str[ix])-0x064B < len(irrelevantPos) {
				charidx := int(str[ix]) - 0x064B
				myShape := 0
				next := nextLink & (linkR + linkL)
				last := lastLink & (linkR + linkL)
				if (last == 3 && next == 1) || (last == 3 && next == 3) {
					myShape = 1
				}
				if (nextLink&alefTypeBit) > 0 && (lastLink&lamTypeBit) > 0 {
					myShape = 0
				}
				if str[ix] == 0x064C || str[ix] == 0x064D { // Wawdoma, Kasrten
					myShape = 0
				}
				str[ix] = uint16(0xFE70 + int(irrelevantPos[charidx]) + myShape)
			}
			ix += step
			if ix == nx {
				currLink = nextLink
				nx = -2
			} else {
				if ix != iEnd {
					currLink = ubaGetLink(str[ix])
				}
			}
		}
	}
}

// ubaGetLink is the port of uba_getLink: the link attributes of a character.
func ubaGetLink(x uint16) int {
	if x >= 0x0622 && x <= 0x06D3 { // 06 range
		return int(link06[int(x)-0x0622])
	} else if x == 0x200D { // ZWJ
		return 3
	} else if x >= 0x206D && x <= 0x206F { // alternate formatting
		return 4
	} else if x >= 0xFE70 && x <= 0xFEFC { // FE range
		return int(linkFE[int(x)-0xFE70])
	}
	return 0
}

// lamalef is the port of Lamalef: the lam-alef ligature (U+065C..U+065F) for an
// alef, or 0 when x is not an alef.
func lamalef(x uint16) uint16 {
	switch x {
	case 0x0622:
		return 0x065C
	case 0x0623:
		return 0x065D
	case 0x0625:
		return 0x065E
	case 0x0627:
		return 0x065F
	}
	return 0
}

// specialChar is the port of specialChar: 1 for characters that do not link to the
// left (hamza, alef, dal-like letters ...), 0 otherwise.
func specialChar(ch uint16) int {
	// hamza
	if (ch >= 0x0621 && ch < 0x0626) || (ch == 0x0627) ||
		(ch > 0x062e && ch < 0x0633) ||
		(ch > 0x0647 && ch < 0x064a) || ch == 0x0629 {
		return 1
	}
	return 0
}

// seenChar is the port of SeenChar: true for the Seen family (Seen, Sheen, Sad, Dad)
// presentation forms.
func seenChar(ch uint16) bool {
	switch ch {
	case 0xFEB1, 0xFEB2, 0xFEB5, 0xFEB6, 0xFEB9, 0xFEBA, 0xFEBD, 0xFEBE:
		return true
	}
	return false
}

// ---------------------------------------------------------------- Lam-Alef

// handleLamAlefWithResizeBuffer shrinks the buffer by the number of lam-alef
// occurrences; the buffer comes with a space at the end (RTL) or the beginning
// (LTR) for each lam-alef, which is removed. Port of handleLamAlefWithResizeBuffer.
func handleLamAlefWithResizeBuffer(buffer []uint16, length int, rtl bool) []uint16 {
	lamAlefCount := 0
	for counter := 0; counter < length; counter++ {
		if buffer[counter] >= 0xFEF5 && buffer[counter] <= 0xFEFC {
			lamAlefCount++
		}
	}
	if lamAlefCount == 0 {
		return buffer
	}
	compressed := make([]uint16, length-lamAlefCount)
	if rtl { // remove the spaces at the end of the buffer
		copy(compressed, buffer[0:len(buffer)-lamAlefCount])
	} else { // remove the spaces at the beginning of the buffer
		copy(compressed, buffer[lamAlefCount:len(buffer)])
	}
	return compressed
}

// deshapeLamAlefWithResizeBuffer expands each lam-alef presentation form into
// the lam and the alef letter. Port of deshapeLamAlefWithResizeBuffer.
func deshapeLamAlefWithResizeBuffer(buffer []uint16, length int) []uint16 {
	lamAlefCount := 0
	for i := 0; i < length; i++ {
		if buffer[i] >= 0xFEF5 && buffer[i] <= 0xFEFC {
			lamAlefCount++
		}
	}
	if lamAlefCount == 0 {
		return buffer
	}
	expanded := make([]uint16, length+lamAlefCount)
	oldCounter := 0
	newCounter := 0
	for oldCounter < length {
		if buffer[oldCounter] >= 0xFEF5 && buffer[oldCounter] <= 0xFEFC {
			chAlefType := alefType[int(buffer[oldCounter])-0xFEF5]
			expanded[newCounter] = 0x0644
			newCounter++
			expanded[newCounter] = chAlefType
		} else {
			expanded[newCounter] = buffer[oldCounter]
		}
		newCounter++
		oldCounter++
	}
	return expanded
}

// handleLamAlefNear moves the space following (RTL) or preceding (LTR) a lam-alef
// so that the lam-alef is shaped next to its neighbour. Port of handleLamAlefNear.
func handleLamAlefNear(buffer []uint16, length int, rtl bool) {
	count := 0
	for count < length {
		if buffer[count] >= 0xFEF5 && buffer[count] <= 0xFEFC && buffer[length-1] == 0x0020 && rtl {
			for tempIdx := length - 1; tempIdx > count+1; tempIdx-- {
				buffer[tempIdx] = buffer[tempIdx-1]
			}
			if count < length-1 {
				buffer[count+1] = 0x0020
			}
		} else if buffer[count] >= 0xFEF5 && buffer[count] <= 0xFEFC && buffer[0] == 0x0020 && !rtl {
			for tempIdx := 0; tempIdx < count-1; tempIdx++ {
				buffer[tempIdx] = buffer[tempIdx+1]
			}
			if count != 0 {
				buffer[count-1] = 0x0020
			}
		}
		count++
	}
}

// deshapeLamAlefNear is the port of deshapeLamAlefNear.
func deshapeLamAlefNear(buffer []uint16, length int) {
	count := 1
	for count < length {
		if buffer[count] >= 0xFEF5 && buffer[count] <= 0xFEFC &&
			(count != length-1 && buffer[count+1] == 0x0020) {
			chAlefType := alefType[int(buffer[count])-0xFEF5]
			buffer[count+1] = chAlefType
			buffer[count] = 0x0644
		}
		count++
	}
}

// handleLamAlefAtBegin moves the spaces to the beginning of the buffer, one per
// lam-alef. Port of handleLamAlefAtBegin.
func handleLamAlefAtBegin(buffer []uint16, length int) {
	count := 0
	lamAlefOccurences := 0
	for count < length {
		if buffer[count] >= 0xFEF5 && buffer[count] <= 0xFEFC && buffer[length-1] == 0x0020 {
			for tempIdx := length - 1; tempIdx > lamAlefOccurences; tempIdx-- {
				buffer[tempIdx] = buffer[tempIdx-1]
			}
			buffer[lamAlefOccurences] = 0x0020
			lamAlefOccurences++
			count++
		}
		count++
	}
}

// deshapeLamAlefAtBegin is the port of deshapeLamAlefAtBegin.
func deshapeLamAlefAtBegin(buffer []uint16, length int) {
	count := 0
	for count < length {
		if buffer[count] >= 0xFEF5 && buffer[count] <= 0xFEFC && buffer[0] == 0x0020 {
			chAlefType := alefType[int(buffer[count])-0xFEF5]
			for tempIdx := 0; tempIdx < count-1; tempIdx++ {
				buffer[tempIdx] = buffer[tempIdx+1]
			}
			buffer[count-1] = 0x0644
			buffer[count] = chAlefType
		}
		count++
	}
}

// handleLamAlefAtEnd moves the spaces to the end of the buffer, one per lam-alef.
// Port of handleLamAlefAtEnd.
func handleLamAlefAtEnd(buffer []uint16, length int) {
	count := 0
	lamAlefOccurences := 0
	for count < length {
		if buffer[count] >= 0xFEF5 && buffer[count] <= 0xFEFC && buffer[0] == 0x0020 {
			for tempIdx := 0; tempIdx < length-lamAlefOccurences-1; tempIdx++ {
				buffer[tempIdx] = buffer[tempIdx+1]
			}
			buffer[length-lamAlefOccurences-1] = 0x0020
			lamAlefOccurences++
		}
		count++
	}
}

// deshapeLamAlefAtEnd is the port of deshapeLamAlefAtEnd.
func deshapeLamAlefAtEnd(buffer []uint16, length int) {
	count := 0
	for count < length {
		if buffer[count] >= 0xFEF5 && buffer[count] <= 0xFEFC && buffer[length-1] == 0x0020 {
			chAlefType := alefType[int(buffer[count])-0xFEF5]
			for tempIdx := length - 1; tempIdx > count+1; tempIdx-- {
				buffer[tempIdx] = buffer[tempIdx-1]
			}
			buffer[count] = 0x0644
			buffer[count+1] = chAlefType
		}
		count++
	}
}

// handleLamAlefAuto is the port of the empty handleLamAlefAuto (no processing).
func handleLamAlefAuto(buffer []uint16, length int, rtl bool) {}

// deshapeLamAlefAuto is the port of deshapeLamAlefAuto.
func deshapeLamAlefAuto(buffer []uint16, length int, rtl bool) {
	count := 0
	for count < length {
		if buffer[count] >= 0xFEF5 && buffer[count] <= 0xFEFC && buffer[length-1] == 0x0020 && rtl {
			chAlefType := alefType[int(buffer[count])-0xFEF5]
			for h := length - 1; h > count; h-- {
				buffer[h] = buffer[h-1]
			}
			buffer[count+1] = chAlefType
			buffer[count] = 0x0644
		} else if buffer[count] >= 0xFEF5 && buffer[count] <= 0xFEFC && buffer[0] == 0x0020 && !rtl {
			chAlefType := alefType[int(buffer[count])-0xFEF5]
			for h := 0; h < count; h++ {
				buffer[h] = buffer[h+1]
			}
			buffer[count] = chAlefType
			buffer[count-1] = 0x0644
		}
		count++
	}
}

// ---------------------------------------------------------------- Seen

// shapeSeenNear marks the space next to a Seen-family character with the Tail
// marker. Port of shapeSeenNear.
func shapeSeenNear(buffer []uint16, length int, rtl bool) {
	for idx := 0; idx < length; idx++ {
		if rtl {
			if seenChar(buffer[idx]) && (idx+1 < length && buffer[idx+1] == 0x0020) {
				buffer[idx+1] = Tail
			}
		} else {
			if seenChar(buffer[idx]) && (idx-1 >= 0 && buffer[idx-1] == 0x0020) {
				buffer[idx-1] = Tail
			}
		}
	}
}

// deshapeSeenNear turns the Tail markers back into spaces. Port of deshapeSeenNear.
func deshapeSeenNear(buffer []uint16, length int) {
	for idx := 0; idx < length; idx++ {
		if buffer[idx] == Tail {
			buffer[idx] = 0x0020
		}
	}
}

// The following Seen modes are empty in JTOpen (not implemented); ported as no-ops.
func shapeSeenAtBegin(buffer []uint16, length int, rtl bool)   {}
func deshapeSeenAtBegin(buffer []uint16, length int, rtl bool) {}
func shapeSeenAtEnd(buffer []uint16, length int, rtl bool)     {}
func deshapeSeenAtEnd(buffer []uint16, length int, rtl bool)   {}
func handleSeenAuto(buffer []uint16, length int, rtl bool)     {}
func deshapeSeenAuto(buffer []uint16, length int, rtl bool)    {}

// ---------------------------------------------------------------- Yeh Hamza

// shapeYehHamzaTwoCellNear replaces Yeh-Hamza isolated/final forms by the
// two-cell form (Yeh followed or preceded by a hamza in the space cell).
// Port of shapeYehHamzaTwoCellNear.
func shapeYehHamzaTwoCellNear(buffer []uint16, length int, rtl bool) {
	for counter := 0; counter < length; counter++ {
		if rtl {
			if buffer[counter] == 0xfe8a {
				buffer[counter] = 0xfef0 // Yeh
				if counter+1 < length && buffer[counter+1] == 0x0020 {
					buffer[counter+1] = 0xfe80 // hamza
				}
			}
			if buffer[counter] == 0xfe89 {
				buffer[counter] = 0xfeef // Yeh
				if counter+1 < length && buffer[counter+1] == 0x0020 {
					buffer[counter+1] = 0xfe80 // hamza
				}
			}
		} else {
			if buffer[counter] == 0xfe8a {
				buffer[counter] = 0xfef0 // Yeh
				if counter-1 >= 0 && buffer[counter-1] == 0x0020 {
					buffer[counter-1] = 0xfe80 // hamza
				}
			}
			if buffer[counter] == 0xfe89 {
				buffer[counter] = 0xfeef // Yeh
				if counter-1 >= 0 && buffer[counter-1] == 0x0020 {
					buffer[counter-1] = 0xfe80 // hamza
				}
			}
		}
	}
}

// deshapeYehHamzaTwoCellNear is the port of deshapeYehHamzaTwoCellNear.
func deshapeYehHamzaTwoCellNear(buffer []uint16, length int) {
	for counter := 0; counter < length; counter++ {
		if counter+1 < length && buffer[counter] == 0xfef0 && buffer[counter+1] == 0xfe80 {
			buffer[counter] = 0xfe8a // Yeh-hamza
			buffer[counter+1] = 0x0020
		}
		if counter+1 < length && buffer[counter] == 0xfeef && buffer[counter+1] == 0xfe80 {
			buffer[counter] = 0xfe89 // Yeh-hamza
			buffer[counter+1] = 0x0020
		}
	}
}

// The following Yeh Hamza modes are empty in JTOpen; ported as no-ops.
func shapeYehHamzaAtBegin(buffer []uint16, length int, rtl bool) {}
func deshapeYehHamzaAtBegin(buffer []uint16, length int)         {}
func shapeYehHamzaAtEnd(buffer []uint16, length int, rtl bool)   {}
func deshapeYehHamzaAtEnd(buffer []uint16, length int)           {}

// shapeYehHamzaAuto is the port of the empty shapeYehHamzaAuto.
func shapeYehHamzaAuto(buffer []uint16, length int, rtl bool) {}

// deshapeYehHamzaAuto is the port of the empty deshapeYehHamzaAuto.
func deshapeYehHamzaAuto(buffer []uint16, length int) {}

// ---------------------------------------------------------------- Tashkeel

// customizeTashkeelZeroWidth is the port of the empty customizeTashkeelZeroWidth.
func customizeTashkeelZeroWidth(buffer []uint16, length int) {}

// customizeTashkeelWithWidth is the port of the empty customizeTashkeelWithWidth.
func customizeTashkeelWithWidth(buffer []uint16, length int) {}

// isTashkeelForm reports whether a presentation form is one of the tashkeel
// shapes handled by the customized tashkeel modes.
func isTashkeelForm(c uint16) bool {
	return (c >= 0xFE70 && c <= 0xFE72) ||
		c == 0xFE74 ||
		(c >= 0xFE76 && c <= 0xFE7B) ||
		(c >= 0xFE7E && c <= 0xFE7F)
}

// customizeTashkeelAtBegin moves each tashkeel to the beginning of the buffer,
// leaving a space in its place. Port of customizeTashkeelAtBegin.
func customizeTashkeelAtBegin(buffer []uint16, length int) {
	count := 0
	tashkeelOcuurences := 0
	for count < length {
		if isTashkeelForm(buffer[count]) {
			for idx := count; idx > tashkeelOcuurences; idx-- {
				buffer[idx] = buffer[idx-1]
			}
			buffer[tashkeelOcuurences] = 0x0020
			tashkeelOcuurences++
		}
		count++
	}
}

// customizeTashkeelAtEnd moves each tashkeel to the end of the buffer, leaving a
// space in its place. Port of customizeTashkeelAtEnd.
func customizeTashkeelAtEnd(buffer []uint16, length int) {
	count := 0
	tashkeelOcuurences := 0
	for count < length {
		if isTashkeelForm(buffer[count]) {
			for idx := count; idx < length-tashkeelOcuurences-1; idx++ {
				buffer[idx] = buffer[idx+1]
			}
			buffer[length-tashkeelOcuurences-1] = 0x0020
			tashkeelOcuurences++
		}
		count++
	}
}

// handleTashkeelAuto is the port of the empty handleTashkeelAuto.
func handleTashkeelAuto(buffer []uint16, length int) {}
