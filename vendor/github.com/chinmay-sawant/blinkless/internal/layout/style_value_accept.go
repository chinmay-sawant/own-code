package layout

import (
	"strconv"
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/css"
)

// Property-name and keyword constants used by the acceptance table.
const (
	propDisplay   = "display"
	propPosition  = "position"
	propOverflow  = "overflow"
	propOverflowX = "overflow-x"
	propOverflowY = "overflow-y"
	propHeight    = "height"
	propWidth     = "width"
	propMinWidth  = "min-width"
	propMinHeight = "min-height"
	propMaxWidth  = "max-width"
	propMaxHeight = "max-height"

	propMarginTop    = "margin-top"
	propMarginRight  = "margin-right"
	propMarginBottom = "margin-bottom"
	propMarginLeft   = "margin-left"

	propPaddingTop    = "padding-top"
	propPaddingRight  = "padding-right"
	propPaddingBottom = "padding-bottom"
	propPaddingLeft   = "padding-left"

	propTextWrap      = "text-wrap"
	propTextWrapStyle = "text-wrap-style"

	propRowGap       = "row-gap"
	propColumnGap    = "column-gap"
	propPlaceContent = "place-content"
	propPlaceItems   = "place-items"
	propPlaceSelf    = "place-self"

	propHyphens             = "hyphens"
	propTextDecorationColor = "text-decoration-color"
	propBorderSpacing       = "border-spacing"
	propBorderCollapse      = "border-collapse"
	propTableLayout         = "table-layout"

	propBorderTopWidth    = "border-top-width"
	propBorderRightWidth  = "border-right-width"
	propBorderBottomWidth = "border-bottom-width"
	propBorderLeftWidth   = "border-left-width"
	propBorderTopColor    = "border-top-color"
	propBorderRightColor  = "border-right-color"
	propBorderBottomColor = "border-bottom-color"
	propBorderLeftColor   = "border-left-color"
	propBorderTopStyle    = "border-top-style"
	propBorderRightStyle  = "border-right-style"
	propBorderBottomStyle = "border-bottom-style"
	propBorderLeftStyle   = "border-left-style"

	propBackground         = "background"
	propBackgroundImage    = "background-image"
	propFlexFlow           = "flex-flow"
	propFont               = "font"
	propGridRow            = "grid-row"
	propGridRowEnd         = "grid-row-end"
	propGridTemplate       = "grid-template"
	propGrid               = "grid"
	propQuotes             = "quotes"
	propHyphenateCharacter = "hyphenate-character"

	sizeMaxContent = "max-content"
	sizeMinContent = "min-content"

	fontWeightBolder  = "bolder"
	fontWeightLighter = "lighter"

	// flexShorthandPairParts is the two-token flex shorthand arity (grow shrink).
	flexShorthandPairParts = 2
)

// supportedDeclaration reports whether the engine accepts one declaration as
// applicable. It is the single value-acceptance rule shared by the cascade
// gate in cascadeRaw and the @supports probe in engineSupportsProperty, so an
// invalid declaration neither outranks a valid earlier one nor satisfies a
// feature query.
//
// Three value classes pass with no property check:
//
//   - custom properties (--*), whose values resolve in mergeCustomProps;
//   - CSS-wide keywords (inherit, initial, unset, revert, revert-layer), which
//     every property accepts;
//   - values containing var(), which are valid at parse time and checked
//     again after substitution by the applier. A computed-value failure makes
//     the property unset; it must not revive an earlier declaration.
func supportedDeclaration(prop, value string) bool {
	if strings.HasPrefix(prop, "--") {
		return true
	}

	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(trimmed) || containsVarFunc(trimmed) {
		return true
	}

	if prop == webkitMaskCompositeProperty && !webkitMaskCompositeValueAccepted(trimmed) {
		return false
	}

	if accepted, handled := webkitMaskLegacyValueAccepted(prop, trimmed); handled {
		return accepted
	}

	effectiveProp := normalizeVendorPrefix(prop)
	if effectiveProp != prop {
		trimmed = strings.TrimSpace(remapWebkitValue(prop, value))
	}

	return declarationValueAccepted(effectiveProp, trimmed)
}

// cssWideKeyword reports whether value is a CSS-wide keyword. The style
// pipeline decides what each resolves to; acceptance is universal.
func cssWideKeyword(value string) bool {
	switch value {
	case inheritKeyword, cssKeywordInitial, cssKeywordUnset, cssKeywordRevert, "revert-layer":
		return true
	default:
		return false
	}
}

// declarationValueAccepted mirrors the value parser behind each property's
// apply arm. Handler ownership alone is not support: display:bogus reaches
// setDisplayKeyword, which ignores it, so the declaration must not win the
// cascade or satisfy @supports.
//
// A property without an entry is accepted. Its applier either takes the value
// as written (transforms, images, strings) or has a grammar this table does
// not model yet; rejecting a valid value would break layout, while the
// applier already drops an invalid one.
func declarationValueAccepted(prop, value string) bool {
	if accepted, handled := specialDeclarationValueAccepted(prop, value); handled {
		return accepted
	}

	switch prop {
	case propOverflow, propOverflowX, propOverflowY:
		_, ok := parseOverflowKeyword(value)

		return ok
	case "opacity":
		_, ok := parseOpacityValue(value)

		return ok
	case "z-index":
		return zIndexValueAccepted(value)
	default:
		return extendedDeclarationValueAccepted(prop, value)
	}
}

func specialDeclarationValueAccepted(prop, value string) (bool, bool) {
	if accepted, handled := animationDeclarationValueAccepted(prop, value); handled {
		return accepted, true
	}

	if accepted, handled := timelineDeclarationValueAccepted(prop, value); handled {
		return accepted, true
	}

	if accepted, handled := transitionDeclarationValueAccepted(prop, value); handled {
		return accepted, true
	}

	if accepted, handled := maskDeclarationValueAccepted(prop, value); handled {
		return accepted, true
	}

	if accepted, handled := declarationRegionsValueAccepted(prop, value); handled {
		return accepted, true
	}

	if accepted, handled := declarationFlowValueAccepted(prop, value); handled {
		return accepted, true
	}

	if accepted, handled := declarationBoxValueAccepted(prop, value); handled {
		return accepted, true
	}

	return declarationTextValueAccepted(prop, value)
}

func declarationFlowValueAccepted(prop, value string) (bool, bool) {
	switch prop {
	case propDisplay:
		return displayValueAccepted(value), true
	case propPosition:
		return keywordIn(value, positionStatic, positionRelative, positionAbsolute, positionFixed, positionSticky), true
	case "float":
		return keywordIn(value, floatLeft, floatRight, cssDisplayNone), true
	case clearKeyword:
		return keywordIn(value, floatLeft, floatRight, clearBoth, cssDisplayNone), true
	case "box-sizing":
		return keywordIn(value, "content-box", borderBox), true
	case "writing-mode":
		return keywordIn(value, writingModeHorizontalTB, writingModeVerticalRL, writingModeVerticalLR), true
	case "direction":
		return keywordIn(value, cssDirectionLTR, cssDirectionRTL), true
	case "visibility":
		return keywordIn(value, visibleKeyword, overflowHidden, borderCollapseValue), true
	default:
		return false, false
	}
}

func declarationBoxValueAccepted(prop, value string) (bool, bool) {
	if accepted, handled := declarationExtentValueAccepted(prop, value); handled {
		return accepted, true
	}

	return declarationSpacingValueAccepted(prop, value)
}

