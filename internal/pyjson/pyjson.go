// Package pyjson spells numbers and strings the way Python's json.dumps did.
//
// The asset pipeline was ported from Python and its outputs are checked by
// hash, so a float that Python wrote as "0.0" cannot become "0". These two
// functions are the whole of that compatibility surface; everything else about
// the JSON shape is the caller's business.
package pyjson

import (
	"math"
	"strconv"
	"strings"
)

// AppendFloat writes a float the way Python's repr does: shortest round-trip
// digits, always with a decimal point or exponent, and the non-standard NaN and
// Infinity spellings json.dumps emits.
func AppendFloat(dst []byte, value float64) []byte {
	switch {
	case math.IsNaN(value):
		return append(dst, "NaN"...)
	case math.IsInf(value, 1):
		return append(dst, "Infinity"...)
	case math.IsInf(value, -1):
		return append(dst, "-Infinity"...)
	}
	text := strconv.FormatFloat(value, 'g', -1, 64)
	if !strings.ContainsAny(text, ".e") {
		text += ".0"
	}
	return append(dst, text...)
}

func AppendString(dst []byte, value string) []byte {
	return strconv.AppendQuote(dst, value)
}
