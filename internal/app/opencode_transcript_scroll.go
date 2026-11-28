package app

// Transcript scroll. Like the settings dialog, the engine has no input path
// that moves an inner overflow box, so the transcript owns its offset: the
// clipped #opencode-transcript viewport holds #opencode-transcript-inner whose
// relative top is -offset. The transcript follows new output only while it is
// attached to the end; scrolling up detaches it and keeps the pixel position
// stable while content grows below.
const (
	openCodeTranscriptViewportID = "opencode-transcript"
	openCodeTranscriptInnerID    = "opencode-transcript-inner"
	openCodeTranscriptWheelStep  = 48
)

type openCodeTranscriptScroll struct {
	detached        bool // false: follow the end
	top             int  // pixel offset while detached
	viewH, contentH int
}

func (s *openCodeTranscriptScroll) maxTop() int { return max(s.contentH-s.viewH, 0) }

// offset is the effective pixel offset: the end while following.
func (s *openCodeTranscriptScroll) offset() int {
	if !s.detached {
		return s.maxTop()
	}
	return min(max(s.top, 0), s.maxTop())
}

// by scrolls by delta pixels (positive = toward the end). Reaching the end
// re-attaches; moving up detaches.
func (s *openCodeTranscriptScroll) by(delta int) {
	next := min(max(s.offset()+delta, 0), s.maxTop())
	s.top = next
	s.detached = next < s.maxTop()
}

// follow reattaches to the end (new session, user sends a message).
func (s *openCodeTranscriptScroll) follow() { s.detached, s.top = false, 0 }

// atEnd reports whether the user is reading the latest output.
func (s *openCodeTranscriptScroll) atEnd() bool { return !s.detached }

// measure stores new geometry and reports whether the effective offset or
// geometry changed.
func (s *openCodeTranscriptScroll) measure(viewH, contentH int) bool {
	before := s.offset()
	changed := viewH != s.viewH || contentH != s.contentH
	s.viewH, s.contentH = viewH, contentH
	if s.detached && s.top > s.maxTop() {
		s.top = s.maxTop()
	}
	return changed || before != s.offset()
}

// updateOpenCodeTranscriptMetrics remeasures from the last layout.
func (a *App) updateOpenCodeTranscriptMetrics() bool {
	if a == nil || a.page == nil || !a.openCode.panelOpen {
		return false
	}
	menus := a.openCodeMenuMetrics()
	boxes := a.page.Boxes()
	vp, ok := findBoxID(boxes, openCodeTranscriptViewportID)
	if !ok {
		return menus
	}
	inner, ok := findBoxID(boxes, openCodeTranscriptInnerID)
	if !ok {
		return false
	}
	return a.openCode.scroll.measure(int(vp.H), int(inner.H)) || menus
}

// openCodeWheelAt scrolls the transcript when the wheel lands on it.
func (a *App) openCodeWheelAt(x, y, dy float64) bool {
	if !a.openCode.panelOpen || dy == 0 {
		return false
	}
	if a.openCodeMenuWheel(x, y, dy) {
		return true
	}
	vp, ok := findBoxID(a.page.Boxes(), openCodeTranscriptViewportID)
	if !ok || x < vp.X || x > vp.X+vp.W || y < vp.Y || y > vp.Y+vp.H {
		return false
	}
	a.updateOpenCodeTranscriptMetrics()
	a.openCode.scroll.by(int(-dy * float64(a.zx(openCodeTranscriptWheelStep))))
	a.refresh()
	return true
}
