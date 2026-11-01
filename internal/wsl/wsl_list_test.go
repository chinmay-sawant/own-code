package wsl

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"
)

func encodeUTF16LE(s string, bom bool) []byte {
	u16 := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(u16)*2+2)
	if bom {
		out = append(out, 0xFF, 0xFE)
	}
	for _, v := range u16 {
		var b [2]byte
		binary.LittleEndian.PutUint16(b[:], v)
		out = append(out, b[:]...)
	}
	return out
}

func TestIsWSLDistrosRoot(t *testing.T) {
	for _, p := range []string{
		`\\wsl$`, `\\wsl$\`, `\\wsl.localhost`, `\\WSL$`, `//wsl$/`, `//WSL.LOCALHOST`,
	} {
		if !IsWSLDistrosRoot(p) {
			t.Errorf("IsWSLDistrosRoot(%q) = false, want true", p)
		}
	}
	for _, p := range []string{
		"", "/", `C:\`, `\\wsl$\Ubuntu`, `\\server\share`, `\\wsl$foo`,
	} {
		if IsWSLDistrosRoot(p) {
			t.Errorf("IsWSLDistrosRoot(%q) = true, want false", p)
		}
	}
}

func TestIsWSLPathAndDistroRoot(t *testing.T) {
	if !IsWSLPath(`\\wsl$\Ubuntu\home\user`) {
		t.Error("IsWSLPath wsl unc = false, want true")
	}
	if IsWSLPath(`C:\Users\name`) {
		t.Error("IsWSLPath drive = true, want false")
	}
	if !IsWSLDistroRoot(`\\wsl$\Ubuntu`) {
		t.Error("IsWSLDistroRoot bare = false, want true")
	}
	if IsWSLDistroRoot(`\\wsl$\Ubuntu\home`) {
		t.Error("IsWSLDistroRoot nested = true, want false")
	}
}

func TestParseDistroListText(t *testing.T) {
	got := ParseDistroListText("Ubuntu\nDebian\n\n  \ndocker-desktop\n")
	if len(got) != 3 || got[0] != "Ubuntu" || got[1] != "Debian" || got[2] != "docker-desktop" {
		t.Fatalf("ParseDistroListText = %q, want 3 distros", got)
	}
	got = ParseDistroListText("* Ubuntu\n* Debian\n")
	if len(got) != 2 {
		t.Fatalf("verbose mark parse = %q, want 2", got)
	}
	got = ParseDistroListText("")
	if len(got) != 0 {
		t.Fatalf("empty parse = %q, want none", got)
	}
}

func TestDecodeWSLListOutput(t *testing.T) {
	le := encodeUTF16LE("Ubuntu\r\nDebian\r\n", true)
	if got := DecodeWSLListOutput(le); got != "Ubuntu\r\nDebian\r\n" {
		t.Fatalf("BOM LE decode = %q", got)
	}
	if names := ParseDistroListText(DecodeWSLListOutput(le)); len(names) != 2 {
		t.Fatalf("BOM LE parse = %q, want 2", names)
	}
	plain := []byte("Ubuntu\nDebian\n")
	if got := DecodeWSLListOutput(plain); got != string(plain) {
		t.Fatalf("UTF-8 decode = %q", got)
	}
	noBOM := encodeUTF16LE("Ubuntu\n", false)
	if got := DecodeWSLListOutput(noBOM); got != "Ubuntu\n" {
		t.Fatalf("no-BOM LE decode = %q", got)
	}
}
