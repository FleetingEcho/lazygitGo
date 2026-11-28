package repository

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func journalCounts(t *testing.T, session FullSession) (operations, snapshots int) {
	t.Helper()
	records, err := session.Operations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	points, err := session.WorkSnapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(records), len(points)
}

// With the safety journal off, the actions beyond the Changes page (apply and
// drop a stash, delete a branch) take no recovery point and add no journal
// entry. What is done from the Changes page (stash, discard) is still recorded:
// that work exists nowhere else.
func TestOnlyActionsBeyondTheChangesPageRunUnrecordedWhenTheSafetyJournalIsOff(t *testing.T) {
	dir, session := recoveryRepository(t)
	SetSafetyJournalEnabled(false)
	ctx := context.Background()

	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("note\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := session.StashSelectedFiles(ctx, "off", []File{{Path: "note.txt", Unstaged: true}}, false); err != nil {
		t.Fatalf("stash with the journal off: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "note.txt")); !os.IsNotExist(err) {
		t.Fatalf("the stash did not take the file: %v", err)
	}
	if err := session.ApplyStash(ctx, 0, false); err != nil {
		t.Fatalf("apply with the journal off: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "note.txt")); err != nil {
		t.Fatalf("apply did not restore the file: %v", err)
	}
	if err := session.Discard(ctx, File{Path: "note.txt", Unstaged: true}); err != nil {
		t.Fatalf("discard with the journal off: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "note.txt")); !os.IsNotExist(err) {
		t.Fatalf("discard left the file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := session.DiscardFiles(ctx, []File{{Path: "a.txt", Tracked: true, Unstaged: true}}); err != nil {
		t.Fatalf("discard files with the journal off: %v", err)
	}
	if content, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(content) != "base\n" {
		t.Fatalf("discard files did not restore the tracked file: %q", content)
	}
	if err := session.DropStash(ctx, 0); err != nil {
		t.Fatalf("drop stash with the journal off: %v", err)
	}
	if out := gitOutput(t, dir, "stash", "list"); out != "" {
		t.Fatalf("the stash was not dropped: %q", out)
	}
	runGit(t, dir, "branch", "topic")
	if err := session.DeleteBranch(ctx, "topic", false); err != nil {
		t.Fatalf("delete branch with the journal off: %v", err)
	}
	if out := gitOutput(t, dir, "branch", "--list", "topic"); out != "" {
		t.Fatalf("the branch was not deleted: %q", out)
	}
	records, err := session.Operations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, record := range records {
		kinds[record.Kind]++
	}
	for _, kind := range []string{OperationStashApply, OperationStashDrop, OperationBranchDelete} {
		if kinds[kind] != 0 {
			t.Fatalf("journal off but %s was recorded (%v)", kind, kinds)
		}
	}
	if kinds[OperationStashSave] != 1 || kinds[OperationDiscard] != 2 {
		t.Fatalf("Changes-page actions must be recorded with the journal off: %v", kinds)
	}
	for _, record := range records {
		if (record.Kind == OperationDiscard || record.Kind == OperationStashSave) && !record.CanUndo() {
			t.Fatalf("%s cannot be undone with the journal off: %#v", record.Kind, record)
		}
	}
}

// With it on, the same actions are recorded, so turning it off is the only
// thing that removes the safety net.
func TestActionsKeepRecoveryPointsWhileTheSafetyJournalIsOn(t *testing.T) {
	SetSafetyJournalEnabled(true)
	t.Cleanup(func() { SetSafetyJournalEnabled(false) })
	dir, session := recoveryRepository(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("note\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := session.Discard(ctx, File{Path: "note.txt", Unstaged: true}); err != nil {
		t.Fatal(err)
	}
	if operations, snapshots := journalCounts(t, session); operations == 0 || snapshots == 0 {
		t.Fatalf("a discard with the journal on recorded %d entries and %d recovery points", operations, snapshots)
	}
}