func declarationExtentValueAccepted(prop, value string) (bool, bool) {
	switch prop {
	case propWidth:
		return widthValueAccepted(value), true
	case propHeight:
		return heightValueAccepted(value), true
	case propMinWidth, propMinHeight:
		return minExtentValueAccepted(value), true
	case propMaxWidth, propMaxHeight:
		return boxLengthValueAccepted(value, cssDisplayNone), true
	default:
		return false, false
	}
}

func declarationSpacingValueAccepted(prop, value string) (bool, bool) {
	switch prop {
	case marginProperty:
		return boxShorthandValueAccepted(value, marginLengthValueAccepted), true
	case propMarginTop, propMarginRight, propMarginBottom, propMarginLeft,
		cssVerticalAlignTop, floatRight, cssVerticalAlignBottom, floatLeft:
		return marginLengthValueAccepted(value), true
	case paddingProperty:
		return boxShorthandValueAccepted(value, paddingLengthValueAccepted), true
	case propPaddingTop, propPaddingRight, propPaddingBottom, propPaddingLeft:
		return paddingLengthValueAccepted(value), true
	default:
		return false, false
	}
}

func declarationTextValueAccepted(prop, value string) (bool, bool) {
	if accepted, handled := declarationTextFontValueAccepted(prop, value); handled {
		return accepted, true
	}

	return declarationTextFlowValueAccepted(prop, value)
}

func declarationTextFontValueAccepted(prop, value string) (bool, bool) {
	switch prop {
	case "font-style":
		return keywordIn(value, contentNormal, cssFontStyleItalic, cssFontStyleOblique), true
	case "font-weight":
		return fontWeightValueAccepted(value), true
	case "text-transform":
		return keywordIn(value, textTransformNone, textTransformUppercase,
			textTransformLowercase, textTransformCapitalize), true
	case "text-align":
		return keywordIn(value, floatLeft, fxCenter, cssTextAlignJustify, floatRight, fxEnd, fxStart), true
	case propTextWrapStyle:
		return keywordIn(value, textWrapStyleAuto, textWrapStyleBalance,
			textWrapStyleStable, textWrapStylePretty, textWrapStyleAvoidShort), true
	default:
		return false, false
	}
}

func declarationTextFlowValueAccepted(prop, value string) (bool, bool) {
	switch prop {
	case propTextWrap:
		return textWrapShorthandValueAccepted(value), true
	case "white-space":
		return keywordIn(value, contentNormal, cssWhiteSpaceNowrap, cssWhiteSpacePre,
			cssWhiteSpacePreWrap, cssWhiteSpacePreLine), true
	case "vertical-align":
		return verticalAlignValueAccepted(value), true
	case "overflow-wrap", "word-wrap":
		return keywordIn(value, contentNormal, overflowWrapBreakWord, overflowWrapAnywhere, "break-spaces"), true
	case "word-break":
		return keywordIn(value, contentNormal, "break-all", "keep-all", overflowWrapBreakWord), true
	default:
		return false, false
	}
}

func maskDeclarationValueAccepted(prop, value string) (bool, bool) {
	if accepted, handled := maskLayerDeclarationAccepted(prop, value); handled {
		return accepted, true
	}

	if accepted, handled := maskBorderDeclarationAccepted(prop, value); handled {
		return accepted, true
	}

	return maskBoxDeclarationAccepted(prop, value)
}

func maskLayerDeclarationAccepted(prop, value string) (bool, bool) {
	switch prop {
	case maskShorthandProperty:
		return maskShorthandValueAccepted(value), true
	case maskImageProperty:
		return maskLayerListValueAccepted(value, maskImageLayerValueAccepted), true
	case maskModeProperty:
		return maskLayerListValueAccepted(value, maskModeLayerValueAccepted), true
	case maskPositionProperty:
		return maskLayerListValueAccepted(value, maskPositionLayerValueAccepted), true
	case maskSizeProperty:
		return maskLayerListValueAccepted(value, maskSizeLayerValueAccepted), true
	default:
		return false, false
	}
}

func maskBoxDeclarationAccepted(prop, value string) (bool, bool) {
	switch prop {
	case maskRepeatProperty:
		return maskLayerListValueAccepted(value, maskRepeatLayerValueAccepted), true
	case maskOriginProperty:
		return maskLayerListValueAccepted(value, maskOriginLayerValueAccepted), true
	case maskClipProperty:
		return maskLayerListValueAccepted(value, maskClipLayerValueAccepted), true
	case maskCompositeProperty:
		return maskLayerListValueAccepted(value, maskCompositeLayerValueAccepted), true
	case maskTypeProperty:
		return maskTypeValueAccepted(value), true
	default:
		return false, false
	}
}

// declarationRegionsValueAccepted models the named-flow, exclusion, masonry
// tolerance, and text-wrap-break value grammars. The appliers drop an invalid
// value, but the gate must reject it here too so a bogus declaration neither
// wins the cascade over an earlier valid one nor satisfies @supports.
func declarationRegionsValueAccepted(prop, value string) (bool, bool) {
	if accepted, handled := namedFlowDeclarationValueAccepted(prop, value); handled {
		return accepted, true
	}

	if accepted, handled := flowToleranceDeclarationValueAccepted(prop, value); handled {
		return accepted, true
	}

	if accepted, handled := exclusionDeclarationValueAccepted(prop, value); handled {
		return accepted, true
	}

	if accepted, handled := wrapBreakDeclarationValueAccepted(prop, value); handled {
		return accepted, true
	}

	return false, false
}

// displayValueAccepted is setDisplayKeyword's keyword set, shared so the
// setter and the acceptance predicate cannot drift.
func displayValueAccepted(value string) bool {
	switch value {
	case displayBlock, "inline", cssDisplayNone, displayListItem, displayTable, displayTableRow, displayTableCell,
		displayRowGroup, displayHeaderGroup, displayFooterGroup,
		cssDisplayInlineBlock, displayTableCaption, "table-column", "table-column-group",
		displayFlex, displayInlineFlex, displayGrid, displayInlineGrid, displaySubgrid, displayFlowRoot,
		"-webkit-box", "-webkit-inline-box":
		return true
	default:
		return false
	}
}

// widthValueAccepted mirrors setWidthValue, which also takes max-content,
// min-content, and the two-token calc form.
func widthValueAccepted(value string) bool {
	switch value {
	case sizeMaxContent, sizeMinContent, overflowAuto:
		return true
	}

	if _, _, ok := calcFlexWidth(value, 0); ok {
		return true
	}

	return boxLengthValueAccepted(value, overflowAuto)
}

// heightValueAccepted mirrors setHeightValue, which takes neither the
// intrinsic keywords nor the two-token calc form.
func heightValueAccepted(value string) bool {
	if value == overflowAuto {
		return true
	}

	return boxLengthValueAccepted(value, overflowAuto)
}

// minExtentValueAccepted mirrors setMinWidthValue and setMinHeightValue,
// which take auto plus the max-extent length grammar (none is valid there).
func minExtentValueAccepted(value string) bool {
	if value == overflowAuto {
		return true
	}

	return boxLengthValueAccepted(value, cssDisplayNone)
}

// boxLengthValueAccepted accepts the length forms lengthBox parses for box
// sizing: vmin/vmax, dvh/svh/lvh, math functions, and CSS lengths. The probe
// context is all zeroes; acceptance must not depend on the element's resolved
// font size or containing block.
func boxLengthValueAccepted(value, autoValue string) bool {
	if value == autoValue {
		return true
	}

	_, ok := lengthBox(value, 0, 0, autoValue)

	return ok
}

