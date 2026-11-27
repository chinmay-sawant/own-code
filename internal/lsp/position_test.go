package lsp

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRuneColUTF16(t *testing.T) {
	line := "a😀b€"
	cases := []struct{ rc, u16 int }{{0, 0}, {1, 1}, {2, 3}, {3, 4}, {4, 5}, {99, 5}, {-1, 0}}
	for _, c := range cases {
		if got := RuneColToUTF16(line, c.rc); got != c.u16 {
			t.Errorf("RuneColToUTF16(%d)=%d want %d", c.rc, got, c.u16)
		}
	}
	// Inside a surrogate pair rounds down.
	if got := UTF16ToRuneCol(line, 2); got != 1 {
		t.Errorf("mid-surrogate = %d want 1", got)
	}
	if got := UTF16ToRuneCol(line, 100); got != 4 {
		t.Errorf("clamp = %d want 4", got)
	}
}

func TestSplitLines(t *testing.T) {
	got := SplitLines("a\r\nb\rc\n")
	want := []string{"a", "b", "c", ""}
	if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != 4 {
		t.Fatalf("got %q", got)
	}
}

func TestOffsetPositionCRLFAndEmoji(t *testing.T) {
	text := "x😀y\r\nséz\nlast"
	// "y" is at line 0, utf16 col 3.
	off := strings.Index(text, "y")
	if p := PositionAtOffset(text, off); p != (Position{0, 3}) {
		t.Fatalf("y pos %+v", p)
	}
	if got := OffsetAtPosition(text, Position{0, 3}); got != off {
		t.Fatalf("offset %d want %d", got, off)
	}
	off = strings.Index(text, "z")
	if p := PositionAtOffset(text, off); p != (Position{1, 2}) {
		t.Fatalf("z pos %+v", p)
	}
	off = strings.Index(text, "last")
	if p := PositionAtOffset(text, off); p != (Position{2, 0}) {
		t.Fatalf("last pos %+v", p)
	}
	if got := OffsetAtPosition(text, Position{2, 0}); got != off {
		t.Fatalf("last off %d", got)
	}
	// Character past EOL clamps before the CRLF.
	if got := OffsetAtPosition(text, Position{0, 50}); got != strings.Index(text, "\r") {
		t.Fatalf("clamp off %d", got)
	}
	// Line past end clamps to len.
	if got := OffsetAtPosition(text, Position{9, 0}); got != len(text) {
		t.Fatalf("eof off %d", got)
	}
	// Offset between \r and \n stays on line 0.
	if p := PositionAtOffset(text, strings.Index(text, "\n")); p.Line != 0 {
		t.Fatalf("mid-crlf %+v", p)
	}
}

func TestPositionEmptyAndLoneCR(t *testing.T) {
	if p := PositionAtOffset("", 5); p != (Position{}) {
		t.Fatal(p)
	}
	if got := OffsetAtPosition("", Position{3, 3}); got != 0 {
		t.Fatal(got)
	}
	text := "a\rb"
	if p := PositionAtOffset(text, 2); p != (Position{1, 0}) {
		t.Fatal(p)
	}
	if got := OffsetAtPosition(text, Position{1, 1}); got != 3 {
		t.Fatal(got)
	}
}

func randText(rng *rand.Rand) string {
	alphabet := []string{"a", "b", " ", "é", "€", "😀", "𝄞", "\n", "\r\n", "\r", "中"}
	var sb strings.Builder
	for i, n := 0, rng.Intn(40); i < n; i++ {
		sb.WriteString(alphabet[rng.Intn(len(alphabet))])
	}
	return sb.String()
}

func TestRoundTripProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 3000; iter++ {
		text := randText(rng)
		lines := SplitLines(text)
		// Every rune boundary round-trips offset -> position -> offset.
		for off := 0; off <= len(text); off++ {
			if off < len(text) && !utf8.RuneStart(text[off]) {
				continue
			}
			if off > 0 && off < len(text) && text[off] == '\n' && text[off-1] == '\r' {
				continue // inside a CRLF; not a valid position
			}
			p := PositionAtOffset(text, off)
			if p.Line >= len(lines) {
				t.Fatalf("%q off %d: line %d out of %d", text, off, p.Line, len(lines))
			}
			if got := OffsetAtPosition(text, p); got != off {
				t.Fatalf("%q off %d -> %+v -> %d", text, off, p, got)
			}
			// rune col conversion agrees.
			rc := UTF16ToRuneCol(lines[p.Line], p.Character)
			if RuneColToUTF16(lines[p.Line], rc) != p.Character {
				t.Fatalf("%q rune col mismatch at %+v", text, p)
			}
		}
	}
}

func FuzzPosition(f *testing.F) {
	f.Add("a😀b\r\nc", 3, 1, 2)
	f.Fuzz(func(t *testing.T, text string, off, line, ch int) {
		if !utf8.ValidString(text) {
			return
		}
		p := PositionAtOffset(text, off)
		if p.Line < 0 || p.Character < 0 {
			t.Fatal("negative position")
		}
		got := OffsetAtPosition(text, Position{line, ch})
		if got < 0 || got > len(text) {
			t.Fatalf("offset out of range: %d", got)
		}
		if got < len(text) && !utf8.RuneStart(text[got]) {
			t.Fatalf("offset %d not on rune boundary", got)
		}
	})
}
