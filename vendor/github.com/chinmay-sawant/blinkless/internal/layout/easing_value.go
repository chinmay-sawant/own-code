package layout

import (
	"math"
	"strconv"
	"strings"
)

const (
	easingLinear             = "linear"
	cssOne                   = "1"
	easingArgumentCount      = 2
	commaListInitialCapacity = 2
)

// easingValueAccepted reports whether value is one CSS <easing-function>.
func easingValueAccepted(value string) bool {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case easingLinear, animationEase, "ease-in", "ease-out", "ease-in-out", "step-start", "step-end":
		return true
	}

	name, body, valid := easingFunctionParts(value)
	if !valid {
		return false
	}

	switch name {
	case "cubic-bezier":
		return cubicBezierValueAccepted(body)
	case "steps":
		return stepsValueAccepted(body)
	case easingLinear:
		return linearValueAccepted(body)
	default:
		return false
	}
}

func easingFunctionParts(value string) (string, string, bool) {
	open := strings.IndexByte(value, '(')
	if open <= 0 || value[len(value)-1] != ')' {
		return "", "", false
	}

	name := strings.TrimSpace(value[:open])
	if name == "" || strings.ContainsAny(name, " \t\n\r\f\v()") {
		return "", "", false
	}

	body := value[open+1 : len(value)-1]
	if strings.ContainsAny(body, "()") {
		return "", "", false
	}

	return strings.ToLower(name), body, true
}

func cubicBezierValueAccepted(body string) bool {
	parts, valid := strictCommaParts(body)
	if !valid || len(parts) != 4 {
		return false
	}

	values := [4]float64{}

	for index, part := range parts {
		value, numberValid := cssNumberValue(part)
		if !numberValid {
			return false
		}

		values[index] = value
	}

	return values[0] >= 0 && values[0] <= 1 && values[2] >= 0 && values[2] <= 1
}

func stepsValueAccepted(body string) bool {
	parts, valid := strictCommaParts(body)
	if !valid || len(parts) < 1 || len(parts) > easingArgumentCount {
		return false
	}

	countDigits, valid := positiveCSSIntegerDigits(strings.TrimSpace(parts[0]))
	if !valid {
		return false
	}

	position := "jump-end"
	if len(parts) == easingArgumentCount {
		position = strings.ToLower(strings.TrimSpace(parts[1]))
	}

	switch position {
	case "jump-none":
		return countDigits > cssOne
	case "jump-start", "jump-end", "jump-both", "start", "end":
		return true
	default:
		return false
	}
}

func linearValueAccepted(body string) bool {
	parts, valid := strictCommaParts(body)
	if !valid || len(parts) == 0 {
		return false
	}

	for _, part := range parts {
		if !linearStopAccepted(part) {
			return false
		}
	}

	return true
}

func linearStopAccepted(part string) bool {
	fields := strings.Fields(part)
	if len(fields) == 0 || len(fields) > 3 {
		return false
	}

	numbers, percentages := 0, 0

	for _, field := range fields {
		if _, valid := cssNumberValue(field); valid {
			numbers++

			continue
		}

		if _, valid := cssPercentageValue(field); valid {
			percentages++

			continue
		}

		return false
	}

	return numbers == 1 && percentages <= 2
}