// marginLengthValueAccepted mirrors marginLen for margins and inset offsets:
// auto plus every length form marginLen parses.
func marginLengthValueAccepted(value string) bool {
	if value == overflowAuto {
		return true
	}

	return lengthValueAccepted(value)
}

// paddingLengthValueAccepted mirrors the padding appliers, which take lengths
// only. marginLen maps auto to 0, but auto is not a padding value, so the
// declaration is rejected instead of resetting the side.
func paddingLengthValueAccepted(value string) bool {
	if value == overflowAuto {
		return false
	}

	return lengthValueAccepted(value)
}

// lengthValueAccepted accepts the forms marginLen parses: vmin/vmax, math
// functions, and CSS lengths (including percentages).
func lengthValueAccepted(value string) bool {
	if _, ok := vminVmaxPt(value, 0, 0); ok {
		return true
	}

	if _, ok := calcLength(value, 0, 0); ok {
		return true
	}

	_, _, ok := css.ParseLength(value)

	return ok
}

// boxShorthandValueAccepted checks 1-4 space-separated tokens of a margin or
// padding shorthand against the longhand predicate.
func boxShorthandValueAccepted(value string, tokenAccepted func(string) bool) bool {
	var tokens [4]string

	count := splitSpaceTokens(value, tokens[:])
	if count < 1 || count > len(tokens) {
		return false
	}

	for idx := range count {
		if !tokenAccepted(tokens[idx]) {
			return false
		}
	}

	return true
}

// textWrapShorthandValueAccepted mirrors setTextWrap: one or two tokens from
// the text-wrap-mode and text-wrap-style keyword sets, at most one of each.
// pretty and avoid-short-last-line are rejected because only auto, balance,
// and stable have layout behavior (inline_balance.go and the catalog row).
func textWrapShorthandValueAccepted(value string) bool {
	var tokens [2]string

	count := splitSpaceTokens(value, tokens[:])
	if count < 1 || count > len(tokens) {
		return false
	}

	modeSeen, styleSeen := false, false

	for idx := range count {
		switch tokens[idx] {
		case fxWrap, cssWhiteSpaceNowrap:
			if modeSeen {
				return false
			}

			modeSeen = true
		case textWrapStyleAuto, textWrapStyleBalance, textWrapStyleStable:
			if styleSeen {
				return false
			}

			styleSeen = true
		default:
			return false
		}
	}

	return true
}

// fontWeightValueAccepted mirrors resolveFontWeight.
func fontWeightValueAccepted(value string) bool {
	switch value {
	case contentNormal, cssFontWeightBold, fontWeightBolder, fontWeightLighter:
		return true
	}

	weight, ok := css.ParseNumber(value)

	return ok && weight >= 100 && weight <= 900
}

// verticalAlignValueAccepted mirrors setVerticalAlignValue: keywords,
// percentages, and lengths.
func verticalAlignValueAccepted(value string) bool {
	trimmed := strings.TrimSpace(value)
	low := strings.ToLower(trimmed)

	switch low {
	case "baseline", cssVerticalAlignTop, "middle", cssVerticalAlignBottom, verticalAlignSub, verticalAlignSuper:
		return true
	}

	if strings.HasSuffix(low, "%") {
		_, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(trimmed, "%")), 64)

		return err == nil
	}

	_, ok := plainLength(value, 0, 0)

	return ok
}

// zIndexValueAccepted mirrors setZIndexValue.
func zIndexValueAccepted(value string) bool {
	if value == overflowAuto {
		return true
	}

	_, err := strconv.Atoi(strings.TrimSpace(value))

	return err == nil
}

// keywordIn reports whether value equals one of the accepted keywords.
func keywordIn(value string, accepted ...string) bool {
	for _, keyword := range accepted {
		if value == keyword {
			return true
		}
	}

	return false
}

// extendedDeclarationValueAccepted models the value grammar of the color,
// background, border, outline, font-size, and transform handlers. It continues
// the table above: a property with no entry in either switch keeps the
// default-accept rule described on declarationValueAccepted.
//
//nolint:cyclop // flat per-property acceptance table reads clearer than a split dispatch
func extendedDeclarationValueAccepted(prop, value string) bool {
	if accepted, handled := cornerDeclarationValueAccepted(prop, value); handled {
		return accepted
	}

	switch prop {
	case "color", "outline-color", propTextDecorationColor:
		return colorValueAccepted(value)
	case "background-color":
		return backgroundColorValueAccepted(value)
	case propBackground:
		return backgroundValueAccepted(value)
	case propBackgroundImage:
		return backgroundImageValueAccepted(value)
	case borderProperty, borderTopProperty, borderRightProperty, borderBottomProperty, borderLeftProperty,
		borderColorKeyword,
		propBorderTopColor, propBorderRightColor, propBorderBottomColor, propBorderLeftColor,
		borderStyleKeyword,
		propBorderTopStyle, propBorderRightStyle, propBorderBottomStyle, propBorderLeftStyle,
		borderWidthKeyword,
		propBorderTopWidth, propBorderRightWidth, propBorderBottomWidth, propBorderLeftWidth:
		return borderValueAccepted(prop, value)
	case "border-radius":
		return borderRadiusValueAccepted(value)
	case "outline":
		return outlineShorthandValueAccepted(value)
	case "outline-style":
		return outlineStyleValueAccepted(value)
	case "outline-width":
		return borderWidthValueAccepted(value)
	case "outline-offset":
		_, ok := plainLength(value, 0, 0)

		return ok
	case "font-size":
		return fontSizeValueAccepted(value)
	case propFont:
		return fontShorthandValueAccepted(value)
	case "transform":
		return transformValueAccepted(value)
	case "perspective":
		return perspectiveValueAccepted(value)
	case "perspective-origin":
		return perspectiveOriginValueAccepted(value)
	case "line-height":
		return lineHeightValueAccepted(value)
	default:
		return layoutDeclarationValueAccepted(prop, value)
	}
}

// cornerDeclarationValueAccepted models the corner-shape family for the
// cascade gate and @supports probe: the 8 single-corner shape longhands, the
// 8 pair longhands, corner-shape, corner, and the 17 radius-plus-shape
// shorthands. handled reports whether prop belongs to the family.
func cornerDeclarationValueAccepted(prop, value string) (bool, bool) {
	switch prop {
	case propCornerTopLeftShape, propCornerTopRightShape, propCornerBottomRightShape,
		propCornerBottomLeftShape, propCornerStartStartShape, propCornerStartEndShape,
		propCornerEndStartShape, propCornerEndEndShape:
		return cornerShapeValueAccepted(value), true
	case propCornerTopShape, propCornerBottomShape, propCornerLeftShape, propCornerRightShape,
		propCornerBlockStartShape, propCornerBlockEndShape, propCornerInlineStartShape,
		propCornerInlineEndShape:
		return cornerShapePairValueAccepted(value), true
	case propCornerShape:
		return cornerShapeSidesValueAccepted(value), true
	case propCornerShorthand:
		return cornerTopLevelValueAccepted(value), true
	case propCornerTop, propCornerRight, propCornerBottom, propCornerLeft,
		propCornerBlockStart, propCornerBlockEnd, propCornerInlineStart, propCornerInlineEnd:
		return cornerSideShorthandValueAccepted(value), true
	case propCornerTopLeft, propCornerTopRight, propCornerBottomRight, propCornerBottomLeft,
		propCornerStartStart, propCornerStartEnd, propCornerEndStart, propCornerEndEnd:
		return cornerSingleShorthandValueAccepted(value), true
	default:
		return false, false
	}
}

