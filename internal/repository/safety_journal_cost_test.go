package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestSafetyJournalCost measures what the Safety Journal adds to each action on
// a repository with and without a large ignored build
// directory. It is a measurement, so it only logs; set LAZYGITAPP_JOURNAL_COST=1
// to run it. The same actions are timed with the journal off and on.
func TestSafetyJournalCost(t *testing.T) {
	if os.Getenv("LAZYGITAPP_JOURNAL_COST") == "" {
		t.Skip("set LAZYGITAPP_JOURNAL_COST=1 to measure")
	}
	f := loadConformanceRepo(t)
	ctx := context.Background()
	dirty := func(n int) []string {
		out := gitOutput(t, f.root, "ls-files", "src/*.rs")
		var paths []string
		for _, p := range strings.Fields(out) {
			paths = append(paths, p)
			if len(paths) == n {
				break
			}
		}
		for _, p := range paths {
			file := filepath.Join(f.root, p)
			raw, _ := os.ReadFile(file)
			_ = os.WriteFile(file, append(raw, []byte("\n// journal cost probe\n")...), 0o644)
		}
		return paths
	}
	reset := func() {
		runGit(t, f.root, "checkout", "--", ".")
		runGit(t, f.root, "reset", "-q")
		runGit(t, f.root, "clean", "-fdq", "--", "probe")
	}
	scenarios := []struct {
		name string
		run  func() error
	}{
		{"stage 3 files", func() error {
			for _, p := range dirty(3) {
				if err := f.session.Stage(ctx, File{Path: p, Tracked: true, Unstaged: true}); err != nil {
					return err
				}
			}
			return nil
		}},
		{"stage all (30 dirty files)", func() error { dirty(30); return f.session.StageAll(ctx) }},
		{"discard 3 files", func() error {
			var files []File
			for _, p := range dirty(3) {
				files = append(files, File{Path: p, Tracked: true, Unstaged: true})
			}
			return f.session.DiscardFiles(ctx, files)
		}},
		{"discard 30 files", func() error {
			var files []File
			for _, p := range dirty(30) {
				files = append(files, File{Path: p, Tracked: true, Unstaged: true})
			}
			return f.session.DiscardFiles(ctx, files)
		}},
		{"stash 30 files", func() error { dirty(30); return f.session.StashChanges(ctx, "probe", false) }},
	}
	bigIgnored := func(files int) {
		dir := filepath.Join(f.root, "target-probe")
		_ = os.MkdirAll(dir, 0o755)
		for i := 0; i < files; i++ {
			sub := filepath.Join(dir, fmt.Sprintf("d%03d", i%100))
			_ = os.MkdirAll(sub, 0o755)
			_ = os.WriteFile(filepath.Join(sub, fmt.Sprintf("f%05d.o", i)), []byte(strings.Repeat("x", 2048)+fmt.Sprint(i)), 0o644)
		}
		exclude := filepath.Join(f.root, ".git", "info", "exclude")
		raw, _ := os.ReadFile(exclude)
		if !strings.Contains(string(raw), "target-probe") {
			_ = os.WriteFile(exclude, append(raw, []byte("\ntarget-probe/\n")...), 0o644)
		}
	}
	measure := func(label string) {
		type row struct {
			name     string
			off, on  time.Duration
			onSecond time.Duration
		}
		var rows []row
		for _, sc := range scenarios {
			var r row
			r.name = sc.name
			for _, enabled := range []bool{false, true, true} {
				reset()
				SetSafetyJournalEnabled(enabled)
				start := time.Now()
				if err := sc.run(); err != nil {
					t.Fatalf("%s (journal %v): %v", sc.name, enabled, err)
				}
				took := time.Since(start)
				switch {
				case !enabled:
					r.off = took
				case r.on == 0:
					r.on = took
				default:
					r.onSecond = took
				}
			}
			rows = append(rows, r)
		}
		SetSafetyJournalEnabled(false)
		sort.SliceStable(rows, func(i, j int) bool { return false })
		t.Logf("--- %s ---", label)
		for _, r := range rows {
			t.Logf("%-28s off %8s   on %8s   on (2nd run) %8s   cost x%.1f", r.name, r.off.Round(time.Millisecond),
				r.on.Round(time.Millisecond), r.onSecond.Round(time.Millisecond), float64(r.onSecond)/float64(max(r.off, time.Millisecond)))
		}
	}
	t.Cleanup(func() { SetSafetyJournalEnabled(false); reset() })
	measure("the conformance worktree (nothing ignored)")
	bigIgnored(20000)
	measure("same, plus 20,000 files in an ignored build directory")
}
