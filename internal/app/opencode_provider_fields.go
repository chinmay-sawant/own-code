package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/chinmay-sawant/own-code/internal/opencode"
)

const (
	maxOpenCodeProviderFormBytes = 64 * 1024
	maxOpenCodeProviderFields    = 32
	maxOpenCodeProviderOptions   = 128
	maxOpenCodeProviderValue     = 16 * 1024
	maxOpenCodeProviderAnswer    = 64 * 1024
)

var providerFieldKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// OpenCodeProviderField describes one user-visible answer accepted by an
// OpenCode key-auth integration method. Sensitive marks values that should be
// shown in a masked control; answer values are never included in this type.
type OpenCodeProviderField struct {
	Key         string
	Title       string
	Description string
	Placeholder string
	Type        string
	Required    bool
	Hidden      bool
	Sensitive   bool
	Options     []OpenCodeProviderOption
	MinLength   *int
	MaxLength   *int
	Minimum     *float64
	Maximum     *float64
	MinItems    *int
	MaxItems    *int
	Custom      bool

	when         []openCodeProviderCondition
	defaultValue json.RawMessage
}

// OpenCodeProviderOption is one allowed value in an integration form.
type OpenCodeProviderOption struct {
	Value       string
	Label       string
	Description string
}

type openCodeProviderCondition struct {
	Key   string          `json:"key"`
	Op    string          `json:"op"`
	Value json.RawMessage `json:"value"`
}

type rawProviderField struct {
	Key         string                      `json:"key"`
	Title       string                      `json:"title"`
	Description string                      `json:"description"`
	Placeholder string                      `json:"placeholder"`
	Type        string                      `json:"type"`
	Required    bool                        `json:"required"`
	Hidden      bool                        `json:"hidden"`
	Options     []OpenCodeProviderOption    `json:"options"`
	MinLength   *int                        `json:"minLength"`
	MaxLength   *int                        `json:"maxLength"`
	Minimum     *float64                    `json:"minimum"`
	Maximum     *float64                    `json:"maximum"`
	MinItems    *int                        `json:"minItems"`
	MaxItems    *int                        `json:"maxItems"`
	Custom      bool                        `json:"custom"`
	When        []openCodeProviderCondition `json:"when"`
	Default     json.RawMessage             `json:"default"`
}

// OpenCodeProviderAnswer contains only the transient, typed values to submit
// as the API's `answer` object. It deliberately redacts values from formatting.
type OpenCodeProviderAnswer struct {
	values map[string]any
}

func (OpenCodeProviderAnswer) String() string {
	return "OpenCodeProviderAnswer{values:[redacted]}"
}

func (a OpenCodeProviderAnswer) GoString() string { return a.String() }

// MarshalJSON implements the OpenCode Form.Answer object shape. Callers should
// marshal only at the HTTP boundary and must not persist the result.
func (a OpenCodeProviderAnswer) MarshalJSON() ([]byte, error) {
	if len(a.values) == 0 {
		return []byte("{}"), nil
	}
	encoded, err := json.Marshal(a.values)
	if err != nil {
		return nil, errors.New("could not encode OpenCode provider form answer")
	}
	if len(encoded) > maxOpenCodeProviderAnswer {
		clear(encoded)
		return nil, errors.New("OpenCode provider form answer exceeds size limit")
	}
	return encoded, nil
}

// JSON returns the bounded wire representation expected by ConnectKeyInput.Answer.
func (a OpenCodeProviderAnswer) JSON() (json.RawMessage, error) {
	encoded, err := a.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encoded), nil
}