// borderValueAccepted mirrors the border shorthand and longhand appliers,
// split out of extendedDeclarationValueAccepted to hold its statement count.
func borderValueAccepted(prop, value string) bool {
	switch prop {
	case borderColorKeyword,
		propBorderTopColor, propBorderRightColor, propBorderBottomColor, propBorderLeftColor:
		return colorListValueAccepted(value)
	case borderStyleKeyword,
		propBorderTopStyle, propBorderRightStyle, propBorderBottomStyle, propBorderLeftStyle:
		return borderStyleValueAccepted(value)
	case borderWidthKeyword,
		propBorderTopWidth, propBorderRightWidth, propBorderBottomWidth, propBorderLeftWidth:
		return borderWidthValueAccepted(value)
	default:
		return borderShorthandValueAccepted(value)
	}
}

// layoutDeclarationValueAccepted models the flex/grid keyword families, the
// logical box and inset families, and the remaining text/table handlers whose
// setters drop invalid values. It continues extendedDeclarationValueAccepted;
// an unlisted property stays default-accept.
//
//nolint:cyclop,funlen,gocyclo,maintidx // flat per-property acceptance table reads clearer than a split dispatch
func layoutDeclarationValueAccepted(prop, value string) bool {
	switch prop {
	case "align-items":
		return alignItemsValueAccepted(value)
	case "justify-content":
		return justifyContentValueAccepted(value)
	case "flex-direction":
		return keywordIn(value, fxRow, fxCol, fxRowRev, fxColRev)
	case propFlexFlow:
		return flexFlowValueAccepted(value)
	case flexKeyword:
		return flexShorthandValueAccepted(value)
	case "flex-grow":
		number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)

		return err == nil && number >= 0
	case "order":
		_, err := strconv.Atoi(strings.TrimSpace(value))

		return err == nil
	case gapKeyword:
		return gapValueAccepted(value, true)
	case propRowGap:
		return gapValueAccepted(value, false)
	case propColumnGap:
		return gapValueAccepted(value, true)
	case propPlaceContent:
		return placeValueAccepted(value, alignContentValueAccepted, justifyContentValueAccepted)
	case propPlaceItems:
		return placeValueAccepted(value, alignItemsValueAccepted, justifyItemsValueAccepted)
	case propPlaceSelf:
		return placeValueAccepted(value, alignSelfValueAccepted, justifySelfValueAccepted)
	case propGridRow:
		return gridRowValueAccepted(value)
	case gridRowStartProp:
		return gridRowStartValueAccepted(value)
	case propGridRowEnd:
		return gridRowEndValueAccepted(value)
	case propGridTemplate:
		return gridTemplateShorthandValueAccepted(value)
	case propGrid:
		return gridShorthandValueAccepted(value)
	case cssPropMarginBlock, cssPropMarginInline:
		return logicalPairValueAccepted(value, marginLengthValueAccepted)
	case propMarginBlockStart, propMarginBlockEnd, propMarginInlineStart, propMarginInlineEnd:
		return marginLengthValueAccepted(value)
	case cssPropPaddingBlock, cssPropPaddingInline:
		return logicalPairValueAccepted(value, paddingLengthValueAccepted)
	case propPaddingBlockStart, propPaddingBlockEnd, propPaddingInlineStart, propPaddingInlineEnd:
		return paddingLengthValueAccepted(value)
	case containerInlineSize, propBlockSize:
		// block-size/inline-size map onto the width or height grammar
		// depending on writing mode; accept either so a valid declaration in
		// one mode is never rejected.
		return widthValueAccepted(value) || heightValueAccepted(value)
	case propMinInlineSize, propMinBlockSize:
		return minExtentValueAccepted(value)
	case propMaxInlineSize, propMaxBlockSize:
		return boxLengthValueAccepted(value, cssDisplayNone)
	case insetKeyword:
		return boxShorthandValueAccepted(value, marginLengthValueAccepted)
	case cssPropInsetBlock, cssPropInsetInline:
		return logicalPairValueAccepted(value, marginLengthValueAccepted)
	case cssPropInsetBlockStart, cssPropInsetBlockEnd, cssPropInsetInlineStart, cssPropInsetInlineEnd:
		return marginLengthValueAccepted(value)
	case "text-indent":
		return lengthValueAccepted(value)
	case "word-spacing":
		return value == contentNormal || lengthValueAccepted(value)
	case propHyphens:
		return keywordIn(strings.ToLower(strings.TrimSpace(value)),
			cssDisplayNone, hyphenationManual, overflowAuto)
	case propHyphenateCharacter:
		return hyphenateCharacterValueAccepted(value)
	case propQuotes:
		return quotesValueAccepted(value)
	case cssPropCounterIncrement, cssPropCounterReset, cssPropCounterSet:
		return counterListValueAccepted(value)
	case "table-layout":
		return keywordIn(value, positionFixed, overflowAuto)
	case "border-collapse":
		return keywordIn(value, borderCollapseValue, "separate")
	case propBorderSpacing:
		return logicalPairValueAccepted(value, lengthValueAccepted)
	case "unicode-bidi":
		return isUnicodeBidiValue(strings.ToLower(strings.TrimSpace(value)))
	case "text-decoration":
		return keywordIn(value, cssTextDecorationUnderline, cssTextDecorationLineThrough, cssDisplayNone)
	case "content-visibility":
		return keywordIn(strings.ToLower(strings.TrimSpace(value)),
			contentVisibilityVisible, contentVisibilityAuto, contentVisibilityHidden)
	case "color-adjust", "print-color-adjust":
		_, ok := normalizeColorAdjust(value)

		return ok
	case "hyphenate-limit-chars":
		return hyphenateLimitCharsValueAccepted(value)
	case "hyphenate-limit-last":
		return keywordIn(strings.ToLower(strings.TrimSpace(value)),
			cssDisplayNone, pageBreakAlways, floatRefColumn, pageKeyword, "spread")
	case "hyphenate-limit-lines":
		return hyphenateLimitLinesValueAccepted(value)
	case "hyphenate-limit-zone":
		return hyphenateLimitZoneValueAccepted(value)
	case "row-rule-width":
		return rowRuleListValueAccepted(value, rowRuleWidthTokenAccepted)
	case "row-rule-style":
		return rowRuleListValueAccepted(value, rowRuleStyleTokenAccepted)
	case "row-rule-color":
		return rowRuleListValueAccepted(value, rowRuleColorTokenAccepted)
	case "row-rule":
		return outlineShorthandValueAccepted(value)
	case "row-rule-break", "column-rule-break", "rule-break":
		return rowRuleKeywordList(value, cssDisplayNone, contentNormal, "intersection")
	case "row-rule-visibility-items", "rule-visibility-items":
		return rowRuleKeywordList(value, "all", "around", "between", contentNormal)
	case "rule-overlap":
		return rowRuleKeywordList(value, "row-over-column", "column-over-row")
	case "row-rule-inset-cap-start", "row-rule-inset-cap-end",
		"row-rule-inset-junction-start", "row-rule-inset-junction-end",
		"column-rule-inset-cap-start", "column-rule-inset-cap-end",
		"column-rule-inset-junction-start", "column-rule-inset-junction-end":
		return rowRuleInsetTokenAccepted(strings.TrimSpace(value))
	case "row-rule-inset", "row-rule-inset-cap", "row-rule-inset-junction",
		"row-rule-inset-start", "row-rule-inset-end",
		"rule", "rule-width", "rule-style", "rule-color",
		"rule-inset", "rule-inset-cap", "rule-inset-start", "rule-inset-end", "rule-inset-junction":
		_, ok := expandGapRuleDeclaration(prop, value)

		return ok
	case anchorNameProp:
		return anchorNameValueAccepted(value)
	case anchorScopeProp:
		return anchorScopeValueAccepted(value)
	case positionAnchorProp:
		return anchorPositionAnchorValueAccepted(value)
	case positionAreaProp:
		return anchorPositionAreaValueAccepted(value)
	case positionTryFallbacks:
		return anchorFallbacksValueAccepted(value)
	case positionTryOrderProp:
		return anchorOrderValueAccepted(value)
	case positionTryProp:
		return anchorTryValueAccepted(value)
	case positionVisibilityProp:
		return anchorVisibilityValueAccepted(value)
	default:
		return true
	}
}

