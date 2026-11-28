package app

import (
	"bytes"
	"context"
	"html/template"
	"strings"
	"testing"

	"github.com/chinmay-sawant/ownframe"
)

func TestFull19TemplatesOwnframeGenParity(t *testing.T) {
	a := setupDiffPerfApp(t, true)
	tpl := template.Must(template.New("app").Parse(buildHTML()))

	checkMode := func(name string, v View) {
		t.Helper()
		var wantBuf, gotBuf bytes.Buffer
		if err := tpl.Execute(&wantBuf, &v); err != nil {
			t.Fatalf("%s: html/template failed: %v", name, err)
		}
		if err := renderAppPage(&gotBuf, &v); err != nil {
			t.Fatalf("%s: renderAppPage failed: %v", name, err)
		}
		want := wantBuf.String()
		got := gotBuf.String()
		// html/template strips /* ... */ CSS comments inside <style>; compare from <body onwards.
		if idx := strings.Index(want, "<body"); idx >= 0 {
			want = want[idx:]
		}
		if idx := strings.Index(got, "<body"); idx >= 0 {
			got = got[idx:]
		}
		// html/template escapes '+' as '&#43;' in attributes (legacy UTF-7 defense);
		// normalize that entity to verify semantic HTML parity across all 19 templates.
		want = strings.ReplaceAll(want, "&#43;", "+")
		if got != want {
			minLen := len(want)
			if len(got) < minLen {
				minLen = len(got)
			}
			diffAt := minLen
			for i := 0; i < minLen; i++ {
				if want[i] != got[i] {
					diffAt = i
					break
				}
			}
			lo := max(0, diffAt-80)
			hiW := min(len(want), diffAt+120)
			hiG := min(len(got), diffAt+120)
			t.Fatalf("%s: parity mismatch at byte %d (len want=%d got=%d):\nwant: %q\ngot:  %q",
				name, diffAt, len(want), len(got), want[lo:hiW], got[lo:hiG])
		}
	}

	checkMode("split-diff", a.View())
	a.setSplit(false)
	checkMode("unified-diff", a.View())
	a.setMode(context.Background(), "explorer")
	checkMode("explorer", a.View())
}

func BenchmarkFullPageTemplateExec(b *testing.B) {
	a := setupDiffPerfApp(b, true)
	v := a.View()
	src := buildHTML()
	tpl := template.Must(template.New("app").Parse(src))

	var sample bytes.Buffer
	if err := tpl.Execute(&sample, &v); err != nil {
		b.Fatal(err)
	}

	b.Run("HTMLTemplate", func(b *testing.B) {
		var buf bytes.Buffer
		buf.Grow(sample.Len())
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			buf.Reset()
			if err := tpl.Execute(&buf, &v); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("OwnframeGen_All19Templates", func(b *testing.B) {
		var buf bytes.Buffer
		buf.Grow(sample.Len())
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			buf.Reset()
			if err := renderAppPage(&buf, &v); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkFullPageRedraw(b *testing.B) {
	a := setupDiffPerfApp(b, true)
	ctx := context.Background()

	v0 := a.View()
	a.selectDiff("other.go")
	v1 := a.View()
	views := []View{v0, v1}

	src := buildHTML()

	b.Run("ConfigHTML_FullRedraw", func(b *testing.B) {
		p, err := ownframe.New(ownframe.Config{
			Title:       "Code Review",
			HTML:        src,
			RetainState: true,
			Width:       1580,
			Height:      1032,
		})
		if err != nil {
			b.Fatal(err)
		}
		p.SetData(&views[0])
		if err := p.Redraw(ctx); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			p.SetData(&views[i%2])
			if err := p.Redraw(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("ConfigRender_OwnframeGen_All19Templates", func(b *testing.B) {
		p, err := ownframe.New(ownframe.Config{
			Title:       "Code Review",
			Render:      renderAppPage,
			RetainState: true,
			Width:       1580,
			Height:      1032,
		})
		if err != nil {
			b.Fatal(err)
		}
		p.SetData(&views[0])
		if err := p.Redraw(ctx); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			p.SetData(&views[i%2])
			if err := p.Redraw(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
}