// ParseOpenCodeProviderKeyFields reads the optional form advertised for a key
// authentication method. Unsupported or malformed schemas fail closed rather
// than silently dropping required provider inputs.
func ParseOpenCodeProviderKeyFields(method opencode.IntegrationMethod) ([]OpenCodeProviderField, error) {
	if method.Type != "key" {
		return nil, errors.New("selected OpenCode authentication method is not key-based")
	}
	if len(method.Form) == 0 || bytes.Equal(bytes.TrimSpace(method.Form), []byte("null")) {
		return nil, nil
	}
	if len(method.Form) > maxOpenCodeProviderFormBytes {
		return nil, errors.New("OpenCode provider form exceeds size limit")
	}
	var rawFields []rawProviderField
	if err := json.Unmarshal(method.Form, &rawFields); err != nil {
		return nil, errors.New("OpenCode provider form is malformed")
	}
	if len(rawFields) > maxOpenCodeProviderFields {
		return nil, errors.New("OpenCode provider form has too many fields")
	}

	fields := make([]OpenCodeProviderField, 0, len(rawFields))
	fieldIndexes := make(map[string]int, len(rawFields))
	for _, raw := range rawFields {
		field, err := normalizeOpenCodeProviderField(raw)
		if err != nil {
			return nil, err
		}
		if _, exists := fieldIndexes[field.Key]; exists {
			return nil, errors.New("OpenCode provider form contains duplicate field keys")
		}
		if field.Key == "key" {
			return nil, errors.New("OpenCode provider form uses the reserved key field")
		}
		fieldIndexes[field.Key] = len(fields)
		fields = append(fields, field)
	}
	for index, field := range fields {
		for _, condition := range field.when {
			dependency, exists := fieldIndexes[condition.Key]
			if !exists || dependency >= index {
				return nil, errors.New("OpenCode provider form has an unsupported field dependency")
			}
		}
	}
	return fields, nil
}

// BuildOpenCodeProviderAnswer validates bounded form strings and converts them
// into the primitive values OpenCode accepts in Form.Answer. Unknown keys are
// rejected so stale UI inputs cannot be sent to a different integration.
func BuildOpenCodeProviderAnswer(method opencode.IntegrationMethod, input map[string]string) (OpenCodeProviderAnswer, error) {
	fields, err := ParseOpenCodeProviderKeyFields(method)
	if err != nil {
		return OpenCodeProviderAnswer{}, err
	}
	if len(input) > maxOpenCodeProviderFields {
		return OpenCodeProviderAnswer{}, errors.New("too many OpenCode provider form answers")
	}
	for key, value := range input {
		if len(key) > 128 || len(value) > maxOpenCodeProviderValue || !utf8.ValidString(value) {
			return OpenCodeProviderAnswer{}, errors.New("OpenCode provider form answer exceeds size limit")
		}
	}

	allowed := make(map[string]struct{}, len(fields))
	values := make(map[string]any, len(fields))
	for _, field := range fields {
		allowed[field.Key] = struct{}{}
	}
	for key := range input {
		if _, ok := allowed[key]; !ok {
			return OpenCodeProviderAnswer{}, errors.New("OpenCode provider form contains an unknown answer field")
		}
	}
	for _, field := range fields {
		visible, err := providerFieldVisible(field, values)
		if err != nil {
			return OpenCodeProviderAnswer{}, err
		}
		if !visible {
			continue
		}
		value, supplied := input[field.Key]
		if !supplied && len(field.defaultValue) != 0 {
			value, err = providerFieldDefault(field)
			if err != nil {
				return OpenCodeProviderAnswer{}, err
			}
			supplied = true
		}
		if !supplied || strings.TrimSpace(value) == "" {
			if field.Required {
				return OpenCodeProviderAnswer{}, fmt.Errorf("OpenCode provider form requires field %q", field.Key)
			}
			continue
		}
		typed, err := parseOpenCodeProviderValue(field, value)
		if err != nil {
			return OpenCodeProviderAnswer{}, fmt.Errorf("invalid OpenCode provider form field %q", field.Key)
		}
		values[field.Key] = typed
	}
	answer := OpenCodeProviderAnswer{values: values}
	if _, err := answer.MarshalJSON(); err != nil {
		return OpenCodeProviderAnswer{}, err
	}
	return answer, nil
}