// colorValueAccepted mirrors the color arms: a parsed color or currentColor.
func colorValueAccepted(value string) bool {
	_, ok := parseUsedColor(value, [3]float64{})

	return ok
}

// backgroundColorValueAccepted mirrors the background-color store: legacy
// colors plus wide-gamut spellings that keep headroom for
// dynamic-range-limit.
func backgroundColorValueAccepted(value string) bool {
	if _, _, _, _, ok := css.ParseColor(value); ok {
		return true
	}

	_, _, ok := css.ParseColorWide(value)

	return ok
}

// backgroundValueAccepted mirrors expandBackgroundDeclaration: the shorthand
// must carry a color or an image the engine reads. The other components
// (repeat, position, attachment) are not parsed from the shorthand, so a
// value carrying only those is rejected.
func backgroundValueAccepted(value string) bool {
	if _, ok := firstBackgroundColorToken(value); ok {
		return true
	}

	return hasBackgroundImage(value)
}

// backgroundImageValueAccepted mirrors applyBackgroundImageValue and the
// painter: none, a url(), or a gradient, checked per comma-separated layer.
func backgroundImageValueAccepted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if strings.EqualFold(trimmed, cssDisplayNone) {
		return true
	}

	for _, raw := range splitCommaLayers(trimmed) {
		layer := strings.TrimSpace(raw)
		if layer == "" {
			return false
		}

		if strings.EqualFold(layer, cssDisplayNone) || isGradientFunc(layer) {
			continue
		}

		if _, ok := firstCSSUrl(layer); !ok {
			return false
		}
	}

	return true
}

// colorListValueAccepted checks 1-4 color tokens against parseUsedColorAlpha,
// the border-color shorthand grammar.
func colorListValueAccepted(value string) bool {
	var tokens [4]string

	count := splitSpaceTokens(value, tokens[:])
	if count < 1 || count > len(tokens) {
		return false
	}

	for idx := range count {
		if _, _, ok := parseUsedColorAlpha(tokens[idx], [3]float64{}); !ok {
			return false
		}
	}

	return true
}

// borderShorthandValueAccepted requires every token of border or border-<side>
// to be a style, width, or color the parser understands. parseBorder silently
// defaults an unknown token to a medium solid border, so the gate rejects them
// instead of letting the declaration reset an earlier border.
func borderShorthandValueAccepted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if strings.EqualFold(trimmed, cssDisplayNone) || trimmed == "0" {
		return true
	}

	recognized := false

	for start := 0; ; {
		token, next, ok := nextSpaceToken(trimmed, start)
		if !ok {
			break
		}

		if !borderTokenRecognized(token) {
			return false
		}

		recognized = true
		start = next
	}

	return recognized
}

func borderTokenRecognized(token string) bool {
	switch token {
	case solidKeyword, borderStyleDashed, borderStyleDotted, cssDisplayNone, overflowHidden,
		thinKeyword, mediumKeyword, thickKeyword:
		return true
	}

	if isCurrentColor(token) {
		return true
	}

	if _, _, _, _, ok := css.ParseColor(token); ok {
		return true
	}

	_, _, ok := css.ParseLength(token)

	return ok
}

// borderWidthValueAccepted mirrors borderWidth and parseOutlineWidth: a width
// keyword or a CSS length.
func borderWidthValueAccepted(value string) bool {
	if keywordIn(value, thinKeyword, mediumKeyword, thickKeyword) {
		return true
	}

	_, _, ok := css.ParseLength(value)

	return ok
}

// borderStyleValueAccepted mirrors setFourBorderStyle and setBorderStyleSide,
// which lowercase the value and recognize solid, dashed, dotted, none, and
// hidden (hidden degrades to none).
func borderStyleValueAccepted(value string) bool {
	return keywordIn(strings.ToLower(strings.TrimSpace(value)),
		solidKeyword, borderStyleDashed, borderStyleDotted, cssDisplayNone, overflowHidden)
}

// outlineStyleValueAccepted mirrors parseOutlineStyle.
func outlineStyleValueAccepted(value string) bool {
	return keywordIn(strings.ToLower(strings.TrimSpace(value)),
		solidKeyword, borderStyleDashed, borderStyleDotted, cssDisplayNone)
}

// borderRadiusValueAccepted checks the 1-4 token lists on each side of the
// optional slash, rejecting negative and unparseable radii that setBorderRadius
// silently drops.
func borderRadiusValueAccepted(value string) bool {
	horiz, vert, hasVert := splitRadiusSlash(value)
	if horiz == "" || !radiusSideValueAccepted(horiz) {
		return false
	}

	if hasVert && (vert == "" || !radiusSideValueAccepted(vert)) {
		return false
	}

	return true
}

func radiusSideValueAccepted(value string) bool {
	count := 0

	for start := 0; ; {
		token, next, ok := nextSpaceToken(value, start)
		if !ok {
			break
		}

		count++
		if count > borderRadiusValueCount {
			return false
		}

		if percent, unit, parsed := css.ParseLength(token); parsed && unit == "%" {
			if percent < 0 {
				return false
			}

			start = next

			continue
		}

		radius, parsed := lengthBox(token, 0, 0, cssDisplayNone)
		if !parsed || radius < 0 {
			return false
		}

		start = next
	}

	return count > 0
}

// outlineShorthandValueAccepted mirrors parseRuleShorthand strictly: every
// token must be a style, width, or color, and at least one must be present.
// parseRuleShorthand defaults unknown tokens to its initial values, so the
// gate rejects them instead of letting the declaration reset an earlier
// outline.
func outlineShorthandValueAccepted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if strings.EqualFold(trimmed, cssDisplayNone) {
		return true
	}

	recognized := false

	for start := 0; ; {
		token, next, ok := nextSpaceToken(trimmed, start)
		if !ok {
			break
		}

		if !outlineTokenRecognized(token) {
			return false
		}

		recognized = true
		start = next
	}

	return recognized
}

// rowRuleListValueAccepted reports whether every space-separated token is
// accepted. Parentheses stay inside one token. An empty list is rejected so
// it cannot replace an earlier valid declaration.
func rowRuleListValueAccepted(value string, accept func(string) bool) bool {
	accepted := false

	for start := 0; ; {
		token, next, ok := nextSpaceToken(value, start)
		if !ok {
			break
		}

		if !accept(token) {
			return false
		}

		accepted = true
		start = next
	}

	return accepted
}

func rowRuleWidthTokenAccepted(token string) bool {
	_, ok := parseRowRuleWidthToken(token, 0)

	return ok
}

func rowRuleStyleTokenAccepted(token string) bool {
	_, ok := parseRowRuleStyleToken(token)

	return ok
}

func rowRuleColorTokenAccepted(token string) bool {
	_, _, ok := parseRowRuleColorToken(token, [3]float64{})

	return ok
}

func outlineTokenRecognized(token string) bool {
	if _, ok := parseOutlineStyle(token); ok {
		return true
	}

	if _, ok := parseOutlineWidth(token, 0); ok {
		return true
	}

	_, ok := parseUsedColor(token, [3]float64{})

	return ok
}

