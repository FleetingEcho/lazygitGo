package repository

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The Changes page promises that nothing done there loses work, and that every
// action can be taken back with Undo. These tests check that promise as a
// property: a random sequence of the page's real actions runs against a
// workspace holding every kind of change, and afterwards the actions are undone
// newest first. After every single Undo, the whole state a user could lose (all
// file bytes and modes including untracked and ignored files, the index with
// its stages, HEAD, and the stash list) must equal, byte for byte, the state
// from just before that action ran.

type sequenceState struct {
	digest string
	stash  string
}

func sequenceSnapshot(t *testing.T, dir string) sequenceState {
	t.Helper()
	return sequenceState{digest: fullDigest(t, dir), stash: gitOutput(t, dir, "stash", "list", "--format=%H")}
}

func sequenceWorkspace(t *testing.T, rng *rand.Rand) (string, FullSession) {
	t.Helper()
	dir, session := recoveryRepository(t)
	lines := func(prefix string, n int) string {
		var b strings.Builder
		for i := 1; i <= n; i++ {
			fmt.Fprintf(&b, "%s line %d\n", prefix, i)
		}
		return b.String()
	}
	writeSafetyFile(t, dir, ".gitignore", "ignored/\n*.log\n")
	writeSafetyFile(t, dir, "src/main.txt", lines("main", 60))
	writeSafetyFile(t, dir, "src/util.txt", lines("util", 40))
	writeSafetyFile(t, dir, "docs/readme.txt", lines("doc", 20))
	writeSafetyFile(t, dir, "sp ace/ünï code.txt", lines("odd", 12))
	writeSafetyFile(t, dir, "run.sh", "#!/bin/sh\necho hi\n")
	if err := os.Chmod(filepath.Join(dir, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "baseline")

	// Several separated edits per file, so each file has more than one hunk.
	edit := func(name string, every int) {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(string(raw), "\n")
		for i := 2; i < len(parts)-1; i += every {
			switch rng.Intn(3) {
			case 0:
				parts[i] = parts[i] + " EDITED"
			case 1:
				parts[i] = ""
			default:
				parts[i] = parts[i] + "\nINSERTED " + fmt.Sprint(i)
			}
		}
		if err := os.WriteFile(path, []byte(strings.Join(parts, "\n")), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	edit("src/main.txt", 12)
	edit("src/util.txt", 11)
	edit("docs/readme.txt", 9)
	edit("sp ace/ünï code.txt", 5)
	// A partly staged file.
	runGit(t, dir, "add", "src/util.txt")
	writeSafetyFile(t, dir, "src/util.txt", lines("util", 40)+"after staging\n")
	// A deletion, a mode change, untracked, empty, binary and ignored files.
	_ = os.Remove(filepath.Join(dir, "docs/readme.txt"))
	if err := os.Chmod(filepath.Join(dir, "run.sh"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSafetyFile(t, dir, "new/untracked.txt", "untracked\nwork\n")
	writeSafetyFile(t, dir, "empty.txt", "")
	writeSafetyFile(t, dir, "bin.dat", "\x00\x01\x02binary\x00\xff")
	writeSafetyFile(t, dir, "staged-new.txt", "brand new\n")
	runGit(t, dir, "add", "staged-new.txt")
	writeSafetyFile(t, dir, "ignored/cache.bin", "\x00cache"+lines("c", 30))
	writeSafetyFile(t, dir, "build.log", "ignored log\n")
	return dir, session
}

// splitHunks returns "git diff" for one file as a header plus its hunks, each
// a complete patch that applies on its own.
func splitHunks(t *testing.T, dir string, args ...string) []string {
	t.Helper()
	out := gitOutput(t, dir, append([]string{"diff"}, args...)...)
	var header strings.Builder
	var hunks []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			hunks = append(hunks, header.String()+current.String())
			current.Reset()
		}
	}
	for _, line := range strings.SplitAfter(out, "\n") {
		switch {
		case strings.HasPrefix(line, "@@"):
			flush()
			current.WriteString(line)
		case current.Len() == 0:
			header.WriteString(line)
		default:
			current.WriteString(line)
		}
	}
	flush()
	return hunks
}

func newOperationIDs(t *testing.T, session Session, seen map[string]bool) []OperationRecord {
	t.Helper()
	records, err := session.Operations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var fresh []OperationRecord
	for _, r := range records {
		if !seen[r.ID] {
			seen[r.ID] = true
			fresh = append(fresh, r)
		}
	}
	return fresh
}

func TestRandomChangesSequencesAreFullyUndoable(t *testing.T) {
	seeds := 24
	if testing.Short() {
		seeds = 4
	}
	if v, err := strconv.Atoi(os.Getenv("LAZYGITAPP_SEQUENCE_SEEDS")); err == nil && v > 0 {
		seeds = v // a longer hunt: LAZYGITAPP_SEQUENCE_SEEDS=300
	}
	runRandomChangesSequences(t, seeds, 0, true)
}

func runRandomChangesSequences(t *testing.T, seeds, seedBase int, journalOn bool) {
	ctx := context.Background()
	totalOps, totalUndone := 0, 0
	for seed := seedBase + 1; seed <= seedBase+seeds; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(seed)))
			dir, session := sequenceWorkspace(t, rng)
			// The helper turns the journal on; set the switch under test.
			SetSafetyJournalEnabled(journalOn)
			seen := map[string]bool{}
			newOperationIDs(t, session, seen)

			type step struct {
				name   string
				before sequenceState
				record OperationRecord
			}
			var steps []step
			files := func() []File { return snapshotFiles(t, session) }
			pick := func(keep func(File) bool) (File, bool) {
				var candidates []File
				for _, f := range files() {
					if keep(f) {
						candidates = append(candidates, f)
					}
				}
				if len(candidates) == 0 {
					return File{}, false
				}
				return candidates[rng.Intn(len(candidates))], true
			}
			for n := 0; n < 16; n++ {
				before := sequenceSnapshot(t, dir)
				var name string
				var err error
				switch rng.Intn(12) {
				case 0:
					if f, ok := pick(func(f File) bool { return f.Unstaged }); ok {
						name = "stage " + f.Path
						err = session.Stage(ctx, f)
					}
				case 1:
					if f, ok := pick(func(f File) bool { return f.Staged }); ok {
						name = "unstage " + f.Path
						err = session.Unstage(ctx, f)
					}
				case 2:
					name, err = "stage all", session.StageAll(ctx)
				case 3:
					name, err = "unstage all", session.UnstageAll(ctx)
				case 4:
					if f, ok := pick(func(f File) bool { return f.Unstaged && !f.HasMergeConflicts }); ok {
						name = "discard " + f.Path
						err = session.Discard(ctx, f)
					}
				case 5:
					var set []File
					for _, f := range files() {
						if !f.HasMergeConflicts && rng.Intn(3) == 0 {
							set = append(set, f)
						}
					}
					if len(set) > 0 {
						name = fmt.Sprintf("discard %d files", len(set))
						err = session.DiscardFiles(ctx, set)
					}
				case 6, 7:
					// Stage one hunk of a tracked file's unstaged diff.
					if f, ok := pick(func(f File) bool { return f.Unstaged && f.Tracked && !f.Deleted }); ok {
						if hunks := splitHunks(t, dir, "--", f.Path); len(hunks) > 0 {
							h := hunks[rng.Intn(len(hunks))]
							name = fmt.Sprintf("stage a hunk of %s", f.Path)
							err = session.ApplyCachedPatch(ctx, h, false)
						}
					}
				case 8, 9:
					// Discard one hunk from the working tree.
					if f, ok := pick(func(f File) bool { return f.Unstaged && f.Tracked && !f.Deleted }); ok {
						if hunks := splitHunks(t, dir, "--", f.Path); len(hunks) > 0 {
							h := hunks[rng.Intn(len(hunks))]
							name = fmt.Sprintf("discard a hunk of %s", f.Path)
							err = session.ApplyWorkingTreePatch(ctx, h, true)
						}
					}
				case 10:
					if f, ok := pick(func(f File) bool { return !f.Tracked && !f.Staged }); ok {
						name = "ignore " + f.Path
						err = session.IgnoreFile(ctx, f)
					}
				default:
					if n%2 == 0 {
						name, err = "stash", session.StashChanges(ctx, fmt.Sprintf("seq %d", n), rng.Intn(2) == 0)
					} else if hasStaged := gitOutput(t, dir, "diff", "--cached", "--name-only"); strings.TrimSpace(hasStaged) != "" {
						name, err = "commit", session.Commit(ctx, fmt.Sprintf("sequence commit %d", n), "", false)
					}
				}
				if name == "" {
					continue
				}
				totalOps++
				after := sequenceSnapshot(t, dir)
				fresh := newOperationIDs(t, session, seen)
				if err != nil {
					// A refused action must change nothing at all.
					if after != before {
						t.Fatalf("step %d %q failed (%v) but changed the workspace", n, name, err)
					}
					continue
				}
				if after == before {
					continue // nothing changed, nothing to undo
				}
				var record OperationRecord
				for _, r := range fresh {
					if r.CanUndo() {
						record = r
						break
					}
				}
				if record.ID == "" {
					t.Fatalf("step %d %q changed the workspace but left nothing to undo; new records: %#v", n, name, fresh)
				}
				steps = append(steps, step{name: name, before: before, record: record})
			}
			if len(steps) == 0 {
				t.Skip("no action changed anything for this seed")
			}
			for i := len(steps) - 1; i >= 0; i-- {
				s := steps[i]
				if err := session.UndoOperation(ctx, s.record.ID); err != nil {
					t.Fatalf("undo of step %d %q refused: %v", i, s.name, err)
				}
				got := sequenceSnapshot(t, dir)
				if got.digest != s.before.digest {
					t.Fatalf("undo of step %d %q did not restore the state before it.\n%s",
						i, s.name, lineDiff(s.before.digest, got.digest))
				}
				if got.stash != s.before.stash {
					t.Fatalf("undo of step %d %q left the stash list as\n%s\nwant\n%s", i, s.name, got.stash, s.before.stash)
				}
				totalUndone++
			}
		})
	}
	t.Logf("ran %d actions across %d seeds; every Undo restored the exact earlier state (%d undone)", totalOps, seeds, totalUndone)
}

