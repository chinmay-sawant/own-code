package app

import "context"

// focusAfterLayout focuses id now, or queues it for the next tick when the
// control does not exist yet. The window wraps a press, caret placement, and
// click in one redraw batch, so a panel a click handler opens is not in the
// form state when the handler asks for its input. The next tick runs after
// the batch commits and the panel has been laid out.
func (a *App) focusAfterLayout(ctx context.Context, id string) error {
	if err := a.page.Form().Focus(ctx, id); err != nil {
		return err
	}

	if a.page.Form().FocusID() == id {
		return nil
	}

	a.pendingFocus = id
	a.page.WantFrameIn(0)

	return nil
}

// applyPendingFocus focuses the control a panel asked for once its redraw
// has committed. It runs from tickWatch on the page loop.
func (a *App) applyPendingFocus(ctx context.Context) error {
	if a.pendingFocus == "" {
		return nil
	}

	id := a.pendingFocus
	a.pendingFocus = ""

	return a.page.Form().Focus(ctx, id)
}