// fontSizeValueAccepted mirrors applyFontSizeValue: a font-size keyword, a
// math function, or a CSS length (percent, em, rem, and the rest).
func fontSizeValueAccepted(value string) bool {
	if _, ok := fontSizeKeyword(value, 0); ok {
		return true
	}

	if _, ok := clampLength(value, 0, 0); ok {
		return true
	}

	_, _, ok := css.ParseLength(value)

	return ok
}

// fontShorthandValueAccepted mirrors expandFontDeclaration plus the font-size
// grammar: optional style/variant/weight prefixes, then a readable font-size
// with an optional /line-height and any family tail. System font keywords
// (caption, icon, ...) are not readable by the engine and are rejected.
func fontShorthandValueAccepted(value string) bool {
	for _, tok := range strings.Fields(value) {
		if size, line, ok := strings.Cut(tok, "/"); ok {
			return fontSizeValueAccepted(size) && (line == "" || lineHeightValueAccepted(line))
		}

		lower := strings.ToLower(tok)

		if _, handled := fontPrefixDecl(lower); handled {
			continue
		}

		if isFontWeightNumber(tok) {
			continue
		}

		return fontSizeValueAccepted(tok)
	}

	return false
}

// transformValueAccepted mirrors applyTransformListValue: none or a list of
// transform functions parseTransformList understands.
func transformValueAccepted(value string) bool {
	if strings.EqualFold(strings.TrimSpace(value), cssDisplayNone) {
		return true
	}

	_, _, _, _, ok := parseTransformList(value, 0) //nolint:dogsled // the acceptance gate needs only the parse verdict

	return ok
}

// perspectiveValueAccepted mirrors applyPerspectiveProperty: none or a
// positive length.
func perspectiveValueAccepted(value string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" || trimmed == cssDisplayNone {
		return true
	}

	pt, _, isPct, ok := parseTransformLength(trimmed, 0)

	return ok && !isPct && pt > 0
}

// perspectiveOriginValueAccepted mirrors applyPerspectiveOriginProperty: one
// or two tokens from percentages, lengths, and the position keywords.
func perspectiveOriginValueAccepted(value string) bool {
	fields := strings.Fields(strings.ToLower(value))
	if len(fields) == 0 || len(fields) > 2 {
		return false
	}

	for _, tok := range fields {
		switch tok {
		case "left", "center", "right", "top", "bottom":
			continue
		}

		if strings.HasSuffix(tok, "%") {
			if _, ok := parseUnitless(strings.TrimSuffix(tok, "%")); ok {
				continue
			}

			return false
		}

		if pt, _, isPct, ok := parseTransformLength(tok, 0); !ok || isPct {
			_ = pt

			return false
		}
	}

	return true
}

// lineHeightValueAccepted mirrors lineHeight: normal, a number, or a length
// (percentages included).
func lineHeightValueAccepted(value string) bool {
	if value == contentNormal {
		return true
	}

	if _, ok := css.ParseNumber(value); ok {
		return true
	}

	_, _, ok := css.ParseLength(value)

	return ok
}

// alignItemsValueAccepted mirrors setAlignItemsValue.
func alignItemsValueAccepted(value string) bool {
	return keywordIn(value, fxStretch, flexStartKeyword, fxFlexEnd, fxCenter, fxStart, fxEnd, "baseline")
}

// alignContentValueAccepted mirrors setAlignContentValue.
func alignContentValueAccepted(value string) bool {
	return keywordIn(value, flexStartKeyword, fxFlexEnd, fxCenter, fxBetween, fxAround,
		fxEvenly, fxStretch, fxStart, fxEnd)
}

// alignSelfValueAccepted mirrors setAlignSelfValue.
func alignSelfValueAccepted(value string) bool {
	return keywordIn(value, overflowAuto, fxStretch, flexStartKeyword, fxFlexEnd, fxCenter, fxStart, fxEnd, "baseline")
}

// justifyContentValueAccepted mirrors setJustifyContentValue.
func justifyContentValueAccepted(value string) bool {
	return keywordIn(value, flexStartKeyword, fxFlexEnd, fxCenter, fxBetween, fxAround, fxEvenly, fxStart, fxEnd)
}

// justifyItemsValueAccepted mirrors setJustifyItemsValue.
func justifyItemsValueAccepted(value string) bool {
	return keywordIn(value, fxStretch, fxStart, fxEnd, fxCenter, flexStartKeyword, fxFlexEnd)
}

// justifySelfValueAccepted mirrors setJustifySelfValue.
func justifySelfValueAccepted(value string) bool {
	return keywordIn(value, overflowAuto, fxStretch, fxStart, fxEnd, fxCenter, flexStartKeyword, fxFlexEnd)
}

// placeValueAccepted checks one or two tokens: the declaration applies when
// either half lands on its longhand, matching the place-* expansion.
func placeValueAccepted(value string, align, justify func(string) bool) bool {
	first, second, ok := splitPlacePair(value)
	if !ok {
		return false
	}

	return align(first) || justify(second)
}

// flexShorthandValueAccepted mirrors parseFlexShorthand's token grammar
// (none, auto, grow, grow shrink, grow shrink basis). It is stricter than the
// parser for a partially valid pair such as "2 bogus": the parser would still
// write grow, but the declaration is invalid and must not win the cascade.
func flexShorthandValueAccepted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == cssDisplayNone || trimmed == overflowAuto {
		return true
	}

	parts := strings.Fields(trimmed)

	switch len(parts) {
	case 0:
		return false
	case 1:
		return flexNumberAccepted(parts[0]) || flexIsBasis(parts[0])
	case flexShorthandPairParts:
		return flexPairValueAccepted(parts[0], parts[1])
	default:
		return flexNumberAccepted(parts[0]) && flexNumberAccepted(parts[1]) && flexIsBasis(parts[2])
	}
}

// flexPairValueAccepted accepts "grow shrink" or "grow basis".
func flexPairValueAccepted(grow, second string) bool {
	return flexNumberAccepted(grow) && (flexNumberAccepted(second) || flexIsBasis(second))
}

func flexNumberAccepted(token string) bool {
	_, err := strconv.ParseFloat(token, 64)

	return err == nil
}

// flexFlowValueAccepted mirrors parseFlexFlow: one or more tokens from the
// direction and wrap keyword sets, in any order.
func flexFlowValueAccepted(value string) bool {
	found := false

	for _, tok := range strings.Fields(value) {
		switch tok {
		case fxRow, fxCol, fxRowRev, fxColRev, cssWhiteSpaceNowrap, fxWrap, fxWrapRev:
			found = true
		default:
			return false
		}
	}

	return found
}

// gridRowValueAccepted mirrors parseGridRow for the grid-line forms the
// engine reads: auto, -1, a nonzero integer, span N, or a start / end pair.
func gridRowValueAccepted(value string) bool {
	clean := strings.TrimSpace(stripGridLineNames(value))
	if clean == "" || isGridAutoToken(clean) {
		return true
	}

	parts := strings.Split(clean, "/")
	if len(parts) == 1 {
		return gridLineEndTokenAccepted(strings.TrimSpace(parts[0]))
	}

	if len(parts) == two {
		start := strings.TrimSpace(parts[0])
		end := strings.TrimSpace(parts[1])

		return start != "" && end != "" &&
			gridLineStartTokenAccepted(start) && gridLineEndTokenAccepted(end)
	}

	return false
}

