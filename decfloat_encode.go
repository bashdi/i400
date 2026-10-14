package i400

import (
	"database/sql/driver"
	"encoding/binary"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// decfloatFormat describes one IEEE 754-2008 decimal interchange format.
type decfloatFormat struct {
	size        int // bytes: 8 (decimal64, DECFLOAT(16)) or 16 (decimal128, DECFLOAT(34))
	precision   int // significant digits
	bias        int // exponent bias
	qmin        int // smallest quantum exponent (unbiased)
	qmax        int // largest quantum exponent (unbiased)
	expContBits uint
}

var (
	decfloat16Format = decfloatFormat{size: 8, precision: 16, bias: decfloat16Bias, qmin: -decfloat16Bias, qmax: 767 - decfloat16Bias, expContBits: 8}
	decfloat34Format = decfloatFormat{size: 16, precision: 34, bias: decfloat34Bias, qmin: -decfloat34Bias, qmax: 12287 - decfloat34Bias, expContBits: 12}
)

// decfloatDecletTable maps a decimal value 0-999 to its 10-bit DPD declet. It is
// the inverse of decfloatUnpackDeclet. Where several declets decode to the same
// value (non-canonical forms), the lowest one is used.
var decfloatDecletTable = func() [1000]int {
	var t [1000]int
	for i := range t {
		t[i] = -1
	}
	for bits := 0; bits < 1024; bits++ {
		v := decfloatUnpackDeclet(bits)
		if v >= 0 && v < 1000 && t[v] < 0 {
			t[v] = bits
		}
	}
	return t
}()

// decfloatValue is a parsed decimal input.
type decfloatValue struct {
	neg  bool
	kind int // decfloatKindFinite, decfloatKindInf, decfloatKindNaN, decfloatKindSNaN
	coef *big.Int
	exp  int
}

const (
	decfloatKindFinite = iota
	decfloatKindInf
	decfloatKindNaN
	decfloatKindSNaN
)

// encodeDecfloat converts a decimal string (for example "123.45", "-0.001",
// "1E+10", "NaN", "-Infinity") into the IEEE 754-2008 DPD binary form used by
// DECFLOAT. size is 8 for DECFLOAT(16) and 16 for DECFLOAT(34). Significant
// digits beyond the precision are rounded half-even.
func encodeDecfloat(value string, size int) ([]byte, error) {
	var f decfloatFormat
	switch size {
	case 8:
		f = decfloat16Format
	case 16:
		f = decfloat34Format
	default:
		return nil, fmt.Errorf("unsupported DECFLOAT size %d", size)
	}

	v, err := parseDecfloatText(value)
	if err != nil {
		return nil, err
	}

	var sign uint64
	if v.neg {
		sign = 1
	}

	var hi, lo uint64
	switch v.kind {
	case decfloatKindInf:
		hi = sign<<63 | 0x1e<<58
	case decfloatKindNaN:
		hi = sign<<63 | 0x1f<<58
	case decfloatKindSNaN:
		hi = sign<<63 | 0x1f<<58 | 0x0200000000000000
	default:
		hi, lo, err = encodeDecfloatFinite(v, f, sign)
		if err != nil {
			return nil, err
		}
	}

	out := make([]byte, size)
	if size == 8 {
		binary.BigEndian.PutUint64(out, hi)
	} else {
		binary.BigEndian.PutUint64(out[:8], hi)
		binary.BigEndian.PutUint64(out[8:], lo)
	}
	return out, nil
}

// encodeDecfloatFinite builds the two 64-bit words for a finite value.
func encodeDecfloatFinite(v decfloatValue, f decfloatFormat, sign uint64) (uint64, uint64, error) {
	coef := new(big.Int).Set(v.coef)
	exp := v.exp

	if coef.Sign() == 0 {
		// Zero: the exponent is clamped into the representable range.
		if exp > f.qmax {
			exp = f.qmax
		}
		if exp < f.qmin {
			exp = f.qmin
		}
	} else {
		// Round to the precision (half-even).
		if n := len(coef.String()); n > f.precision {
			drop := n - f.precision
			coef = decfloatRoundDrop(coef, drop)
			exp += drop
			if n := len(coef.String()); n > f.precision {
				// Rounding carried into a new digit (10^p): drop one more zero.
				coef = decfloatRoundDrop(coef, 1)
				exp++
			}
		}
		// Subnormal: the exponent is below qmin, so round to qmin.
		if exp < f.qmin {
			drop := f.qmin - exp
			if drop > len(coef.String()) {
				coef = new(big.Int)
			} else {
				coef = decfloatRoundDrop(coef, drop)
			}
			exp = f.qmin
		}
		// Clamp: exponent above qmax is represented by padding zeros into the
		// coefficient, as long as they fit the precision.
		if exp > f.qmax {
			shift := exp - f.qmax
			if coef.Sign() != 0 && len(coef.String())+shift > f.precision {
				return 0, 0, fmt.Errorf("DECFLOAT exponent out of range: %s", v.describe())
			}
			if coef.Sign() != 0 {
				coef.Mul(coef, decfloatPow10(shift))
			}
			exp = f.qmax
		}
	}

	digits := coef.String()
	if len(digits) > f.precision {
		return 0, 0, fmt.Errorf("DECFLOAT coefficient exceeds %d digits", f.precision)
	}
	digits = strings.Repeat("0", f.precision-len(digits)) + digits

	biased := exp + f.bias
	if biased < 0 {
		return 0, 0, fmt.Errorf("DECFLOAT exponent out of range: %s", v.describe())
	}
	expMSD := uint64(biased >> f.expContBits)
	expCont := uint64(biased) & (1<<f.expContBits - 1)

	coefMSD := uint64(digits[0] - '0')
	var comb uint64
	if coefMSD < 8 {
		comb = expMSD<<3 | coefMSD
	} else {
		comb = 0x18 | expMSD<<1 | (coefMSD - 8)
	}

	// Pack the remaining digits as DPD declets, most significant first.
	rest := digits[1:]
	cont := new(big.Int)
	for i := 0; i+3 <= len(rest); i += 3 {
		g := int(rest[i]-'0')*100 + int(rest[i+1]-'0')*10 + int(rest[i+2]-'0')
		cont.Lsh(cont, 10)
		cont.Or(cont, big.NewInt(int64(decfloatDecletTable[g])))
	}

	if f.size == 8 {
		hi := sign<<63 | comb<<58 | expCont<<50 | cont.Uint64()
		return hi, 0, nil
	}
	mask := new(big.Int).SetUint64(^uint64(0))
	lo := new(big.Int).And(cont, mask).Uint64()
	hiCont := new(big.Int).Rsh(cont, 64).Uint64()
	hi := sign<<63 | comb<<58 | expCont<<46 | hiCont
	return hi, lo, nil
}

func (v decfloatValue) describe() string {
	return fmt.Sprintf("%s*10^%d", v.coef.String(), v.exp)
}

// parseDecfloatText parses a decimal string into sign, coefficient and exponent.
func parseDecfloatText(value string) (decfloatValue, error) {
	s := strings.TrimSpace(value)
	if s == "" {
		return decfloatValue{}, fmt.Errorf("empty DECFLOAT value")
	}
	var neg bool
	if s[0] == '+' || s[0] == '-' {
		neg = s[0] == '-'
		s = s[1:]
	}

	switch strings.ToLower(s) {
	case "inf", "infinity":
		return decfloatValue{neg: neg, kind: decfloatKindInf}, nil
	case "nan":
		return decfloatValue{neg: neg, kind: decfloatKindNaN}, nil
	case "snan":
		return decfloatValue{neg: neg, kind: decfloatKindSNaN}, nil
	}

	mant := s
	exp10 := 0
	if idx := strings.IndexAny(s, "eE"); idx >= 0 {
		mant = s[:idx]
		e, err := strconv.Atoi(s[idx+1:])
		if err != nil {
			return decfloatValue{}, fmt.Errorf("invalid DECFLOAT exponent in %q", value)
		}
		exp10 = e
	}

	intPart, fracPart := mant, ""
	if idx := strings.IndexByte(mant, '.'); idx >= 0 {
		intPart = mant[:idx]
		fracPart = mant[idx+1:]
	}
	if intPart == "" && fracPart == "" {
		return decfloatValue{}, fmt.Errorf("invalid DECFLOAT value %q", value)
	}
	for _, c := range intPart + fracPart {
		if c < '0' || c > '9' {
			return decfloatValue{}, fmt.Errorf("invalid DECFLOAT value %q", value)
		}
	}

	digits := strings.TrimLeft(intPart+fracPart, "0")
	coef := new(big.Int)
	if digits != "" {
		coef.SetString(digits, 10)
	}
	exp := exp10 - len(fracPart)
	return decfloatValue{neg: neg, kind: decfloatKindFinite, coef: coef, exp: exp}, nil
}

// decfloatRoundDrop divides coef by 10^drop, rounding half-even.
func decfloatRoundDrop(coef *big.Int, drop int) *big.Int {
	if drop <= 0 {
		return coef
	}
	p := decfloatPow10(drop)
	q, r := new(big.Int).QuoRem(coef, p, new(big.Int))
	cmp := new(big.Int).Lsh(r, 1).Cmp(p)
	if cmp > 0 || (cmp == 0 && q.Bit(0) == 1) {
		q.Add(q, big.NewInt(1))
	}
	return q
}

func decfloatPow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// decfloatParameterSize returns the DECFLOAT storage size for a parameter field.
func decfloatParameterSize(field parameterMarkerField) (int, error) {
	switch field.Length {
	case 8, 16:
		return field.Length, nil
	case 0:
		if field.Precision > 16 {
			return 16, nil
		}
		return 8, nil
	default:
		return 0, fmt.Errorf("unsupported DECFLOAT parameter length %d", field.Length)
	}
}

// encodeDecfloatParameter encodes a DECFLOAT input parameter as DPD binary.
func encodeDecfloatParameter(value driver.Value, field parameterMarkerField) ([]byte, error) {
	size, err := decfloatParameterSize(field)
	if err != nil {
		return nil, err
	}
	var text string
	if f, ok := value.(float64); ok {
		text = strconv.FormatFloat(f, 'g', -1, 64)
	} else {
		text, err = parameterFieldDecimalString(value)
		if err != nil {
			return nil, err
		}
	}
	if _, perr := parseDecfloatText(text); perr != nil {
		// Empty, whitespace-only or malformed text is a binding error, never 0.
		return nil, numericParameterError(value, "DECFLOAT", errInvalidNumericText)
	}
	return encodeDecfloat(text, size)
}
