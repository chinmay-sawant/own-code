package edit

import (
	"math/rand"
	"testing"
)

func randText(r *rand.Rand) string {
	const alphabet = "ab \né世x"

	runes := []rune(alphabet)
	n := r.Intn(12)
	out := make([]rune, n)

	for i := range out {
		out[i] = runes[r.Intn(len(runes))]
	}

	return string(out)
}

func TestDiffApplyInvertProperty(t *testing.T) {
	r := rand.New(rand.NewSource(7))

	for i := 0; i < 5000; i++ {
		old, updated := randText(r), randText(r)

		e, ok := Diff(old, updated)
		if !ok {
			if old != updated {
				t.Fatalf("Diff reported equal for %q vs %q", old, updated)
			}

			continue
		}

		if got := e.Apply(old); got != updated {
			t.Fatalf("Apply(%q) = %q, want %q (edit %+v)", old, got, updated, e)
		}

		if back := e.Invert(old).Apply(updated); back != old {
			t.Fatalf("undo round trip %q -> %q -> %q", old, updated, back)
		}
	}
}

func TestDiffEqual(t *testing.T) {
	if _, ok := Diff("same\n", "same\n"); ok {
		t.Fatal("equal texts produced an edit")
	}
}

func TestEditMapOffset(t *testing.T) {
	e := Edit{Start: 2, End: 4, Text: "xyz"}

	cases := map[int]int{0: 0, 2: 2, 3: 5, 4: 5, 6: 7}
	for in, want := range cases {
		if got := e.MapOffset(in); got != want {
			t.Fatalf("MapOffset(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestBufferTextRoundTripProperty(t *testing.T) {
	r := rand.New(rand.NewSource(11))

	for i := 0; i < 2000; i++ {
		text := randText(r)
		b := NewBuffer("f", []byte(text))
		b.SetText(b.Text())

		if b.Dirty {
			t.Fatalf("round trip of %q marked dirty", text)
		}

		if again := NewBuffer("f", []byte(b.Text())).Text(); again != b.Text() {
			t.Fatalf("not idempotent: %q vs %q", again, b.Text())
		}
	}
}
