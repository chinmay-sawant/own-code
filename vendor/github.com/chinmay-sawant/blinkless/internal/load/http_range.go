package load

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// checkRangeConsistency checks the whole Content-Range value and compares
// its span with a declared or observed body length. A negative length means
// the response has no declared length, as with chunked transfer encoding.
func checkRangeConsistency(header string, length int64) error {
	if header == "" {
		return nil
	}

	span, ok := contentRangeSpan(header)
	if !ok {
		return fmt.Errorf("%w: invalid Content-Range %q", errContentLengthRangeMismatch, clipRef(header))
	}

	if length >= 0 && length != span {
		return fmt.Errorf("%w: body length %d disagrees with Content-Range %q",
			errContentLengthRangeMismatch, length, clipRef(header))
	}

	return nil
}

func contentRangeSpan(header string) (int64, bool) {
	raw, hasUnit := strings.CutPrefix(header, "bytes ")
	if !hasUnit {
		return 0, false
	}

	bounds, total, hasTotal := strings.Cut(raw, "/")
	if !hasTotal {
		return 0, false
	}

	first, last, validBounds := parseRangeBounds(bounds)
	if !validBounds {
		return 0, false
	}

	if total != "*" {
		size, err := strconv.ParseUint(total, 10, 63)
		if err != nil || size <= last {
			return 0, false
		}
	}

	// ParseUint limits both bounds to 63 bits; parseRangeBounds also excludes
	// a span of MaxInt64, so the conversion and the added byte both fit.
	return int64(last-first) + 1, true //nolint:gosec // both bounds and the span are checked above
}

func parseRangeBounds(bounds string) (uint64, uint64, bool) {
	start, end, hasBounds := strings.Cut(bounds, "-")
	if !hasBounds {
		return 0, 0, false
	}

	first, firstErr := strconv.ParseUint(start, 10, 63)
	last, lastErr := strconv.ParseUint(end, 10, 63)

	return first, last, firstErr == nil && lastErr == nil && last >= first && last-first < math.MaxInt64
}