// The Changes page must be fully undoable whatever the Safety Journal switch
// says: the same random sequences run with the journal off.
func TestRandomChangesSequencesAreUndoableWithTheJournalOff(t *testing.T) {
	SetSafetyJournalEnabled(false)
	t.Cleanup(func() { SetSafetyJournalEnabled(true) })
	runRandomChangesSequences(t, 8, 500, false)
}

// Discard leaves the ignored tree out of its recovery point, unless the discard
// can reach an ignored file. Discarding an untracked directory removes
// everything in it, ignored files included, and Undo must bring all of it back.
func TestDiscardingAnUntrackedDirectoryRecoversItsIgnoredFilesToo(t *testing.T) {
	ctx := context.Background()
	dir, session := sequenceWorkspace(t, rand.New(rand.NewSource(5)))
	writeSafetyFile(t, dir, "scratch/notes.txt", "untracked note\n")
	writeSafetyFile(t, dir, "scratch/debug.log", "ignored log inside the directory\n")
	writeSafetyFile(t, dir, "scratch/deep/trace.log", "ignored deeper\n")
	before := sequenceSnapshot(t, dir)
	if err := session.Discard(ctx, File{Path: "scratch/", Unstaged: true}); err != nil {
		t.Fatalf("discard the directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "scratch")); !os.IsNotExist(err) {
		t.Skipf("this Git/lazygit version did not remove the directory (%v)", err)
	}
	record := latestOfKind(t, session, OperationDiscard)
	if !record.CanUndo() {
		t.Fatalf("cannot undo: %#v", record)
	}
	if err := session.UndoOperation(ctx, record.ID); err != nil {
		t.Fatalf("undo: %v", err)
	}
	if got := sequenceSnapshot(t, dir); got != before {
		t.Fatalf("undo did not restore the directory and its ignored files\nbefore:\n%s\nafter:\n%s", before.digest, got.digest)
	}
}

// With a large ignored tree beside the work, discard, stash and commit stay
// exact and fast: the ignored tree is never walked into the recovery point.
func TestChangesActionsDoNotCopyAnUnrelatedIgnoredTree(t *testing.T) {
	ctx := context.Background()
	dir, session := sequenceWorkspace(t, rand.New(rand.NewSource(8)))
	for i := 0; i < 3000; i++ {
		writeSafetyFile(t, dir, fmt.Sprintf("ignored/pkg%02d/obj%04d.o", i%40, i), fmt.Sprintf("object %d\n", i))
	}
	before := sequenceSnapshot(t, dir)
	files := snapshotFiles(t, session)
	var discard []File
	for _, f := range files {
		if f.Tracked && f.Unstaged && !f.Deleted {
			discard = append(discard, f)
		}
	}
	if len(discard) == 0 {
		t.Fatal("workspace has nothing to discard")
	}
	if err := session.DiscardFiles(ctx, discard); err != nil {
		t.Fatal(err)
	}
	record := latestOfKind(t, session, OperationDiscard)
	if record.Snapshot == nil {
		t.Fatalf("discard has no recovery point: %#v", record)
	}
	// The recovery point must not contain the ignored objects.
	listing := gitOutput(t, dir, "ls-tree", "-r", "--name-only", record.Snapshot.Hash)
	if strings.Contains(listing, "obj0001.o") {
		t.Fatalf("the recovery point copied the ignored tree")
	}
	if err := session.UndoOperation(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	if got := sequenceSnapshot(t, dir); got != before {
		t.Fatalf("undo changed the workspace:\n%s\n--- after ---\n%s", before.digest, got.digest)
	}
}

// A build or dev server keeps rewriting ignored files while the user works.
// That must neither stop Undo of a discard nor be rolled back by it.
func TestUndoOfADiscardIgnoresAndKeepsNewerIgnoredFileChanges(t *testing.T) {
	ctx := context.Background()
	dir, session := sequenceWorkspace(t, rand.New(rand.NewSource(12)))
	var target File
	for _, f := range snapshotFiles(t, session) {
		if f.Tracked && f.Unstaged && !f.Deleted && f.Path == "src/main.txt" {
			target = f
		}
	}
	if target.Path == "" {
		t.Fatal("src/main.txt is not modified in the fixture")
	}
	edited, _ := os.ReadFile(filepath.Join(dir, target.Path))
	if err := session.Discard(ctx, target); err != nil {
		t.Fatal(err)
	}
	record := latestOfKind(t, session, OperationDiscard)
	writeSafetyFile(t, dir, "ignored/cache.bin", "rewritten by a build after the discard\n")
	writeSafetyFile(t, dir, "fresh.log", "a new ignored file\n")
	if err := session.UndoOperation(ctx, record.ID); err != nil {
		t.Fatalf("undo refused because an ignored file changed: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, target.Path)); string(got) != string(edited) {
		t.Fatalf("the discarded edits were not restored: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "ignored/cache.bin")); string(got) != "rewritten by a build after the discard\n" {
		t.Fatalf("undo rolled back a newer ignored file: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "fresh.log")); string(got) != "a new ignored file\n" {
		t.Fatalf("undo removed a newer ignored file: %q", got)
	}
}

// lineDiff lists the lines only one of the two digests has, so a failure names
// the files that differ instead of printing both whole states.
func lineDiff(want, got string) string {
	set := func(text string) map[string]bool {
		m := map[string]bool{}
		for _, l := range strings.Split(text, "\n") {
			m[l] = true
		}
		return m
	}
	w, g := set(want), set(got)
	var b strings.Builder
	for _, l := range strings.Split(want, "\n") {
		if !g[l] {
			b.WriteString("  missing after undo: " + l + "\n")
		}
	}
	for _, l := range strings.Split(got, "\n") {
		if !w[l] {
			b.WriteString("  unexpected after undo: " + l + "\n")
		}
	}
	return b.String()
}
