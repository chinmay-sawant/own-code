# Merge conflicts

own-code shows Git merge conflicts in Source Control and opens a merge editor for conflicted files. Git's index is the source of truth: own-code lists paths with unmerged stages, not files that merely contain conflict markers.

## Detection

A conflict appears when Git records an unmerged path during a merge, rebase, cherry-pick, or revert. own-code reads those index entries and leaves a path out of the conflict list when Git sees no unmerged stages, even if the file contains marker text.

Source Control gives each conflicted path a conflict badge. The conflicts section shows how many paths are unmerged. After you resolve or stage a file outside the app, refresh Source Control to update the list.

## Open the merge editor

Select a conflicted path in Source Control to open it in the merge editor. The editor also opens from a conflicted file's context menu where those actions are available.

The merge editor has three parts:

- **Current (ours):** the current side recorded by Git.
- **Incoming (theirs):** the other side recorded by Git.
- **Result:** the editable file that own-code writes to the working tree.

In a rebase, Git swaps the labels: ours is the branch you are rebasing onto, and theirs is the commit being replayed. The merge editor follows Git's stages.

Conflict ranges are highlighted. Unresolved and resolved hunks use different markers, so you can see what is left. Current and Incoming are read-only. Result accepts normal editing, including undo and redo.

## Resolve a conflict

Each conflict offers these actions:

- Accept Current Change
- Accept Incoming Change
- Accept Both Changes
- Compare Changes

Accept actions replace the conflict in Result with the selected side. Accept Both keeps both sides. Compare opens the comparison view and returns you to the result.

Right-click in the merge editor or on the conflicted path in Source Control to reach the same actions where they apply.

Previous and next controls move between conflicts. The toolbar shows the current position, for example 2 of 5, scrolls the range into view, and focuses it. The same controls have keyboard shortcuts while the merge editor has focus.

You can also edit Result directly. Undo and redo work on manual edits and accepted changes. Moving between conflicts keeps the editor scroll position and focuses the target range.

If the file changes on disk after you open it, own-code warns before saving over the external change.

## Save and stage

Save writes Result to disk. It does not stage the file.

Mark Resolved or Stage runs `git add` on that path and refreshes Source Control. Save first if Result has unsaved edits. Staging removes the path from the unmerged list when Git accepts it.

own-code never stages on save, never commits, and never continues a merge, rebase, cherry-pick, or revert. Every stage action is explicit. After all conflicts are staged, finish the operation yourself:

```sh
git commit
git merge --continue
git rebase --continue
git cherry-pick --continue
git revert --continue
```

Use the command that matches the operation Git started.

## Three-way base and diff3

Git records three stages for a text conflict: base, current, and incoming. When a base stage exists, Compare opens a three-way view with Base alongside Current and Incoming.

Add/add conflicts have no base, so their comparison has two sides. Other conflicts with a missing or unreadable base also fall back to two-way comparison.

If the working file uses diff3 style, the base section is available in the file text. To include base markers in future conflicts, enable diff3 before starting the merge:

```sh
git config merge.conflictStyle diff3
```

## Fallbacks

Binary files have no line-level merge. The resolver shows a notice instead of the text editor and leaves the file unchanged. Resolve it with an external tool or choose a side with Git, then stage it explicitly.

Path-level conflicts do not fit the three-pane text model. This covers add/add, modify/delete, rename conflicts, and conflicts with no base. The resolver names the conflict type and shows the side or sides that exist. It does not guess a deletion or pick a side for you.

If a file's markers cannot be parsed, the merge editor opens the text as an editable file with a notice. It does not rewrite the content on its own.

In every fallback the path stays unmerged until you stage it. own-code does not stage, delete, or overwrite the file automatically.

## No automatic Git changes

The resolver separates three actions:

- Save writes the working tree file.
- Mark Resolved or Stage stages that one path.
- Committing and continuing the operation stay outside own-code.

Opening a conflict, accepting a side, comparing, and navigating never stage or commit anything.
