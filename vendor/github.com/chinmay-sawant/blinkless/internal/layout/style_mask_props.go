package layout

import "strings"

type maskStyleProperty struct {
	field       *string
	parentField *string
	initial     string
	lower       bool
}

func applyMaskProps(
	style *ResolvedStyle, prop, value string, _ float64, _ *styleContext,
	parent *ResolvedStyle, hasParent bool,
) bool {
	if prop == maskShorthandProperty {
		return true
	}

	property, ok := maskStylePropertyFor(style, parent, prop)
	if !ok {
		return false
	}

	trimmed := resolveMaskShorthandComponent(prop, strings.TrimSpace(value))
	parentValue := maskParentValue(property.parentField)

	if wide, ok := cssWideValue(trimmed, parentValue, property.initial, hasParent); ok {
		*property.field = wide

		return true
	}

	if property.lower {
		trimmed = strings.ToLower(trimmed)
	}

	*property.field = trimmed

	return true
}

func resolveMaskShorthandComponent(prop, value string) string {
	if !strings.HasPrefix(value, maskShorthandDeferredPrefix) {
		return value
	}

	shorthand := strings.TrimPrefix(value, maskShorthandDeferredPrefix)
	if cssWideKeyword(shorthand) {
		return shorthand
	}

	layers, ok := parseMaskShorthand(shorthand)
	if ok {
		return maskShorthandPropertyValue(layers, prop)
	}

	return maskInitialValue(prop)
}

func maskParentValue(parentField *string) string {
	if parentField == nil {
		return ""
	}

	return *parentField
}

func maskStylePropertyFor(style, parent *ResolvedStyle, prop string) (maskStyleProperty, bool) {
	field := maskStyleField(style, prop)

	initial, ok := maskStyleInitial(prop)
	if !ok {
		return maskStyleProperty{field: nil, parentField: nil, initial: "", lower: false}, false
	}

	var parentField *string
	if parent != nil {
		parentField = maskStyleField(parent, prop)
	}

	return maskStyleProperty{
		field:       field,
		parentField: parentField,
		initial:     initial,
		lower:       maskStyleLowercase(prop),
	}, true
}

func maskStyleField(style *ResolvedStyle, prop string) *string {
	switch prop {
	case maskTypeProperty:
		return &style.MaskType
	default:
		return maskStyleLayerField(style, prop)
	}
}

func maskStyleLayerField(style *ResolvedStyle, prop string) *string {
	switch prop {
	case maskImageProperty:
		return &style.MaskImage
	case maskModeProperty:
		return &style.MaskMode
	case maskPositionProperty:
		return &style.MaskPosition
	case maskSizeProperty:
		return &style.MaskSize
	case maskRepeatProperty:
		return &style.MaskRepeat
	case maskOriginProperty:
		return &style.MaskOrigin
	case maskClipProperty:
		return &style.MaskClip
	case maskCompositeProperty:
		return &style.MaskComposite
	default:
		return nil
	}
}

func maskStyleInitial(prop string) (string, bool) {
	switch prop {
	case maskImageProperty:
		return "none", true
	case maskModeProperty:
		return maskModeMatchSource, true
	case maskPositionProperty:
		return maskInitialPosition, true
	case maskSizeProperty:
		return "auto", true
	case maskRepeatProperty:
		return "repeat", true
	case maskOriginProperty, maskClipProperty:
		return "border-box", true
	case maskCompositeProperty:
		return maskCompositeAdd, true
	case maskTypeProperty:
		return maskModeLuminance, true
	default:
		return "", false
	}
}

func maskStyleLowercase(prop string) bool {
	switch prop {
	case maskModeProperty, maskCompositeProperty, maskRepeatProperty,
		maskOriginProperty, maskClipProperty, maskTypeProperty:
		return true
	default:
		return false
	}
}

func maskInitialValue(prop string) string {
	initial, _ := maskStyleInitial(prop)

	return initial
}
