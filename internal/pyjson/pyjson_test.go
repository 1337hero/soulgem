package pyjson

import (
	"math"
	"testing"
)

func TestAppendFloat(t *testing.T) {
	cases := []struct {
		value float64
		want  string
	}{
		{0, "0.0"},
		{-0, "0.0"},
		{1, "1.0"},
		{-0.01428, "-0.01428"},
		{0.55, "0.55"},
		{0.5019607843137255, "0.5019607843137255"},
		{1e21, "1e+21"},
		{1.0 / 3.0, "0.3333333333333333"},
		{math.NaN(), "NaN"},
		{math.Inf(1), "Infinity"},
		{math.Inf(-1), "-Infinity"},
	}
	for _, test := range cases {
		if got := string(AppendFloat(nil, test.value)); got != test.want {
			t.Errorf("AppendFloat(%v) = %s, want %s", test.value, got, test.want)
		}
	}
}

func TestAppendString(t *testing.T) {
	cases := []struct{ value, want string }{
		{"", `""`},
		{"NPC Head [Head]", `"NPC Head [Head]"`},
		{`back\slash`, `"back\\slash"`},
		{"quote\"inside", `"quote\"inside"`},
		{"tab\there", `"tab\there"`},
	}
	for _, test := range cases {
		if got := string(AppendString(nil, test.value)); got != test.want {
			t.Errorf("AppendString(%q) = %s, want %s", test.value, got, test.want)
		}
	}
}
