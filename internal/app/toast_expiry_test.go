package app

import (
	"testing"
	"time"
)

func TestToastExpiresAfterLifetime(t *testing.T) {
	app := newTestApp(t)
	app.showToast("success", "Saved", "file.go")

	app.pollToast()

	if !app.toast.Show {
		t.Fatal("toast should still show before its lifetime ends")
	}

	app.toast.until = time.Now().Add(-time.Millisecond)
	app.pollToast()

	if app.toast.Show {
		t.Fatal("toast should dismiss itself once its lifetime has elapsed")
	}
}