func normalizeOpenCodeProviderField(raw rawProviderField) (OpenCodeProviderField, error) {
	if !providerFieldKeyPattern.MatchString(raw.Key) || raw.Key == "key" {
		return OpenCodeProviderField{}, errors.New("OpenCode provider form contains an invalid field key")
	}
	if !boundedProviderLabel(raw.Title, 255) || !boundedProviderLabel(raw.Description, 1024) || !boundedProviderLabel(raw.Placeholder, 255) {
		return OpenCodeProviderField{}, errors.New("OpenCode provider form has oversized field metadata")
	}
	field := OpenCodeProviderField{
		Key: raw.Key, Title: raw.Title, Description: raw.Description,
		Placeholder: raw.Placeholder, Type: raw.Type, Required: raw.Required,
		Hidden: raw.Hidden, Options: append([]OpenCodeProviderOption(nil), raw.Options...),
		MinLength: raw.MinLength, MaxLength: raw.MaxLength,
		Minimum: raw.Minimum, Maximum: raw.Maximum,
		MinItems: raw.MinItems, MaxItems: raw.MaxItems,
		Custom: raw.Custom, when: raw.When,
		defaultValue: append(json.RawMessage(nil), raw.Default...),
	}
	field.Sensitive = looksLikeSensitiveProviderField(raw.Key + " " + raw.Title + " " + raw.Description)
	switch raw.Type {
	case "string", "number", "integer", "boolean", "multiselect":
	default:
		return OpenCodeProviderField{}, errors.New("OpenCode provider form uses an unsupported field type")
	}
	if raw.Hidden && raw.Required && len(raw.Default) == 0 {
		return OpenCodeProviderField{}, errors.New("OpenCode provider form has a hidden required field without a default")
	}
	if len(raw.Options) > maxOpenCodeProviderOptions {
		return OpenCodeProviderField{}, errors.New("OpenCode provider form has too many options")
	}
	for _, option := range raw.Options {
		if !boundedProviderLabel(option.Value, 255) || !boundedProviderLabel(option.Label, 255) || !boundedProviderLabel(option.Description, 1024) {
			return OpenCodeProviderField{}, errors.New("OpenCode provider form has oversized options")
		}
	}
	if raw.MinLength != nil && (*raw.MinLength < 0 || *raw.MinLength > maxOpenCodeProviderValue) ||
		raw.MaxLength != nil && (*raw.MaxLength < 0 || *raw.MaxLength > maxOpenCodeProviderValue) ||
		raw.MinItems != nil && (*raw.MinItems < 0 || *raw.MinItems > maxOpenCodeProviderOptions) ||
		raw.MaxItems != nil && (*raw.MaxItems < 0 || *raw.MaxItems > maxOpenCodeProviderOptions) ||
		raw.Minimum != nil && (math.IsNaN(*raw.Minimum) || math.IsInf(*raw.Minimum, 0)) ||
		raw.Maximum != nil && (math.IsNaN(*raw.Maximum) || math.IsInf(*raw.Maximum, 0)) {
		return OpenCodeProviderField{}, errors.New("OpenCode provider form has invalid bounds")
	}
	if raw.MinLength != nil && raw.MaxLength != nil && *raw.MinLength > *raw.MaxLength ||
		raw.Minimum != nil && raw.Maximum != nil && *raw.Minimum > *raw.Maximum ||
		raw.MinItems != nil && raw.MaxItems != nil && *raw.MinItems > *raw.MaxItems {
		return OpenCodeProviderField{}, errors.New("OpenCode provider form has inconsistent bounds")
	}
	for _, condition := range raw.When {
		if !providerFieldKeyPattern.MatchString(condition.Key) || condition.Op != "eq" && condition.Op != "neq" || !validProviderConditionValue(condition.Value) {
			return OpenCodeProviderField{}, errors.New("OpenCode provider form has an invalid field condition")
		}
	}
	if len(raw.Default) != 0 && !validRawProviderDefault(raw.Type, raw.Default) {
		return OpenCodeProviderField{}, errors.New("OpenCode provider form has an invalid default value")
	}
	return field, nil
}

