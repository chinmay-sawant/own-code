# Merge conflict demo

Manual test fixture for the git conflict viewer. It builds a small repo and leaves it mid-merge, so Source Control shows real unmerged paths. Marker text alone is not enough. The viewer reads the git index, which is why this is a script and not a checked-in file.

## Setup

```sh
cd testdata/merge-conflicts
./setup.sh
```

This creates `demo/` with two conflicted files:

- `config/services.yaml` with two hunks (poll interval and the service list)
- `backend/scheduler.go` with one hunk (worker and backoff constants)

The script sets `merge.conflictStyle diff3` in the demo repo, so the working file carries base sections and Compare shows three sides. Rerunning the script resets the demo. `demo/` is ignored by git and never committed.

## Open it

From the repo root:

```sh
go run ./cmd/own-code -root testdata/merge-conflicts/demo
```

Go to Source Control. Both paths carry a conflict badge. Select one to open the merge editor.

## Things to try

- Accept Current, Incoming, and Both on each hunk. Both keeps the two sides in order.
- Move with Previous and Next (p and n). The count reads 1 of 2, then 2 of 2 in `services.yaml`.
- Toggle Compare to see the base section from diff3.
- Save with Ctrl+S. Saving writes the file but leaves the path unmerged.
- Mark Resolved stages that one path with `git add`. The badge clears for that file only.
- After both paths are staged, finish outside the app with `git commit`.

## Start over

```sh
./setup.sh
```

Or, inside `demo/`, `git merge --abort` drops the merge and `git checkout current` plus `git merge incoming` replays it.
