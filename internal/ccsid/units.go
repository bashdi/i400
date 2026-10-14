package ccsid

import "fmt"

// ErrNotSingleByte is returned by DecodeUnits and EncodeUnits for CCSIDs whose
// table is not a single-byte table.
var ErrNotSingleByte = fmt.Errorf("ccsid: not a single-byte CCSID")

// DecodeUnits maps single-byte data to UTF-16 code units, one unit per byte.
// It is the table step of JTOpen's ConvTable.byteArrayToCharArray, before any
// bidi layout is applied.
func DecodeUnits(data []byte, ccsid int) ([]uint16, error) {
	c, err := Lookup(ccsid)
	if err != nil {
		return nil, err
	}
	sb, ok := c.(*singleByte)
	if !ok {
		return nil, ErrNotSingleByte
	}
	out := make([]uint16, len(data))
	for i, b := range data {
		out[i] = sb.toUnicode[b]
	}
	return out, nil
}

// EncodeUnits maps UTF-16 code units to single-byte data, one byte per unit.
// It is the table step of JTOpen's ConvTable.stringToByteArray, after any
// bidi layout is applied.
func EncodeUnits(units []uint16, ccsid int) ([]byte, error) {
	c, err := Lookup(ccsid)
	if err != nil {
		return nil, err
	}
	sb, ok := c.(*singleByte)
	if !ok {
		return nil, ErrNotSingleByte
	}
	out := make([]byte, len(units))
	for i, u := range units {
		out[i] = sb.fromUni[u]
	}
	return out, nil
}