// gridRowStartValueAccepted mirrors setGridStartIndex: auto, -1, or a nonzero
// integer after [name] line names are stripped.
func gridRowStartValueAccepted(value string) bool {
	clean := strings.TrimSpace(stripGridLineNames(value))
	if clean == "" || isGridAutoToken(clean) {
		return true
	}

	return gridLineStartTokenAccepted(clean)
}

// gridRowEndValueAccepted mirrors applyGridEndOnly: auto, -1, a nonzero
// integer, or "span N" with a positive N, after [name] stripping.
func gridRowEndValueAccepted(value string) bool {
	clean := strings.TrimSpace(stripGridLineNames(value))
	if clean == "" || isGridAutoToken(clean) {
		return true
	}

	return gridLineEndTokenAccepted(clean)
}

// gridLineStartTokenAccepted reports whether token is a start grid line the
// engine reads: auto, -1, or a nonzero integer.
func gridLineStartTokenAccepted(token string) bool {
	if isGridAutoToken(token) {
		return true
	}

	n, err := strconv.Atoi(token)

	return err == nil && n != 0
}

// gridLineEndTokenAccepted reports whether token is an end grid line the
// engine reads: auto, -1, a nonzero integer, or span N with N > 0.
func gridLineEndTokenAccepted(token string) bool {
	if isGridAutoToken(token) {
		return true
	}

	if span, ok := strings.CutPrefix(token, "span "); ok {
		n, err := strconv.Atoi(strings.TrimSpace(span))

		return err == nil && n > 0
	}

	n, err := strconv.Atoi(token)

	return err == nil && n != 0
}

// maxGridTrackNesting caps repeat() recursion in the acceptance grammar so a
// pathological nested value cannot recurse without bound.
const maxGridTrackNesting = 8

// gridTemplateShorthandValueAccepted mirrors parseGridTemplateShorthand:
// none, a masonry value the setter skips, the areas form, or a rows
// [/ columns] pair of track lists.
func gridTemplateShorthandValueAccepted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}

	if strings.EqualFold(trimmed, cssDisplayNone) || gridValueHasMasonry(trimmed) {
		return true
	}

	before, after, hasSlash := splitGridTemplateSlash(trimmed)
	if before == "" || (hasSlash && after == "") {
		return false
	}

	if gridPartHasAreaString(before) {
		return gridAreaTemplateAccepted(before) && gridTrackListAfterSlashAccepted(after, hasSlash)
	}

	return gridTrackListAccepted(before) && gridTrackListAfterSlashAccepted(after, hasSlash)
}

// gridShorthandValueAccepted mirrors parseGridShorthand: none, a masonry value
// the setter skips, the grid-template form, or an auto-flow form.
func gridShorthandValueAccepted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}

	if strings.EqualFold(trimmed, cssDisplayNone) || gridValueHasMasonry(trimmed) {
		return true
	}

	before, after, hasSlash := splitGridTemplateSlash(trimmed)
	if before == "" || (hasSlash && after == "") {
		return false
	}

	beforeFlow := gridPartHasAutoFlow(before)
	afterFlow := gridPartHasAutoFlow(after)

	if !beforeFlow && !afterFlow {
		return gridTemplateShorthandValueAccepted(trimmed)
	}

	return gridAutoFlowShorthandAccepted(before, after, hasSlash, beforeFlow, afterFlow)
}

// gridAutoFlowShorthandAccepted validates the auto-flow forms of the grid
// shorthand: exactly one side carries auto-flow, the slash is required, and
// the other side is a track list.
func gridAutoFlowShorthandAccepted(before, after string, hasSlash, beforeFlow, afterFlow bool) bool {
	if !hasSlash || (beforeFlow && afterFlow) {
		return false
	}

	if beforeFlow {
		return gridAutoFlowSideAccepted(before) && gridTrackListAccepted(after)
	}

	return gridTrackListAccepted(before) && gridAutoFlowSideAccepted(after)
}

// gridAutoFlowSideAccepted validates the auto-flow side of the grid shorthand:
// the auto-flow keyword, an optional dense, and the optional auto track sizes
// the engine stores as ignored but CSS allows.
func gridAutoFlowSideAccepted(part string) bool {
	seenFlow, seenDense := false, false

	for _, token := range splitGridTrackTokens(part) {
		switch strings.ToLower(token) {
		case "auto-flow":
			if seenFlow {
				return false
			}

			seenFlow = true
		case gridFlowDense:
			if seenDense {
				return false
			}

			seenDense = true
		default:
			if !gridTrackSizeAccepted(token, 0) {
				return false
			}
		}
	}

	return seenFlow
}

// gridTrackListAfterSlashAccepted validates the optional columns side of the
// template shorthands.
func gridTrackListAfterSlashAccepted(after string, hasSlash bool) bool {
	if !hasSlash {
		return true
	}

	return gridTrackListAccepted(after)
}

// gridAreaTemplateAccepted validates the areas side of grid-template: quoted
// area rows, each optionally followed by a track size, with [line names]
// allowed between them. A track size before the first area row is rejected.
func gridAreaTemplateAccepted(part string) bool {
	sawArea := false

	for idx := 0; idx < len(part); {
		for idx < len(part) && isCSSSpace(part[idx]) {
			idx++
		}

		if idx >= len(part) {
			break
		}

		next, area, ok := scanGridAreaTemplateToken(part, idx, sawArea)
		if !ok {
			return false
		}

		sawArea = sawArea || area
		idx = next
	}

	return sawArea
}

// scanGridAreaTemplateToken consumes one areas-form token: a quoted area row,
// a [line names] group, or a track size after the first area row. It returns
// the next index, whether the token was an area row, and whether it is valid.
func scanGridAreaTemplateToken(part string, idx int, sawArea bool) (int, bool, bool) {
	switch part[idx] {
	case '"', '\'':
		end := scanQuotedGridToken(part, idx)
		if end <= idx+1 || part[end-1] != part[idx] {
			return idx, false, false
		}

		return end, true, true
	case '[':
		end := skipGridLineNames(part, idx)
		if end <= idx+1 || part[end-1] != ']' {
			return idx, false, false
		}

		return end, false, true
	default:
		end := scanGridTemplateToken(part, idx)
		if !sawArea || !gridTrackSizeAccepted(part[idx:end], 0) {
			return idx, false, false
		}

		return end, false, true
	}
}

// scanGridTemplateToken returns the end of one unquoted grid-template token.
func scanGridTemplateToken(part string, pos int) int {
	end := pos
	for end < len(part) && !isCSSSpace(part[end]) &&
		part[end] != '"' && part[end] != '\'' && part[end] != '[' {
		end++
	}

	return end
}

// gridTrackListAccepted validates one <track-list>: whitespace-separated track
// sizes with optional [line names], or the lone none keyword. The forms mirror
// parseGridTrackDefs: auto, min-content, max-content, fr, non-negative lengths
// and percentages, minmax(), and repeat().
func gridTrackListAccepted(value string) bool {
	return gridTrackListAcceptedDepth(value, 0)
}

// gridTrackListAcceptedDepth is gridTrackListAccepted carrying the current
// repeat() nesting depth.
func gridTrackListAcceptedDepth(value string, depth int) bool {
	tokens := splitGridTrackTokens(value)
	if len(tokens) == 0 {
		return false
	}

	if len(tokens) == 1 && strings.EqualFold(tokens[0], cssDisplayNone) {
		return true
	}

	for _, token := range tokens {
		if isGridLineNamesToken(token) {
			continue
		}

		if !gridTrackSizeAccepted(token, depth) {
			return false
		}
	}

	return true
}

