# Terminal

own-code includes an embedded terminal panel for commands, tests, and interactive shells. It runs inside the app window and does not need an external terminal or browser.

## Open, hide, and resize

Open the terminal from the terminal toggle in the app. The panel opens below the editor. Use the same toggle to hide it. Hiding the panel keeps its sessions running, including any command in progress.

Drag the divider between the editor and the terminal to resize the panel. On Unix, the app passes the new size to the shell's PTY, so full-screen programs redraw at the new dimensions. The Windows fallback cannot pass resize events to programs; see [Platform support](#platform-support).

Closing own-code terminates every terminal session, whether the panel is visible or hidden.

## Tabs and sessions

Use the new terminal control to create a session. Each session has its own shell, working directory, scrollback, and running process. Switch sessions from the tab strip. Closing one tab terminates that session's shell and closes its terminal connection. The other tabs keep running.

If a shell exits on its own, its tab reports the exit and stops accepting input. Close the tab or create a new session.

## Focus and keyboard input

Click the terminal body or a tab to focus the terminal. While it has focus, keystrokes go to the shell: `Ctrl+C` interrupts the foreground process, `Ctrl+D` ends input, `Ctrl+Z` suspends the foreground job, and `Tab` completion and the arrow keys work as they do in a normal terminal. Click outside the terminal to leave focus; app shortcuts apply again.

## Output, scrollback, and copy/paste

The terminal renders ANSI colors, cursor movement, erasing, and the common control sequences used by shells and command-line programs. It keeps a scrollback buffer for earlier output.

The panel follows live output while it is at the bottom. Mouse wheel and the scroll controls move through scrollback, and scrolling up keeps your place while new output arrives. Scroll back to the bottom to follow live output again.

Copy puts the visible terminal output on the clipboard. It copies the current screen or the visible scrollback window; it does not copy an arbitrary text selection. Use Paste to send clipboard text to the focused shell, then press Enter to run it. The terminal's Copy and Paste controls are separate from the app's editing and explorer shortcuts. While the terminal has focus, `Ctrl+Shift+C` copies and `Ctrl+Shift+V` pastes; `Ctrl+C` interrupts the running command rather than copying. Press Ctrl plus the backtick key to toggle the panel from anywhere.

## Choose a shell

Set `OWN_CODE_SHELL` before starting own-code to choose the shell executable. own-code reads only the executable name or path from the variable. It does not read arguments from the value, and it does not run a command from it automatically.

The value can be an absolute path or a command on `PATH`.

```sh
OWN_CODE_SHELL=/bin/bash own-code
OWN_CODE_SHELL=/usr/bin/fish go run ./cmd/own-code
```

Windows CMD:

```bat
set OWN_CODE_SHELL=pwsh.exe
own-code
```

PowerShell:

```powershell
$env:OWN_CODE_SHELL = "pwsh.exe"
own-code
```

If `OWN_CODE_SHELL` is unset, own-code uses `$SHELL` on Unix when set, otherwise `/bin/sh`. On Windows it uses `%COMSPEC%` when set, otherwise `cmd.exe`. An invalid value shows an error in the terminal panel and does not affect the rest of the app.

## Working directory

A new session starts in the active workspace root. That is the directory passed with `-root`, or the current directory when `-root` is omitted.

```sh
pwd
```

Each session has its own working directory. Changing directory in one tab does not move another tab, and new tabs start at the workspace root again. Selecting a file in the explorer does not change a running session's directory.

## Platform support

Unix systems, including Linux, macOS, and WSL, run the shell on a pseudo-terminal (PTY). Interactive programs such as `vim`, `less`, `top`, and prompts that read from `/dev/tty` work as they do in a normal terminal, including resize handling and signals.

Windows uses a pipe-backed terminal fallback with no PTY. Line-oriented commands and ordinary prompts work. Full-screen programs have limited support, resize and signal fidelity are not available, and stderr goes to the app console instead of the terminal panel. Use the app's WSL handoff for a Linux workspace when you need full PTY behavior on Windows.

## Security

Terminal commands run with your operating system user's privileges. There is no sandbox, no separate account, and no permission prompt inside the terminal. A command can read or change any file you can, use your network access, and read environment variables or credentials available to the app.

own-code starts a shell only when you open a terminal session. It does not run workspace files on its own. Only the executable name or path from `OWN_CODE_SHELL` is read; own-code does not run commands or arguments from that value. The shell starts normally and may read its usual startup files.

Only run commands you trust, especially commands pasted from a website or chat. Review a command before pressing Enter. Closing a tab or the app stops the session and its shell, but it does not undo files they changed.
