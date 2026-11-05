package render

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// clean replaces control, surrogate-range, bidirectional override and
// non-character code points with spaces and keeps at most maximum runes.
func clean(value string, maximum int) string {
	runes := []rune(value)
	for i, r := range runes {
		if r <= 0x1f || r >= 0x7f && r <= 0x9f || r >= 0xd800 && r <= 0xdfff ||
			r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 || r == 0xfffe || r == 0xffff {
			runes[i] = ' '
		}
	}
	if maximum > 0 && len(runes) > maximum {
		runes = runes[:maximum]
	}
	return string(runes)
}

// decimals formats v with places decimals, rounding half to even like Python.
func decimals(v float64, places int) string { return strconv.FormatFloat(v, 'f', places, 64) }

// general formats v like Python's "{:g}": six significant digits without
// trailing zeros, switching to an exponent outside [1e-4, 1e6).
func general(v float64) string {
	if v == 0 {
		return "0"
	}
	exp := int(math.Floor(math.Log10(math.Abs(v))))
	// Rounding to six digits can carry into the next power of ten.
	if rounded, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'e', 5, 64), 64); rounded != 0 {
		exp = int(math.Floor(math.Log10(math.Abs(rounded))))
	}
	if exp < -4 || exp >= 6 {
		mantissa, power, _ := strings.Cut(strconv.FormatFloat(v, 'e', 5, 64), "e")
		if strings.Contains(mantissa, ".") {
			mantissa = strings.TrimRight(strings.TrimRight(mantissa, "0"), ".")
		}
		sign, digits := power[:1], strings.TrimLeft(power[1:], "0")
		if len(digits) < 2 {
			digits = strings.Repeat("0", 2-len(digits)) + digits
		}
		return mantissa + "e" + sign + digits
	}
	s := strconv.FormatFloat(v, 'f', max(0, 5-exp), 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// percentText shows whole percents from 10 up, one decimal below.
func percentText(value float64) string {
	if value >= 10 || value == 0 {
		return decimals(value, 0) + "%"
	}
	return strings.TrimRight(strings.TrimRight(decimals(value, 1), "0"), ".") + "%"
}

// grouped inserts thousands separators into a run of digits.
func grouped(digits string) string {
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// roundHalfUp rounds a non-negative value to the given decimals.
func roundHalfUp(v *big.Rat, decimals int) *big.Int {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	scaled := new(big.Rat).Mul(v, new(big.Rat).SetInt(scale))
	scaled.Add(scaled, big.NewRat(1, 2))
	return new(big.Int).Quo(scaled.Num(), scaled.Denom())
}

// centsText formats a scaled integer of hundredths as "1,234.57".
func centsText(cents *big.Int) string {
	s := cents.String()
	for len(s) < 3 {
		s = "0" + s
	}
	return grouped(s[:len(s)-2]) + "." + s[len(s)-2:]
}

// money shows a stored amount rounded half up to cents, e.g. $1,234.57.
func money(v *big.Rat) string { return "$" + centsText(roundHalfUp(v, 2)) }

// tokenCount compacts a token total, e.g. 1.65B, rounding half up.
func tokenCount(value *int64) string {
	if value == nil {
		return "—"
	}
	for _, unit := range []struct {
		divisor int64
		suffix  string
	}{{1_000_000_000, "B"}, {1_000_000, "M"}, {1_000, "K"}} {
		if *value >= unit.divisor {
			s := centsText(roundHalfUp(big.NewRat(*value, unit.divisor), 2))
			s = strings.ReplaceAll(s, ",", "")
			return strings.TrimRight(strings.TrimRight(s, "0"), ".") + unit.suffix
		}
	}
	return strconv.FormatInt(*value, 10)
}