// gridTrackSizeAccepted mirrors parseTrackSize/parseOneTrackDef for one track
// token: keywords, fr, non-negative lengths and percentages, minmax(), and
// repeat().
func gridTrackSizeAccepted(token string, depth int) bool {
	if depth > maxGridTrackNesting {
		return false
	}

	tok := strings.TrimSpace(token)
	lower := strings.ToLower(tok)

	switch lower {
	case overflowAuto, sizeMinContent, sizeMaxContent:
		return true
	}

	if strings.HasPrefix(lower, "minmax(") && strings.HasSuffix(tok, ")") {
		return gridMinmaxTrackAccepted(tok)
	}

	if strings.HasPrefix(lower, "repeat(") && strings.HasSuffix(tok, ")") {
		return gridRepeatTrackAccepted(tok, depth)
	}

	return gridBareTrackSizeAccepted(lower)
}

// gridMinmaxTrackAccepted validates minmax(<size>, <size>) with exactly two
// top-level arguments.
func gridMinmaxTrackAccepted(tok string) bool {
	inner := tok[len("minmax(") : len(tok)-1]

	parts := splitTopLevelComma(inner)
	if len(parts) != two {
		return false
	}

	return gridBareTrackSizeAccepted(strings.ToLower(parts[0])) &&
		gridBareTrackSizeAccepted(strings.ToLower(parts[1]))
}

// gridRepeatTrackAccepted validates repeat(<count>, <track-list>): a positive
// integer or auto-fill/auto-fit, then a nested track list.
func gridRepeatTrackAccepted(tok string, depth int) bool {
	inner := tok[len("repeat(") : len(tok)-1]

	parts := splitTopLevelComma(inner)
	if len(parts) != two || !gridRepeatCountAccepted(parts[0]) {
		return false
	}

	return gridTrackListAcceptedDepth(parts[1], depth+1)
}

// gridRepeatCountAccepted mirrors expandRepeatFunctions' integer count and
// parseAutoFitDefs' auto-fit/auto-fill forms.
func gridRepeatCountAccepted(part string) bool {
	count := strings.TrimSpace(strings.ToLower(part))
	if count == "auto-fit" || count == "auto-fill" {
		return true
	}

	number, err := strconv.Atoi(count)

	return err == nil && number > 0
}

// gridBareTrackSizeAccepted accepts one leaf track size: a keyword, a
// non-negative fr coefficient, or a non-negative length or percentage. The
// token is already lowercased by the caller.
func gridBareTrackSizeAccepted(token string) bool {
	switch token {
	case overflowAuto, sizeMinContent, sizeMaxContent:
		return true
	}

	if strings.HasPrefix(token, "-") {
		return false
	}

	if factor, ok := strings.CutSuffix(token, "fr"); ok {
		number, err := strconv.ParseFloat(strings.TrimSpace(factor), 64)

		return err == nil && number >= 0
	}

	length, ok := lengthBox(token, 0, 0, "")

	return ok && length >= 0
}

// splitGridTrackTokens splits a track list on whitespace outside parentheses
// and brackets, so minmax()/repeat() arguments and [line names] stay intact.
func splitGridTrackTokens(value string) []string {
	var tokens []string

	var current strings.Builder

	depth := 0

	flush := func() {
		if current.Len() == 0 {
			return
		}

		tokens = append(tokens, current.String())
		current.Reset()
	}

	for idx := range len(value) {
		char := value[idx]

		switch {
		case char == '(' || char == '[':
			depth++

			current.WriteByte(char)
		case char == ')' || char == ']':
			depth = max(depth-1, 0)

			current.WriteByte(char)
		case isTrackWhitespace(char) && depth == 0:
			flush()
		default:
			current.WriteByte(char)
		}
	}

	flush()

	return tokens
}

// isGridLineNamesToken reports whether token is one or more bracketed line
// names, such as [main] or [content start].
func isGridLineNamesToken(token string) bool {
	if !strings.HasPrefix(token, "[") || !strings.HasSuffix(token, "]") {
		return false
	}

	depth := 0

	for idx := range len(token) {
		switch token[idx] {
		case '[':
			depth++
		case ']':
			depth--
			if depth < 0 {
				return false
			}
		}
	}

	return depth == 0
}

// gapValueAccepted mirrors the gap appliers: normal where accepted, or a
// non-negative length.
func gapValueAccepted(value string, allowNormal bool) bool {
	if allowNormal && value == contentNormal {
		return true
	}

	length, ok := lengthBox(value, 0, 0, cssDisplayNone)

	return ok && length >= 0
}

// logicalPairValueAccepted checks one or two tokens of a logical pair
// shorthand against the longhand predicate.
func logicalPairValueAccepted(value string, tokenAccepted func(string) bool) bool {
	var tokens [2]string

	count := splitSpaceTokens(value, tokens[:])
	if count < 1 || count > len(tokens) {
		return false
	}

	for idx := range count {
		if !tokenAccepted(tokens[idx]) {
			return false
		}
	}

	return true
}

// hyphenateLimitCharsValueAccepted mirrors applyHyphenateLimitChars: auto or
// one to three integer/auto fields.
func hyphenateLimitCharsValueAccepted(value string) bool {
	val := strings.ToLower(strings.TrimSpace(value))
	if val == overflowAuto {
		return true
	}

	parts := strings.Fields(val)
	if len(parts) < 1 || len(parts) > maxHyphenateLimitValues {
		return false
	}

	for _, part := range parts {
		if part == overflowAuto {
			continue
		}

		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return false
		}
	}

	return true
}

// hyphenateLimitLinesValueAccepted mirrors applyHyphenateLimitLines.
func hyphenateLimitLinesValueAccepted(value string) bool {
	val := strings.ToLower(strings.TrimSpace(value))
	if val == "no-limit" {
		return true
	}

	n, err := strconv.Atoi(val)

	return err == nil && n >= 0
}

// hyphenateLimitZoneValueAccepted mirrors applyHyphenateLimitZone: a
// non-negative percentage or length.
func hyphenateLimitZoneValueAccepted(value string) bool {
	val := strings.ToLower(strings.TrimSpace(value))

	if strings.HasSuffix(val, "%") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(val, "%"), 64)

		return err == nil && n >= 0
	}

	length, ok := plainLength(val, 0, 0)

	return ok && length >= 0
}

// counterListValueAccepted mirrors parseCounterList: none, or one or more
// <identifier> [<integer>?] pairs. Function tokens, stray integers, and a
// mixed-in none are rejected so the declaration cannot win the cascade.
func counterListValueAccepted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if strings.EqualFold(trimmed, cssDisplayNone) {
		return true
	}

	tokens := strings.Fields(trimmed)
	if len(tokens) == 0 {
		return false
	}

	for idx := 0; idx < len(tokens); idx++ {
		tok := tokens[idx]
		if strings.ContainsRune(tok, '(') || strings.EqualFold(tok, cssDisplayNone) || isIntegerToken(tok) {
			return false
		}

		if idx+1 < len(tokens) && isIntegerToken(tokens[idx+1]) {
			idx++
		}
	}

	return true
}

// quotesValueAccepted mirrors parseQuotes: none, or at least two quoted
// strings (open/close pairs).
func quotesValueAccepted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if strings.EqualFold(trimmed, cssDisplayNone) {
		return true
	}

	return len(collectQuotedStrings(trimmed)) >= two
}

// hyphenateCharacterValueAccepted mirrors the wave-3 setter, which strips
// surrounding quotes from a single token: auto or one string token.
func hyphenateCharacterValueAccepted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if strings.EqualFold(trimmed, overflowAuto) {
		return true
	}

	return len(strings.Fields(trimmed)) == 1
}
