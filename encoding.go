package i400

import (
	"encoding/binary"
	"fmt"
	"unicode/utf16"

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
