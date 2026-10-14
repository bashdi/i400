package i400

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// errInvalidNumericText marks text that is not a number at all (empty,
// whitespace-only, junk such as "abc", "1.2.3" or "--1"). Encoders convert it
// into a parameter error that names the target SQL type; it is never coerced to 0.
var errInvalidNumericText = errors.New("not a valid number")

// maxDecimalExponent bounds the decimal exponent accepted in numeric text.
// Anything beyond it is either a truncation to zero or an out-of-range value,
// so there is no need to materialise the digits.
const maxDecimalExponent = 100000

// numericParameterError converts a numeric parse/convert failure for value into
// a parameter error naming typeName (for example "INTEGER" or "DECIMAL").
func numericParameterError(value driver.Value, typeName string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, errInvalidNumericText) || errors.Is(err, strconv.ErrSyntax) {
		text, _ := parameterFieldValueString(value)
		return fmt.Errorf("parameter value %q is not a valid number for %s", text, typeName)
	}
	if errors.Is(err, strconv.ErrRange) {
		text, _ := parameterFieldValueString(value)
		return fmt.Errorf("parameter value %q out of range for %s", text, typeName)
	}
	return err
}

// decimalTextDigits converts decimal text (optional sign, digits, optional
// fraction, optional exponent) into the digit string used for a packed or
// zoned decimal field with the given scale. It returns the digits (integer
// digits followed by exactly scale fraction digits, leading integer zeros
// removed) and the sign.
//
// Like JTOpen's SQLDecimal.set (which uses truncateScale and BigDecimal
// setScale(ROUND_DOWN)), fraction digits beyond the scale are truncated, not
// rounded and not reported as errors. Integer-digit overflow is reported by the
// caller against the field precision; it is never truncated here.
func decimalTextDigits(text string, scale int) (string, bool, error) {
	if scale < 0 {
		scale = 0
	}
	s := strings.TrimSpace(text)
	negative := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-'
		s = s[1:]
	}

	exp := 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		body := s[i+1:]
		s = s[:i]
		sign := 1
		if body != "" && (body[0] == '+' || body[0] == '-') {
			if body[0] == '-' {
				sign = -1
			}
			body = body[1:]
		}
		if body == "" || !isASCIIDigits(body) {
			return "", false, errInvalidNumericText
		}
		if len(body) > 9 {
			exp = sign * (maxDecimalExponent + 1)
		} else {
			e, err := strconv.Atoi(body)
			if err != nil {
				return "", false, errInvalidNumericText
			}
			exp = sign * e
		}
	}

	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
	}
	if (intPart == "" && fracPart == "") || !isASCIIDigits(intPart) || !isASCIIDigits(fracPart) {
		return "", false, errInvalidNumericText
	}

	digits := intPart + fracPart
	point := len(intPart) + exp // position of the decimal point within digits
	if strings.Trim(digits, "0") == "" || point < -maxDecimalExponent {
		// Zero, or a magnitude too small to survive truncation: the value is 0.
		digits, point = "", 0
	} else if point > maxDecimalExponent {
		return "", false, fmt.Errorf("decimal value out of range")
	}

	var intDigits, fracDigits string
	switch {
	case point <= 0:
		fracDigits = strings.Repeat("0", -point) + digits
	case point >= len(digits):
		intDigits = digits + strings.Repeat("0", point-len(digits))
	default:
		intDigits, fracDigits = digits[:point], digits[point:]
	}

	// Truncate (never round) to the field scale, then pad to exactly scale digits.
	if len(fracDigits) > scale {
		fracDigits = fracDigits[:scale]
	} else {
		fracDigits += strings.Repeat("0", scale-len(fracDigits))
	}

	integerPart := strings.TrimLeft(intDigits, "0")
	if strings.Trim(integerPart+fracDigits, "0") == "" {
		// Truncation may leave a zero value; a zero is never negative.
		negative = false
	}
	if integerPart == "" {
		if fracDigits == "" {
			return "0", negative, nil
		}
		return fracDigits, negative, nil
	}
	return integerPart + fracDigits, negative, nil
}
