# 09a: LazyCoder policy audit versus own-code agenttools

Scope: Phase 4 audit item of [09](09-lazykoder-agent-runtime-port.md). Source reviewed read-only:
`chinmay-sawant/lazykoder` `internal/policy/policy.go` (and its test file). Nothing was copied.

## What LazyCoder's policy is

- `Classify(command)` / `ClassifyWithAllowlist(command, allowlist, enabled)` is a **shell-command classifier**
  (uses `mvdan.cc/sh` to parse). Verdicts: `Allow`, `Ask`, `Deny` (`Deny` is declared but never returned).
- Default is **allow**; it returns **ask** only for: deletes (`rm`, `rmdir`, `unlink`, `shred`, `git rm`,
  `find -delete`, deletes behind `sudo`/`env`/`xargs`/`command`, and nested `sh -c`), unparsable or empty
  input, line-continuation syntax, and (when enabled) executables outside an allowlist.
- `PrivateOrLoopback(host)` is a network helper (SSRF-style host check), not a tool gate.
- It has **no notion of file tools, worktree roots, per-call approval binding, or workspace containment.**

## Matrix: initial own-code tools

Decisions are the effective own-code decisions (`agenttools.DefaultPolicy` plus `Policy.Authorize`).

| Tool | own-code | LazyCoder equivalent | Notes |
| --- | --- | --- | --- |
| `read_file` | allow | none (not a policy concern there) | Containment, symlink recheck, 64 KiB cap enforced in the executor. Refuses sensitive-name paths (`internal/sensitive`: `.env*`, keys, `*.pem`, credentials, secrets, tokens, `.npmrc`, `.netrc`, ...) and git-ignored paths with `ErrSensitive`; `list_files` and `search_files` silently skip them. Git-ignored detection uses `git ls-files --others --ignored`; outside a git repo only the name rules apply. `run_shell` is not filtered and stays approval-gated. |
| `list_files` | allow | none | Same containment rules. |
| `search_files` | allow | none | Skips symlinks and `.git`; bounded results. |
| `write_file` | ask | none | Stages a proposal in a separate proposal worktree only; never writes the user's checkout. Apply is a separate, rechecked step. |
| `edit_file` | ask | none | Same as `write_file`; `old_string` must match exactly once. |
| `run_shell` (any command) | ask | allow, except ask for the cases below | own-code asks for **every** command; this is stricter by design. |
| `run_shell`: delete / recursive delete | ask | ask (destructive flag when recursive) | Same outcome; own-code already asks. |
| `run_shell`: unparsable / ambiguous syntax | ask | ask | Same outcome. |
| `run_shell`: executable not in allowlist | ask | ask only if allowlist enabled | own-code has no allowlist; always ask. |
| unknown tool, nil policy, unrecognized decision | deny | n/a | Fail closed; unknown tools are rejected before approval. |
| dismissal, timeout, cancellation, missing approval | deny | n/a (LazyCoder confirms in a TUI) | Only an explicit grant counts. |

## Differences that matter

1. **Default.** LazyCoder defaults to allow for shell; own-code defaults to ask. Porting its `Classify`
   as-is would weaken the first release's "approve every shell command" requirement.
2. **Binding.** LazyCoder's decision is about a command string only. own-code binds an approval to
   session, turn, call ID, worktree root and canonical args hash, verified inside the executor.
3. **No Deny.** LazyCoder never denies; own-code denies write tools when no proposal root is configured
   (they are not registered) and denies anything missing from the policy.
4. **Shell classification is advisory.** Parsing cannot prove a command is safe (`curl | sh`, `python -c`,
   `git` hooks, shell expansions the parser cannot resolve). A worktree cwd is not a sandbox either way.

## Decisions

- Do not port `Classify` to relax approval. The first version keeps ask-for-every-command.
- Optional later reuse: use a parse-based classifier only to **annotate** the approval prompt (for example
  highlight "recursive delete detected"), never to auto-allow. Adopting it needs the `mvdan.cc/sh` dependency
  and its own tests; not done now.
- Do not port `PrivateOrLoopback` now: no own-code tool performs network fetches. Revisit if a fetch/web tool
  is added; it would be a prerequisite for one. Note `ModelTarget.endpoint` already restricts http to loopback.
- Gaps still open in own-code (tracked in 09, not solved by this audit): dirty editor buffer checks and staged
  git state when applying a proposal belong to the app-side accept flow; `ApplyProposal` only rechecks the
  baseline hash, containment and symlinks.
