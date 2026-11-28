package repository

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// oracleFileHistory is what Git itself answers for one path, with literal
// pathspecs (a name like "a[1].txt" or "*.rs" is a file name, not a glob).
func (f *conformanceRepo) oracleFileHistory(t *testing.T, path string, extraRefs ...string) []string {
	t.Helper()
	// --topo-order is the order the app's commit loader is configured with.
	args := []string{"--literal-pathspecs", "log", "--follow", "--topo-order", "--format=%H", "HEAD"}
	if len(extraRefs) > 0 {
		args = []string{"--literal-pathspecs", "log", "--follow", "--topo-order", "--show-pulls", "--format=%H", "HEAD"}
	}
	args = append(args, extraRefs...)
	args = append(args, "--", path)
	out := strings.Fields(gitIn(t, f.root, args...))
	if len(out) > 0 {
		return out
	}
	// --follow can lose a path on non-linear history; the exact-path log is
	// the documented fallback.
	args = []string{"--literal-pathspecs", "log", "--no-follow", "--topo-order", "--show-pulls", "--format=%H", "HEAD"}
	args = append(args, extraRefs...)
	args = append(args, "--", path)
	return strings.Fields(gitIn(t, f.root, args...))
}

func TestConformanceFileHistoryMatchesGit(t *testing.T) {
	f := loadConformanceRepo(t)
	ctx := context.Background()
	checked, commitsSeen := 0, 0
	for _, group := range f.interestingPaths(t) {
		if len(group.paths) == 0 {
			t.Logf("group %q: no paths found in this repository", group.name)
			continue
		}
		for _, path := range group.paths {
			want := f.oracleFileHistory(t, path)
			name := fmt.Sprintf("%s: %s", group.name, path)

			got, err := f.session.FileCommits(ctx, path, len(want)+25, 0)
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			if len(want) == 0 && len(got) == 0 {
				checked++
				continue
			}
			if !sameSequence(t, name+" (one read)", hashesOf(got), want) {
				continue
			}
			commitsSeen += len(got)

			// Paging: any page size stitched together equals the one read; no
			// commit repeated or skipped at a page seam.
			for _, size := range []int{1, 7, 50} {
				var stitched []string
				limitReads := min(len(want), 400)
				for skip := 0; skip < limitReads; skip += size {
					page, err := f.session.FileCommits(ctx, path, size, skip)
					if err != nil {
						t.Errorf("%s page size %d skip %d: %v", name, size, skip, err)
						break
					}
					if len(page) > size {
						t.Errorf("%s: page of %d exceeds its limit %d", name, len(page), size)
					}
					stitched = append(stitched, hashesOf(page)...)
					if len(page) == 0 {
						break
					}
				}
				wantPrefix := want[:min(len(want), len(stitched))]
				if len(stitched) < min(len(want), limitReads) {
					t.Errorf("%s: pages of %d ended after %d commits, want at least %d", name, size, len(stitched), min(len(want), limitReads))
				}
				sameSequence(t, fmt.Sprintf("%s (pages of %d)", name, size), stitched, wantPrefix)
				if size == 1 && len(want) > 60 {
					break // one-by-one pages on a long history add nothing
				}
			}

			// Metadata of each listed commit matches the repository.
			for _, c := range got[:min(len(got), 30)] {
				o := f.byHash[c.Hash]
				if o == nil {
					t.Errorf("%s: unknown commit %s", name, c.Hash)
					continue
				}
				if c.Subject != o.Subject || c.AuthorName != o.Name || c.UnixTimestamp != o.At {
					t.Errorf("%s: commit %s metadata differs", name, c.Hash[:10])
				}
			}
			checked++
		}
	}
	t.Logf("verified the file history of %d paths (%d commits in total)", checked, commitsSeen)
}

