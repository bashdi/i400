package i400

import (
	"encoding/binary"
	"fmt"
	"unicode/utf16"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
)

func EncodeUTF16BE(text string) []byte {
	runes := utf16.Encode([]rune(text))
	encoded := make([]byte, len(runes)*2)
	for i, codeUnit := range runes {
		binary.BigEndian.PutUint16(encoded[i*2:], codeUnit)
	}
	return encoded
}

func DecodeUTF16BE(data []byte) (string, error) {
	if len(data)%2 != 0 {
		return "", fmt.Errorf("utf16be data length must be even: %d", len(data))
	}
	codeUnits := make([]uint16, len(data)/2)
	for i := range codeUnits {
		codeUnits[i] = binary.BigEndian.Uint16(data[i*2:])
	}
	return string(utf16.Decode(codeUnits)), nil
}

func EncodeEBCDIC37(text string) ([]byte, error) {
	encoded, err := charmap.CodePage037.NewEncoder().Bytes([]byte(text))
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func encodingForCCSID(ccsid int) (encoding.Encoding, bool) {
	switch ccsid {
	case 0, 37, 273, 277, 278, 280, 284, 285, 297, 500, 65535:
		return charmap.CodePage037, true
	case 437:
		return charmap.CodePage437, true
	case 819:
		return charmap.ISO8859_1, true
	case 850:
		return charmap.CodePage850, true
	case 858:
		return charmap.CodePage858, true
	case 912:
		return charmap.ISO8859_2, true
	case 1026:
		return charmap.CodePage037, true
	case 1141:
		return charmap.CodePage1140, true
	case 1252:
		return charmap.Windows1252, true
	case 930, 933, 935, 937, 939:
		// x/text does not provide IBM's mixed single-/double-byte tables.
		// The invariant EBCDIC single-byte portion is still usable for
		// protocol text that contains no national DBCS characters.
		return charmap.CodePage037, true
	case 1047:
		return charmap.CodePage1047, true
	case 1140:
		return charmap.CodePage1140, true
	case 1200, 13488, 1208:
		return nil, true
	default:
		return nil, false
	}
}

func encodeCCSID(text string, ccsid int) ([]byte, error) {
	switch ccsid {
	case 1200, 13488:
		return EncodeUTF16BE(text), nil
	case 1208:
		return []byte(text), nil
	}
	codec, ok := encodingForCCSID(ccsid)
	if !ok {
		return nil, fmt.Errorf("%w: unsupported CCSID %d", ErrUnsupported, ccsid)
	}
	return codec.NewEncoder().Bytes([]byte(text))
}

func decodeCCSID(data []byte, ccsid int) (string, error) {
	switch ccsid {
	case 1200, 13488:
		return DecodeUTF16BE(data)
	case 1208:
		return string(data), nil
	}
	codec, ok := encodingForCCSID(ccsid)
	if !ok {
		return "", fmt.Errorf("%w: unsupported CCSID %d", ErrUnsupported, ccsid)
	}
	decoded, err := codec.NewDecoder().Bytes(data)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func DecodeEBCDIC37(data []byte) (string, error) {
	decoded, err := charmap.CodePage037.NewDecoder().Bytes(data)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func EncodeEBCDIC37BlankPad(text string, size int) ([]byte, error) {
	encoded, err := EncodeEBCDIC37(text)
	if err != nil {
		return nil, err
	}
	result := make([]byte, size)
	for i := range result {
		result[i] = 0x40
	}
	copy(result, encoded)
	return result, nil
}

func EncodeUTF16BEBlankPad(text string, size int) []byte {
	if size <= 0 {
		return nil
	}
	encoded := EncodeUTF16BE(text)
	result := make([]byte, size)
	for i := range result {
		result[i] = 0x00
	}
	for i := 0; i < size/2; i++ {
		binary.BigEndian.PutUint16(result[i*2:], uint16(' '))
	}
	copy(result, encoded)
	return result
}

func EncodeUTF16BEString(text string) []byte {
	return EncodeUTF16BE(text)
}
