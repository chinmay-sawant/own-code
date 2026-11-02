package edit

import (
	"reflect"
	"testing"
)

func TestTrimLines(t *testing.T) {
	lines := []string{"a  ", "\t", "", "b\t \t", "c"}
	got, n := TrimLines(lines)

	want := []string{"a", "", "", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TrimLines = %q, want %q", got, want)
	}

	if n != 3 {
		t.Fatalf("TrimLines changed = %d, want 3", n)
	}

	if lines[0] != "a  " {
		t.Fatal("TrimLines mutated its input")
	}
}

func TestTouchedLines(t *testing.T) {
	saved := []string{"a", "b", "c"}
	current := []string{"a", "B", "c", "d"}

	got := TouchedLines(saved, current)
	want := []bool{false, true, false, true}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TouchedLines = %v, want %v", got, want)
	}
}

func TestTrimTouched(t *testing.T) {
	current := []string{"keep  ", "touch  ", "same"}
	touched := []bool{false, true, false}

	got, n := TrimTouched(current, touched)
	want := []string{"keep  ", "touch", "same"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TrimTouched = %q, want %q", got, want)
	}

	if n != 1 {
		t.Fatalf("TrimTouched changed = %d, want 1", n)
	}
}
