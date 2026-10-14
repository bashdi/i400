// Package bidi is a Go port of the Bidi layout engine of JTOpen
// (com.ibm.as400.access, files BidiFlag, BidiFlagSet, ArabicOption,
// ArabicOptionSet, BidiShape, ...). It is ported as faithfully as possible:
// same algorithms, same constants, same behaviour, including the quirks.
//
// Text is handled as []uint16, one element per UTF-16 code unit, exactly like
// Java's char[]. Methods that change the Java array in place do the same in Go
// (the caller's slice is modified); methods that change the length return the
// new slice.
//
// Naming: Java method names are kept, capitalised for export. Java overloads
// that differ in argument type get a suffix (for example NewBidiFlagSetFromChars,
// NewBidiFlagSetFromString). Java IllegalArgumentException becomes ErrIllegalArgument.
//
// Mapping of the implemented classes (step 1 of 2):
//
//	BidiFlag.java
//	  BidiFlag.TYPE_IMPLICIT ..... TypeImplicit (and the other predefined flags, see flags.go)
//	  BidiFlag.value, .mask ...... unexported fields of BidiFlag
//
//	BidiFlagSet.java
//	  new BidiFlagSet()                           NewBidiFlagSet()
//	  new BidiFlagSet(BidiFlagSet)                NewBidiFlagSetFromSet(model)
//	  new BidiFlagSet(BidiFlag)                   NewBidiFlagSetFromFlag(flag)
//	  new BidiFlagSet(f1, f2 ... f5)              NewBidiFlagSetFromFlags(flags...) (error on conflict)
//	  new BidiFlagSet(char[])                     NewBidiFlagSetFromChars([]uint16)
//	  new BidiFlagSet(String)                     NewBidiFlagSetFromString(str) (error on bad syntax)
//	  equals(Object)                              (*BidiFlagSet).Equals(*BidiFlagSet)
//	  getType/getText/getOrientation/getSwap/getNumerals  GetType/GetText/GetOrientation/GetSwap/GetNumerals
//	  hashCode()                                  HashCode()
//	  setAllFlags(BidiFlagSet)                    (*BidiFlagSet).SetAllFlags(model)
//	  setAllFlags(String)                         (*BidiFlagSet).SetAllFlagsFromString(str) (error)
//	  static set2AllFlags(f1, f2, String)         Set2AllFlags(f1, f2, str) (error)
//	  setOneFlag(BidiFlag)                        (*BidiFlagSet).SetOneFlag(flag)
//	  toString() / static toString(int)           String() / bidiFlagSetString(value)
//	  parseBidiFlagSet(String)                    parseBidiFlagSet(str) (returns negative on error)
//
//	ArabicOption.java
//	  ArabicOption.LAMALEF_NEAR ... TASHKEEL_AUTO  LamAlefNear ... TashkeelAuto (see arabicoptions.go)
//
//	ArabicOptionSet.java
//	  new ArabicOptionSet()                       NewArabicOptionSet()
//	  new ArabicOptionSet(ArabicOptionSet)        NewArabicOptionSetFromSet(set)
//	  new ArabicOptionSet(ArabicOption)           NewArabicOptionSetFromOption(option)
//	  new ArabicOptionSet(o1 ... o4)              NewArabicOptionSetFromOptions(opts...) (error on conflict)
//	  new ArabicOptionSet(char[])                 NewArabicOptionSetFromChars([]uint16)
//	  equals(Object)                              (*ArabicOptionSet).Equals(*ArabicOptionSet)
//	  getLamAlefMode/getSeenMode/getYehHamzaMode/getTashkeelMode  GetLamAlefMode/GetSeenMode/GetYehHamzaMode/GetTashkeelMode
//	  hashCode(), setAllOptions(), setOneOption() HashCode(), SetAllOptions(), SetOneOption()
//
//	BidiShape.java
//	  BidiShape.Tail                              Tail
//	  shape(inAttr, outAttr, str, optionSet)      (*BidiShape).Shape(inAttr, outAttr, str, optionSet)
//	  shapingRoutine(inAttr, outAttr, str, rtl)   (*BidiShape).shapingRoutine (unexported)
//	  uba_getLink, Lamalef, specialChar, SeenChar ubaGetLink, lamalef, specialChar, seenChar
//	  Lam-alef handling methods                   handleLamAlef*, deshapeLamAlef* (same names, lower-case)
//	  Seen, Yeh Hamza, Tashkeel handling          the same Java names, lower-case; empty Java methods are no-ops
//	  static tables                               tables.go (generated from the Java arrays by gen/ShapeGolden.java)
//
// Mapping of the reordering and conversion classes (step 2a):
//
//	BidiOrder.java (order.go, tables in order_tables.go)
//	  BidiOrder.order(src, dst, bdx)              (*bidiOrder).order(src, dst, bdx)
//	  the Java fields ucb_*, ics_*, impTab, ...   fields of bidiOrder, same names in camel case
//	  pass2, pass3, fillTypeArray(2), ...         (*bidiOrder).pass2, pass3, fillTypeArray, fillTypeArray2
//	  implicitProcessing, BaseLvl                 implicitProcessing, baseLvl
//	  getChType(char[, boolean])                  getChType, getChTypeWB
//	  UCQSYMM                                     ucqsymm (UCQSPAC is not used by JTOpen and is not ported)
//	  Vector insertPoints of Long                 BidiTransform.insertPoints []int64; Long = int64 with the Java int arithmetic
//	  static tables                               notSpacing, symPairs, impTabLTR, impTabRTL, impTabLTRr, ...
//
//	BidiText.java (text.go)
//	  BidiText fields flags, data, offset, count  Flags, Data, Offset, Count (exported)
//	  new BidiText() / (flags) / (flags, char[]) / (flags, char[], off, len, cap)
//	                                              NewBidiText / NewBidiTextFromFlags / NewBidiTextFromData / NewBidiTextFromRange
//	  setCharsRef, toCharArray, equals           SetCharsRef, ToCharArray, Equals
//	  transform(BidiFlagSet)                      (*BidiText).TransformFlags
//	  transform(BidiTransform)                    (*BidiText).Transform
//	  toString() / new BidiText(flags, String)    not ported: strings are []uint16 (the caller converts)
//
//	BidiTransform.java (transform.go)
//	  the public option fields and output fields  same names, exported (Flags, ImpToImp, RoundTrip, ...,
//	                                              InpCount, OutCount, SrcToDstMap, DstToSrcMap, PropertyMap)
//	  delimiters (String)                         Delimiters []uint16 (empty = none)
//	  new BidiTransform()                         NewBidiTransform() (Java defaults)
//	  continuation, internalState                 not ported: the Java transform never reads them
//
//	BidiStringType.java (stringtype.go)
//	  DEFAULT, NONE, ST4..ST11                    StringTypeDefault, StringTypeNone, StringTypeST4..StringTypeST11
//
//	BidiConversionProperties.java (properties.go)
//	  the properties used by the conversion       Properties struct (value type; NoImplicitReordering is the inverse of impToImp_)
//	  copyOptionsTo, setNumeralShapingOnTransform copyOptionsTo, setNumeralShapingOnTransform (unexported)
//	  DefaultProperties()                         the new BidiConversionProperties()
//	  NUMERALS_*                                  NumeralShapingDefault ... NumeralShapingAny
//
//	AS400BidiTransform.java (as400transform.go)
//	  CCSID_TABLE                                 ccsidTable (generated)
//	  getStringType(ccsid)                        StringTypeForCCSID(ccsid)
//	  initFlagSet(stringType)                     initFlagSet
//	  FLAG_SET (shared per string type)           flagSetCache (shared on purpose, as in JTOpen)
//	  constructor, setAS400Ccsid, setAS400StringType, setJavaStringType,
//	  setBidiConversionProperties                 newAS400Transform, setAS400Ccsid, setAS400StringType,
//	                                              setJavaStringType, setBidiConversionProperties (unexported)
//	  toJavaLayout(char[]) / toAS400Layout(char[])  (*as400Transform).toJavaLayout / toAS400Layout (unexported)
//	  ST constants ST1..ST14                      ast1..ast14 (only ST4..ST11 appear in the table)
//	  bidiTransform(String)                       TransformString (layout.go)
//	  SQL_statement_reordering, meta_data_reordering  SQLStatementReordering, MetaDataReordering (layout.go)
//	  convertSQLToHostCCSID, convertDataFromHostCCSID ConvertSQLToHost, ConvertDataFromHost (layout.go)
//	  isBidiCcsid, getStringTypeX                 isBidiSystemCCSID, StringTypeForSystem (layout.go)
//	  isVisual                                    not ported (not used by the driver)
//
// Call sequences of the conversion path (ConvTableBidiMap.java). These are the entry points the
// driver uses after (ToLogical) or before (ToHost) the single-byte table mapping:
//
//	byteArrayToCharArray(bytes, props)   (driver: table decode, then)
//	  ToLogical(text, ccsid, props)
//	    if props.StringType == NONE: return text
//	    abt = new AS400BidiTransform(ccsid)
//	    abt.setBidiConversionProperties(props)
//	    abt.setJavaStringType(props.StringType)
//	    abt.setAS400StringType(getStringType(ccsid))
//	    return abt.toJavaLayout(text)
//
//	charArrayToByteArray(chars, props)   (driver: then table encode, one unit per character)
//	  ToHost(text, ccsid, props)
//	    abt = new AS400BidiTransform(ccsid)
//	    abt.setBidiConversionProperties(props)
//	    abt.setJavaStringType(props.StringType)
//	    abt.setAS400StringType(getStringType(ccsid))
//	    return abt.toAS400Layout(text)     (returns text for NONE; the result is the full data array)
//
// The Java side of the string type is props.StringType, so DEFAULT (0) selects the default flag
// set (the ST5 flags), not the flags of the CCSID. The IBM i side is always getStringType(ccsid).
// toJavaLayout and toAS400Layout skip the layout (return the text) when either string type is NONE
// or when both are equal; a CCSID without a string type has type 0, so with DEFAULT it is skipped too.
//
// Not ported (step 2): BidiConvert (not in the conversion path) and the ConvTable classes themselves.
// The Shape entry point that BidiText.transform calls is (*BidiShape).Shape.
//
// Verification: shape_test.go replays 4800 shaping vectors (testdata/shape_golden.txt.gz); order_test.go
// replays 12000 BidiText.transform vectors with random text, flags, options, delimiters and map requests
// (testdata/order_golden.txt.gz); transform_test.go replays 12160 ToLogical / ToHost vectors over all
// 19 CCSIDs of the conversion list, string types NONE and DEFAULT and 4..11, implicit reordering,
// numeric round trip and removal of marks on implicit to visual (testdata/transform_golden.txt.gz).
// The generators are gen/ShapeGolden.java and gen/OrderGolden.java. The OrderGolden "transform" mode
// first checks that the real ConvTableBidiMap path equals the call sequence above for every CCSID
// that has a table class.
package bidi