// Every commit File History lists really changes the file (or a name it was
// renamed from), and every commit that changed the exact path is listed.
func TestConformanceFileHistoryIsSoundAndComplete(t *testing.T) {
	f := loadConformanceRepo(t)
	ctx := context.Background()
	for _, group := range f.interestingPaths(t) {
		for i, path := range group.paths {
			if i >= 6 {
				break
			}
			got, err := f.session.FileCommits(ctx, path, 100000, 0)
			if err != nil {
				t.Errorf("%s: %v", path, err)
				continue
			}
			listed := map[string]bool{}
			for _, c := range got {
				if listed[c.Hash] {
					t.Errorf("%s: commit %s listed twice", path, c.Hash[:10])
				}
				listed[c.Hash] = true
			}
			// Complete: all commits whose own diff touches the exact path.
			// Merges are excluded: File History follows renames, and a merge
			// only enters it through --show-pulls (when several refs are read).
			exact := strings.Fields(gitIn(t, f.root, "--literal-pathspecs", "log", "--no-follow", "--no-merges", "--format=%H", "HEAD", "--", path))
			for _, h := range exact {
				if !listed[h] {
					t.Errorf("%s (%s): commit %s changed the path but is not in the history", path, group.name, h[:10])
					break
				}
			}
			// Sound: each listed commit's changes include a name in the chain. Git's
			// --follow also follows copies, so the check detects them too.
			names := map[string]bool{path: true}
			var input strings.Builder
			for _, c := range got {
				fmt.Fprintln(&input, c.Hash)
			}
			if len(got) == 0 {
				continue
			}
			out := gitStdin(t, f.root, input.String(), "diff-tree", "--stdin", "--root", "-r", "-m", "-M", "-C", "--find-copies-harder", "--name-status")
			current := ""
			touched := map[string]bool{}
			for _, line := range strings.Split(out, "\n") {
				p := strings.Split(line, "\t")
				switch {
				case len(p) == 1 && len(strings.Fields(line)) > 0 && len(strings.Fields(line)[0]) == 40:
					current = strings.Fields(line)[0] // "<hash>" or "<hash> (from <parent>)"
				case len(p) >= 2 && current != "":
					hit := false
					for _, n := range p[1:] {
						if names[n] {
							hit = true
						}
					}
					if hit {
						touched[current] = true
						for _, n := range p[1:] {
							names[n] = true // a rename extends the chain
						}
					}
				}
			}
			unrelated := 0
			for _, c := range got {
				if !touched[c.Hash] {
					unrelated++
					if unrelated <= 3 {
						t.Errorf("%s (%s): listed commit %s does not change the file", path, group.name, c.Hash[:10])
					}
				}
			}
		}
	}
}

func TestConformanceFileHistoryIncludingAnotherCommit(t *testing.T) {
	f := loadConformanceRepo(t)
	ctx := context.Background()
	origins := []string{
		strings.TrimSpace(gitIn(t, f.root, "rev-parse", "1.16.0^{commit}")),
		strings.TrimSpace(gitIn(t, f.root, "rev-parse", "origin/Integration_3.0.0^{commit}")),
		f.all[len(f.all)/2].Hash,
		f.all[len(f.all)-1].Hash,
		f.all[0].Hash,
	}
	paths := f.interestingPaths(t)
	checked := 0
	for _, group := range paths {
		for i, path := range group.paths {
			if i >= 4 || group.name == "missing" {
				continue
			}
			for _, origin := range origins {
				want := f.oracleFileHistory(t, path, origin)
				got, err := f.session.FileCommitsIncluding(ctx, path, origin, len(want)+25, 0)
				if err != nil {
					t.Errorf("%s from %s: %v", path, origin[:8], err)
					continue
				}
				if len(want) == 0 && len(got) == 0 {
					continue
				}
				name := fmt.Sprintf("%s (%s) including %s", path, group.name, origin[:8])
				if !sameSequence(t, name, hashesOf(got), want) {
					continue
				}
				var stitched []string
				for skip := 0; skip < min(len(want), 200); skip += 13 {
					page, err := f.session.FileCommitsIncluding(ctx, path, origin, 13, skip)
					if err != nil {
						t.Errorf("%s page %d: %v", name, skip, err)
						break
					}
					stitched = append(stitched, hashesOf(page)...)
				}
				sameSequence(t, name+" (pages of 13)", stitched, want[:min(len(want), len(stitched))])
				checked++
			}
		}
	}
	t.Logf("verified %d path/origin combinations", checked)
	if _, err := f.session.FileCommitsIncluding(ctx, "README.rst", "not-a-hash", 10, 0); err == nil {
		t.Error("an abbreviated or invalid origin must be refused")
	}
	if _, err := f.session.FileCommits(ctx, "", 10, 0); err == nil {
		t.Error("an empty path must be refused")
	}
	if _, err := f.session.FileCommits(ctx, "README.rst", 0, 0); err == nil {
		t.Error("a zero limit must be refused")
	}
}

func gitStdin(t testing.TB, dir, input string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.quotepath=off"}, args...)...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return string(out)
}
