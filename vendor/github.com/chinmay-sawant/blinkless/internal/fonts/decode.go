package fonts

const (
	// Caps for untrusted @font-face payloads.
	maxTables   = 1024
	maxTableLen = 16 << 20 // 16 MiB per table
	maxSFNTSize = 32 << 20 // 32 MiB reconstructed SFNT
)

// Decode returns SFNT bytes for a font payload. WOFF2 input is reconstructed
// into SFNT. Every other container passes through for ParseFontBytes.
func Decode(data []byte) ([]byte, error) {
	if len(data) >= 4 && string(data[0:4]) == woff2Signature {
		return DecodeWOFF2(data)
	}

	return data, nil
}
