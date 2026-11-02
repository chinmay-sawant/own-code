//go:build !js

// Command own-code opens the Code Review app.
// Pass -web to serve the interactive picture in a browser instead.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/chinmay-sawant/own-code/internal/app"
	"github.com/chinmay-sawant/own-code/internal/wsl"
	"github.com/chinmay-sawant/ownframe"
)

func main() {
	webMode := flag.Bool("web", false, "serve the picture in a browser on -addr")
	addr := flag.String("addr", "127.0.0.1:8145", "listen address for -web")
	root := flag.String("root", ".", "directory to browse; when omitted the last opened project reopens")
	files := flag.Int("files", app.MaxWorkspaceFiles, "explicit N runs a full scan up to N files; default is lazy (ReadDir root only, folders load on expand)")
	dev := flag.Bool("dev", false, "open the developer tools dock at startup")
	perf := flag.Bool("perf", true, "enable pipeline sampling for the devtools Frame tab")
	windowControl := flag.Bool("window-control", true, "draw custom minimize, maximize, and close controls in the titlebar")
	fps := flag.Int("fps", 0, "desktop present cap: 60/120/144/240, -1/0 uncapped (default); Updates run at cap+20, idle still parks")
	wsl.AddWSLFlags(flag.CommandLine)
	flag.Parse()
	if wsl.MaybeReexecInWSL() {
		return
	}

	rootExplicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "root" {
			rootExplicit = true
		}
	})

	a, err := app.NewWithLimit(app.ResolveStartupRoot(rootExplicit, *root), *files)
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()

	a.EnableSession()

	fpsSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "fps" {
			fpsSet = true
		}
	})
	a.SetFPSCLI(*fps, fpsSet)

	devSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "dev" {
			devSet = true
		}
	})

	ctx := context.Background()
	a.ReloadChanges(ctx, false)
	// The dock stays hidden by default. A restored session that remembers an
	// open dock must not reopen it unless the launch passed -dev=true;
	// F12/Ctrl+Shift+I and SetDevTools still toggle it at runtime.
	if devSet {
		a.SetDevTools(*dev)
	} else {
		a.SetDevTools(false)
	}

	if *perf {
		a.Page().Window().SetPerf(true)
	}

	if *webMode {
		if err := ownframe.ServeWithOptions(ctx, a.Page(), *addr, ownframe.ServeOptions{InsecureAllowPprof: *perf}); err != nil {
			log.Fatal(err)
		}

		return
	}

	// Custom window controls are desktop window only. The web preview keeps the
	// native frame and hides the in-page controls.
	a.SetWindowControl(*windowControl)

	if err := a.StartWatcher(ctx); err != nil {
		if appErr := app.AppErrorAt(err); appErr != nil {
			log.Printf("own-code: watcher degraded (%s): %v", appErr.UserMessage(), err)
		} else {
			log.Printf("own-code: watcher unavailable: %v", err)
		}
	}
	options := ownframe.WindowOptions{CustomChrome: a.WindowControl(), PlacementID: "own-code", MaxFPS: a.WindowMaxFPSValue()}
	if a.WindowControl() {
		options.Draggable = a.WindowControlDrag
	}
	runErr := ownframe.RunWithOptions(ctx, a.Page(), options)
	a.StopWatcher()

	if runErr != nil {
		log.Fatal(runErr)
	}
}
