package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chinmay-sawant/own-code/internal/agentreview"
	"github.com/chinmay-sawant/own-code/internal/agenttools"
)

func writeStagedProposalForRestart(t *testing.T, configDir, sid string, p agenttools.Proposal) {
	t.Helper()
	if p.Deleted {
		return
	}
	root, err := openCodeProposalDir(configDir, sid)
	if err != nil {
		t.Fatal(err)
	}
	target, err := proposalFilePath(root, p.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(p.NewContent), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOpenCodeProposalPersistenceRestoresHunkStateWithoutApplying(t *testing.T) {
	repo := t.TempDir()
	e2eGit(t, repo, "init", "-q")
	base := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\n"
	file := filepath.Join(repo, "f.txt")
	if err := os.WriteFile(file, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	e2eGit(t, repo, "add", ".")
	e2eGit(t, repo, "commit", "-qm", "init")
	configDir := t.TempDir()
	settingsPath := filepath.Join(configDir, "settings.json")
	first := newProposalApp(t, repo)
	first.settingsPath = settingsPath
	proposal := e2eProposal(t, repo, "f.txt", "A\nb\nc\nd\ne\nf\ng\nh\nI\nj\n")
	writeStagedProposalForRestart(t, configDir, "s1", proposal)
	first.addAgentProposal("s1", proposal)
	initial := first.agentReviewRegistry().Get("s1", "f.txt")
	if initial == nil || len(initial.Hunks) != 2 {
		t.Fatalf("initial review = %+v", initial)
	}
	firstIDs := []string{initial.Hunks[0].ID, initial.Hunks[1].ID}
	if err := first.reviewOpenCodeHunk(agentHunkAccept, "f.txt", firstIDs[0]); err != nil {
		t.Fatal(err)
	}
	if err := first.reviewOpenCodeHunk(agentHunkReject, "f.txt", firstIDs[1]); err != nil {
		t.Fatal(err)
	}
	beforeRestore, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	second := newProposalApp(t, repo)
	second.settingsPath = settingsPath
	restoredState := loadOpenCodeProposalRestore(configDir, repo, "s1")
	proposalRoot, err := openCodeProposalDir(configDir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	statePath, err := openCodeProposalStatePath(configDir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(proposalRoot, "f.txt")); err != nil {
		t.Fatal(err)
	}
	second.receiveOpenCodePanelReply(openCodeReply{
		operation: openCodeOperationLoadMessages, selectedSessionID: "s1", panelStatus: "Local session ready.", proposalRestore: restoredState,
	})
	restored := second.agentReviewRegistry().Get("s1", "f.txt")
	if restored == nil || len(restored.Hunks) != 2 || restored.Hunks[0].ID != firstIDs[0] || restored.Hunks[1].ID != firstIDs[1] {
		t.Fatalf("restored review = %+v", restored)
	}
	if !strings.Contains(second.openCode.panelStatus, "Restored 1 pending proposal") {
		t.Fatalf("session load status = %q", second.openCode.panelStatus)
	}
	if restored.States[0] != agentreview.Accepted || restored.States[1] != agentreview.Rejected {
		t.Fatalf("restored hunk states = %v, want accepted and rejected", restored.States)
	}
	afterRestore, err := os.ReadFile(file)
	if err != nil || string(afterRestore) != string(beforeRestore) {
		t.Fatalf("restore changed checkout: %q, %v", afterRestore, err)
	}
	if err := second.reviewOpenCodeHunk(agentHunkRevert, "f.txt", firstIDs[0]); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != base {
		t.Fatalf("revert after restore = %q, %v; want original baseline", got, err)
	}
}

func TestOpenCodeProposalPersistenceSkipsMissingStagedContent(t *testing.T) {
	repo := t.TempDir()
	e2eGit(t, repo, "init", "-q")
	file := filepath.Join(repo, "f.txt")
	if err := os.WriteFile(file, []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e2eGit(t, repo, "add", ".")
	e2eGit(t, repo, "commit", "-qm", "init")
	configDir := t.TempDir()
	first := newProposalApp(t, repo)
	first.settingsPath = filepath.Join(configDir, "settings.json")
	proposal := e2eProposal(t, repo, "f.txt", "agent\n")
	writeStagedProposalForRestart(t, configDir, "s1", proposal)
	first.addAgentProposal("s1", proposal)
	proposalRoot, err := openCodeProposalDir(configDir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(proposalRoot, "f.txt")); err != nil {
		t.Fatal(err)
	}

	second := newProposalApp(t, repo)
	second.settingsPath = first.settingsPath
	second.receiveOpenCodePanelReply(openCodeReply{
		operation: openCodeOperationLoadMessages, selectedSessionID: "s1", panelStatus: "Local session ready.",
		proposalRestore: loadOpenCodeProposalRestore(configDir, repo, "s1"),
	})
	if len(second.openCode.agentProposals["s1"]) != 0 || !strings.Contains(second.openCode.panelStatus, "1 incomplete or invalid proposal record") {
		t.Fatalf("missing staged content restore = proposals %v, status %q", second.openCode.agentProposals["s1"], second.openCode.panelStatus)
	}
	loaded, skipped, err := restoreCounts(loadOpenCodeProposalRestore(configDir, repo, "s1"))
	if err != nil || loaded != 0 || skipped != 1 {
		t.Fatalf("missing staged content restore = %d loaded, %d skipped, %v", loaded, skipped, err)
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != "base\n" {
		t.Fatalf("missing proposal changed checkout: %q, %v", got, err)
	}
}

func TestOpenCodeProposalPersistenceSkipsChangedStagedContent(t *testing.T) {
	repo := t.TempDir()
	e2eGit(t, repo, "init", "-q")
	file := filepath.Join(repo, "f.txt")
	if err := os.WriteFile(file, []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e2eGit(t, repo, "add", ".")
	e2eGit(t, repo, "commit", "-qm", "init")
	configDir := t.TempDir()
	first := newProposalApp(t, repo)
	first.settingsPath = filepath.Join(configDir, "settings.json")
	proposal := e2eProposal(t, repo, "f.txt", "agent\n")
	writeStagedProposalForRestart(t, configDir, "s1", proposal)
	first.addAgentProposal("s1", proposal)
	proposalRoot, err := openCodeProposalDir(configDir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proposalRoot, "f.txt"), []byte("tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	second := newProposalApp(t, repo)
	second.settingsPath = first.settingsPath
	loaded, skipped, err := restoreCounts(loadOpenCodeProposalRestore(configDir, repo, "s1"))
	if err != nil || loaded != 0 || skipped != 1 {
		t.Fatalf("changed staged content restore = %d loaded, %d skipped, %v", loaded, skipped, err)
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != "base\n" {
		t.Fatalf("changed staged proposal touched checkout: %q, %v", got, err)
	}
}

func TestOpenCodeProposalPersistenceRestoresDeleteAndRename(t *testing.T) {
	repo := t.TempDir()
	e2eGit(t, repo, "init", "-q")
	for name, contents := range map[string]string{"delete.txt": "delete me\n", "from.txt": "move me\n"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e2eGit(t, repo, "add", ".")
	e2eGit(t, repo, "commit", "-qm", "init")
	configDir := t.TempDir()
	first := newProposalApp(t, repo)
	first.settingsPath = filepath.Join(configDir, "settings.json")
	deleteProposal := e2eWholeProposal(t, repo, "delete_file", map[string]string{"path": "delete.txt"})
	renameProposal := e2eWholeProposal(t, repo, "move_file", map[string]string{"from": "from.txt", "to": "to.txt"})
	writeStagedProposalForRestart(t, configDir, "s1", renameProposal)
	first.addAgentProposal("s1", deleteProposal)
	first.addAgentProposal("s1", renameProposal)

	second := newProposalApp(t, repo)
	second.settingsPath = first.settingsPath
	restored := loadOpenCodeProposalRestore(configDir, repo, "s1")
	loaded, skipped, err := restoreCounts(restored)
	second.applyOpenCodeProposalRestore(restored)
	if err != nil || loaded != 2 || skipped != 0 {
		t.Fatalf("restore = %d loaded, %d skipped, %v", loaded, skipped, err)
	}
	if got := second.agentProposalRows("s1"); len(got) != 2 || got[0].Change != "Delete" || got[1].Change != "Rename" {
		t.Fatalf("restored proposal rows = %+v", got)
	}
	if del := second.agentReviewRegistry().Get("s1", "delete.txt"); del == nil || len(del.Hunks) != 0 {
		t.Fatalf("restored delete review = %+v", del)
	}
	if moved := second.agentReviewRegistry().Get("s1", "to.txt"); moved == nil || len(moved.Hunks) != 0 || moved.RenamedFrom != "from.txt" {
		t.Fatalf("restored rename review = %+v", moved)
	}
	if _, err := os.Stat(filepath.Join(repo, "delete.txt")); err != nil {
		t.Fatalf("delete proposal touched checkout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "from.txt")); err != nil {
		t.Fatalf("rename proposal touched checkout: %v", err)
	}
}

func restoreCounts(restored *openCodeProposalRestore) (int, int, error) {
	if restored == nil {
		return 0, 0, nil
	}
	return restored.loaded, restored.skipped, restored.err
}

func TestOpenCodeProposalPersistenceSkipsDiffManifestTampering(t *testing.T) {
	repo := t.TempDir()
	e2eGit(t, repo, "init", "-q")
	file := filepath.Join(repo, "f.txt")
	if err := os.WriteFile(file, []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e2eGit(t, repo, "add", ".")
	e2eGit(t, repo, "commit", "-qm", "init")
	configDir := t.TempDir()
	app := newProposalApp(t, repo)
	app.settingsPath = filepath.Join(configDir, "settings.json")
	proposal := e2eProposal(t, repo, "f.txt", "agent\n")
	writeStagedProposalForRestart(t, configDir, "s1", proposal)
	app.addAgentProposal("s1", proposal)

	statePath, err := openCodeProposalStatePath(configDir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var saved openCodePersistedProposals
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	saved.Proposals[0].Proposal.Diff = "forged diff"
	data, err = json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivateProposalFile(statePath, data); err != nil {
		t.Fatal(err)
	}

	restored := loadOpenCodeProposalRestore(configDir, repo, "s1")
	if restored.err != nil || restored.loaded != 0 || restored.skipped != 1 {
		t.Fatalf("tampered diff restore = %+v", restored)
	}
	second := newProposalApp(t, repo)
	second.applyOpenCodeProposalRestore(restored)
	if rows := second.agentProposalRows("s1"); len(rows) != 0 {
		t.Fatalf("tampered diff proposal remained visible: %+v", rows)
	}
}