func cssNumberValue(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if !cssNumberToken(raw) {
		return 0, false
	}

	value, err := strconv.ParseFloat(raw, 64)

	return value, err == nil && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func cssPercentageValue(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasSuffix(raw, "%") {
		return 0, false
	}

	return cssNumberValue(strings.TrimSuffix(raw, "%"))
}

func cssNumberToken(raw string) bool {
	if raw == "" {
		return false
	}

	index := numericSignEnd(raw)
	if index < 0 {
		return false
	}

	integerEnd, integerDigits := scanNumericDigits(raw, index)
	index = integerEnd

	if index < len(raw) && raw[index] == '.' {
		fractionEnd, fractionDigits := scanNumericDigits(raw, index+1)
		if fractionDigits == 0 {
			return false
		}

		index = fractionEnd
		integerDigits += fractionDigits
	}

	if integerDigits == 0 {
		return false
	}

	index, valid := consumeNumericExponent(raw, index)

	return valid && index == len(raw)
}

func numericSignEnd(raw string) int {
	if raw[0] != '+' && raw[0] != '-' {
		return 0
	}

	if len(raw) == 1 {
		return -1
	}

	return 1
}

func scanNumericDigits(raw string, index int) (int, int) {
	start := index

	for index < len(raw) && isCSSNumericDigit(raw[index]) {
		index++
	}

	return index, index - start
}

func consumeNumericExponent(raw string, index int) (int, bool) {
	if index == len(raw) || (raw[index] != 'e' && raw[index] != 'E') {
		return index, true
	}

	index++
	if index < len(raw) && (raw[index] == '+' || raw[index] == '-') {
		index++
	}

	_, digits := scanNumericDigits(raw, index)

	return len(raw), digits > 0 && hasOnlyNumericExponentTail(raw, index)
}

func hasOnlyNumericExponentTail(raw string, index int) bool {
	for ; index < len(raw); index++ {
		if !isCSSNumericDigit(raw[index]) {
			return false
		}
	}

	return true
}

func isCSSNumericDigit(char byte) bool {
	return char >= '0' && char <= '9'
}

func cssIntegerToken(raw string) bool {
	if raw == "" {
		return false
	}

	index := numericSignEnd(raw)
	if index < 0 {
		return false
	}

	end, digits := scanNumericDigits(raw, index)

	return digits > 0 && end == len(raw)
}

func positiveCSSIntegerDigits(raw string) (string, bool) {
	if !cssIntegerToken(raw) || raw[0] == '-' {
		return "", false
	}

	if raw[0] == '+' {
		raw = raw[1:]
	}

	digits := strings.TrimLeft(raw, "0")

	return digits, digits != ""
}

// strictCommaParts splits a comma list and rejects empty entries and unclosed
// delimiters. Easing functions only allow flat arguments, but tracking nesting
// here also keeps transition list parsing from accepting malformed functions.
func strictCommaParts(value string) ([]string, bool) {
	if strings.TrimSpace(value) == "" {
		return nil, false
	}

	parser := commaListParser{
		value:   value,
		parts:   make([]string, 0, commaListInitialCapacity),
		start:   0,
		depth:   0,
		quote:   0,
		escaped: false,
	}

	for index, char := range value {
		separated, valid := parser.consume(char)
		if !valid {
			return nil, false
		}

		if separated {
			if !parser.appendPart(index) {
				return nil, false
			}
		}
	}

	return parser.finish()
}

type commaListParser struct {
	value   string
	parts   []string
	start   int
	depth   int
	quote   rune
	escaped bool
}

func (parser *commaListParser) consume(char rune) (bool, bool) {
	if parser.escaped {
		parser.escaped = false

		return false, true
	}

	if char == '\\' {
		parser.escaped = true

		return false, true
	}

	if parser.quote != 0 {
		if char == parser.quote {
			parser.quote = 0
		}

		return false, true
	}

	switch char {
	case '"', '\'':
		parser.quote = char
	case '(':
		parser.depth++
	case ')':
		parser.depth--
		if parser.depth < 0 {
			return false, false
		}
	case ',':
		return parser.depth == 0, true
	}

	return false, true
}

func (parser *commaListParser) appendPart(index int) bool {
	part := strings.TrimSpace(parser.value[parser.start:index])
	if part == "" {
		return false
	}

	parser.parts = append(parser.parts, part)
	parser.start = index + 1

	return true
}

func (parser *commaListParser) finish() ([]string, bool) {
	if parser.depth != 0 || parser.quote != 0 || parser.escaped {
		return nil, false
	}

	part := strings.TrimSpace(parser.value[parser.start:])
	if part == "" {
		return nil, false
	}

	parser.parts = append(parser.parts, part)

	return parser.parts, true
}
