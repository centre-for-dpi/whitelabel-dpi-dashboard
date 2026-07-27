package model_test

import (
	"testing"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
)

func TestOptFloatPresence(t *testing.T) {
	tests := []struct {
		name  string
		got   model.OptFloat
		valid bool
		value float64
	}{
		{"present", model.Float(99.41), true, 99.41},
		{"present zero", model.Float(0), true, 0},
		{"absent", model.NoFloat(), false, 0},
		{"zero value is absent", model.OptFloat{}, false, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got.Valid != tc.valid {
				t.Errorf("Valid = %v, want %v", tc.got.Valid, tc.valid)
			}
			if tc.got.Value != tc.value {
				t.Errorf("Value = %v, want %v", tc.got.Value, tc.value)
			}
		})
	}
}

func TestOptFloatOr(t *testing.T) {
	// A reported 0 must win over the default: 0% availability is a real
	// reading, not a missing one.
	if got := model.Float(0).Or(99.9); got != 0 {
		t.Errorf("Float(0).Or(99.9) = %v, want 0", got)
	}
	if got := model.Float(99.41).Or(0); got != 99.41 {
		t.Errorf("Float(99.41).Or(0) = %v, want 99.41", got)
	}
	if got := model.NoFloat().Or(99.9); got != 99.9 {
		t.Errorf("NoFloat().Or(99.9) = %v, want 99.9", got)
	}
}

func TestOptFloatIsComparable(t *testing.T) {
	// Comparability is why OptFloat is a struct rather than a *float64: the
	// enclosing Metrics and Maintenance values are compared with == elsewhere.
	if model.Float(1) != model.Float(1) {
		t.Error("equal present values compared unequal")
	}
	if model.Float(0) == model.NoFloat() {
		t.Error("a present zero compared equal to an absent value")
	}
}