func boundedProviderLabel(value string, max int) bool {
	return len(value) <= max && utf8.ValidString(value)
}

func looksLikeSensitiveProviderField(metadata string) bool {
	value := strings.ToLower(metadata)
	for _, marker := range []string{"secret", "password", "token", "credential", "api key", "apikey", "auth key"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func validProviderConditionValue(raw json.RawMessage) bool {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return false
	}
	switch value.(type) {
	case string, bool, json.Number:
		return true
	default:
		return false
	}
}

func validRawProviderDefault(fieldType string, raw json.RawMessage) bool {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return false
	}
	switch fieldType {
	case "string":
		_, ok := value.(string)
		return ok
	case "number", "integer":
		_, ok := value.(json.Number)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "multiselect":
		_, ok := value.([]any)
		return ok
	default:
		return false
	}
}

func providerFieldDefault(field OpenCodeProviderField) (string, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(field.defaultValue))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", errors.New("invalid OpenCode provider form default")
	}
	if field.Type == "multiselect" {
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", errors.New("invalid OpenCode provider form default")
		}
		return string(encoded), nil
	}
	return fmt.Sprint(value), nil
}

func providerFieldVisible(field OpenCodeProviderField, values map[string]any) (bool, error) {
	for _, condition := range field.when {
		actual, exists := values[condition.Key]
		if !exists {
			return false, errors.New("OpenCode provider form condition depends on an unanswered field")
		}
		var expected any
		decoder := json.NewDecoder(bytes.NewReader(condition.Value))
		decoder.UseNumber()
		if err := decoder.Decode(&expected); err != nil {
			return false, errors.New("OpenCode provider form condition is invalid")
		}
		matches := fmt.Sprint(actual) == fmt.Sprint(expected)
		if condition.Op == "neq" {
			matches = !matches
		}
		if !matches {
			return false, nil
		}
	}
	return true, nil
}

func parseOpenCodeProviderValue(field OpenCodeProviderField, value string) (any, error) {
	switch field.Type {
	case "string":
		if !utf8.ValidString(value) || len(value) > maxOpenCodeProviderValue {
			return nil, errors.New("invalid string")
		}
		length := len([]rune(value))
		if field.MinLength != nil && length < *field.MinLength || field.MaxLength != nil && length > *field.MaxLength {
			return nil, errors.New("string length is out of range")
		}
		if len(field.Options) > 0 && !field.Custom && !providerOptionContains(field.Options, value) {
			return nil, errors.New("string value is not an allowed option")
		}
		return value, nil
	case "number":
		number, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) ||
			field.Minimum != nil && number < *field.Minimum || field.Maximum != nil && number > *field.Maximum {
			return nil, errors.New("number is out of range")
		}
		return number, nil
	case "integer":
		number, err := strconv.ParseInt(value, 10, 64)
		if err != nil || field.Minimum != nil && float64(number) < *field.Minimum || field.Maximum != nil && float64(number) > *field.Maximum {
			return nil, errors.New("integer is out of range")
		}
		return number, nil
	case "boolean":
		boolean, err := strconv.ParseBool(value)
		if err != nil {
			return nil, errors.New("invalid boolean")
		}
		return boolean, nil
	case "multiselect":
		var values []string
		if err := json.Unmarshal([]byte(value), &values); err != nil {
			return nil, errors.New("multiselect must be a JSON string array")
		}
		if field.MinItems != nil && len(values) < *field.MinItems || field.MaxItems != nil && len(values) > *field.MaxItems {
			return nil, errors.New("multiselect size is out of range")
		}
		for _, item := range values {
			if len(field.Options) > 0 && !field.Custom && !providerOptionContains(field.Options, item) {
				return nil, errors.New("multiselect contains an unsupported option")
			}
		}
		return values, nil
	default:
		return nil, errors.New("unsupported form type")
	}
}

func providerOptionContains(options []OpenCodeProviderOption, value string) bool {
	for _, option := range options {
		if option.Value == value {
			return true
		}
	}
	return false
}
