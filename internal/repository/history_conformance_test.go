package repository

// History, File History and filter conformance tests.
//
// These run the repository session against a large history and compare every
// answer with an oracle built from plain `git` output parsed here in Go. The
// oracle never reuses the app's own argument building or parsing: filter
// semantics (message, author, time window, branch) are re-implemented as
// straight string/integer predicates over the full commit list, so a wrong git
// flag, a lost commit, a wrong order or a wrong count in the app shows up as a
// precise mismatch.
//
// By default the history is a deterministic synthetic repository (see
// buildConformanceRepository: merges, renames, deleted and oddly named files,
// clock skew, regex characters in names, hexadecimal words in messages), so the
// tests run on any machine and in every full test run. To also check a big real
// history, point LAZYGITAPP_REAL_REPO at a clone (the original fish-shell
// history, ~23k commits, takes ~20 minutes); it is COPIED to a temporary
// directory first, so opening a session can never touch the original. The
// real-repository run is opt-in because it is slow and needs that clone.

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type oracleCommit struct {
	Hash    string
	At, Ct  int64 // author and committer time
	Name    string
	Email   string
	Parents []string
	Subject string
	Message string // full raw message, as git --grep sees it
}

type conformanceRepo struct {
	root    string
	session FullSession
	all     []oracleCommit // git log --all, git's default order
	byHash  map[string]*oracleCommit
	topo    []string // git rev-list --all --topo-order
}

var (
	conformanceOnce sync.Once
	conformanceFix  *conformanceRepo
	conformanceErr  error
	conformanceSkip string
)

// realRepoSource is the clone the user asked to check instead of the synthetic
// history ("" when none was asked for).
func realRepoSource() string { return os.Getenv("LAZYGITAPP_REAL_REPO") }

var conformanceRoot string // the temporary directory holding the repository

// cleanupConformanceRepo removes the repository the tests built or copied.
func cleanupConformanceRepo() {
	if conformanceRoot != "" {
		_ = os.RemoveAll(conformanceRoot)
	}
}

func gitIn(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.quotepath=off", "-c", "log.showSignature=false"}, args...)...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return string(out)
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func loadConformanceRepo(t *testing.T) *conformanceRepo {
	t.Helper()
	conformanceOnce.Do(func() {
		dir, err := os.MkdirTemp("", "lazygitgo-conformance-*")
		if err != nil {
			conformanceErr = err
			return
		}
		conformanceRoot = dir
		root := filepath.Join(dir, "repo")
		if src := realRepoSource(); src != "" {
			if _, err := os.Stat(filepath.Join(src, ".git")); err != nil {
				conformanceSkip = "LAZYGITAPP_REAL_REPO " + src + " is not a Git repository"
				return
			}
			if out, err := exec.Command("cp", "-a", src, root).CombinedOutput(); err != nil {
				conformanceErr = fmt.Errorf("copy repository: %v: %s", err, out)
				return
			}
		} else {
			commits := 2500
			if testing.Short() {
				commits = 500
			}
			buildConformanceRepository(t, root, commits)
		}
		fix := &conformanceRepo{root: root, byHash: map[string]*oracleCommit{}}
		raw, err := exec.Command("git", "-C", root, "log", "--exclude=refs/tangit/*", "--all",
			"--format=%x1e%H%x1f%at%x1f%ct%x1f%an%x1f%ae%x1f%P%x1f%s%x1f%B").Output()
		if err != nil {
			conformanceErr = err
			return
		}
		for _, rec := range strings.Split(string(raw), "\x1e") {
			if rec == "" {
				continue
			}
			f := strings.SplitN(rec, "\x1f", 8)
			if len(f) != 8 {
				conformanceErr = fmt.Errorf("oracle: malformed record %q", rec)
				return
			}
			at, _ := strconv.ParseInt(f[1], 10, 64)
			ct, _ := strconv.ParseInt(f[2], 10, 64)
			fix.all = append(fix.all, oracleCommit{
				Hash: f[0], At: at, Ct: ct, Name: f[3], Email: f[4],
				Parents: strings.Fields(f[5]), Subject: f[6], Message: strings.TrimRight(f[7], "\n"),
			})
		}
		for i := range fix.all {
			fix.byHash[fix.all[i].Hash] = &fix.all[i]
		}
		topo, err := exec.Command("git", "-C", root, "rev-list", "--exclude=refs/tangit/*", "--all", "--topo-order").Output()
		if err != nil {
			conformanceErr = err
			return
		}
		fix.topo = strings.Fields(string(topo))
		session, err := openFullRepositorySessionForTest((Opener{}).Open(context.Background(), root))
		if err != nil {
			conformanceErr = err
			return
		}
		fix.session = session
		conformanceFix = fix
	})
	if conformanceSkip != "" {
		t.Skip(conformanceSkip)
	}
	if conformanceErr != nil {
		t.Fatal(conformanceErr)
	}
	if conformanceFix == nil {
		t.Fatal("the conformance repository could not be built")
	}
	return conformanceFix
}

func hashesOf(commits []Commit) []string {
	out := make([]string, len(commits))
	for i, c := range commits {
		out[i] = c.Hash
	}
	return out
}

// sameSequence fails with the first divergence, the lengths, and the missing
// and unexpected hashes, so a mismatch explains itself.
func sameSequence(t *testing.T, name string, got, want []string) bool {
	t.Helper()
	if len(got) == len(want) {
		same := true
		for i := range got {
			if got[i] != want[i] {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	gotSet, wantSet := map[string]bool{}, map[string]bool{}
	for _, h := range got {
		gotSet[h] = true
	}
	for _, h := range want {
		wantSet[h] = true
	}
	var missing, extra []string
	for _, h := range want {
		if !gotSet[h] {
			missing = append(missing, h[:10])
		}
	}
	for _, h := range got {
		if !wantSet[h] {
			extra = append(extra, h[:10])
		}
	}
	first := -1
	for i := 0; i < len(got) && i < len(want); i++ {
		if got[i] != want[i] {
			first = i
			break
		}
	}
	cut := func(s []string) []string {
		if len(s) > 8 {
			return append(append([]string{}, s[:8]...), fmt.Sprintf("…(+%d)", len(s)-8))
		}
		return s
	}
	t.Errorf("%s: got %d commits, want %d; first divergence at index %d; missing %v; unexpected %v; same set=%v",
		name, len(got), len(want), first, cut(missing), cut(extra), len(missing) == 0 && len(extra) == 0)
	return false
}

func (f *conformanceRepo) filter(keep func(*oracleCommit) bool) []string {
	var out []string
	for i := range f.all {
		if keep(&f.all[i]) {
			out = append(out, f.all[i].Hash)
		}
	}
	return out
}

func (f *conformanceRepo) revList(t *testing.T, args ...string) []string {
	t.Helper()
	return strings.Fields(gitIn(t, f.root, append([]string{"rev-list"}, args...)...))
}

func messageHas(c *oracleCommit, query string) bool {
	return strings.Contains(asciiLower(c.Message), asciiLower(query))
}

func authorHas(c *oracleCommit, query string) bool {
	// git matches --author against "Name <email>".
	return strings.Contains(asciiLower(c.Name+" <"+c.Email+">"), asciiLower(query))
}

func (f *conformanceRepo) limit() int { return len(f.all) + 10 }

// ---------------------------------------------------------------------------

func TestConformanceRepositoryShape(t *testing.T) {
	f := loadConformanceRepo(t)
	count := strings.TrimSpace(gitIn(t, f.root, "rev-list", "--count", "--exclude=refs/tangit/*", "--all"))
	if strconv.Itoa(len(f.all)) != count {
		t.Fatalf("oracle has %d commits, git says %s", len(f.all), count)
	}
	if len(f.all) < 400 {
		t.Fatalf("expected a large repository, got %d commits", len(f.all))
	}
}

// RecentCommits is the plain History list. Paged in any chunk size it must be
// every commit exactly once, in an order where no parent precedes its child,
// with correct metadata.
func TestConformanceRecentCommitsPagingIsCompleteAndOrdered(t *testing.T) {
	f := loadConformanceRepo(t)
	ctx := context.Background()
	for _, page := range []int{1000, 777} {
		var all []Commit
		for skip := 0; ; skip += page {
			commits, err := f.session.RecentCommits(ctx, page, skip)
			if err != nil {
				t.Fatalf("page %d skip %d: %v", page, skip, err)
			}
			all = append(all, commits...)
			if len(commits) < page {
				break
			}
		}
		got := hashesOf(all)
		seen := map[string]int{}
		for i, h := range got {
			if j, dup := seen[h]; dup {
				t.Fatalf("page=%d: %s appears twice (%d and %d)", page, h[:10], j, i)
			}
			seen[h] = i
		}
		if len(got) != len(f.all) {
			t.Errorf("page=%d: %d commits, want %d", page, len(got), len(f.all))
		}
		for _, c := range f.all {
			if _, ok := seen[c.Hash]; !ok {
				t.Errorf("page=%d: commit %s missing", page, c.Hash[:10])
				break
			}
		}
		// Order: a parent never comes before its child.
		for i, c := range all {
			for _, p := range c.Parents {
				if j, ok := seen[p]; ok && j < i {
					t.Fatalf("page=%d: parent %s (index %d) listed before child %s (index %d)", page, p[:10], j, c.Hash[:10], i)
				}
			}
		}
		// Fields.
		bad := 0
		for _, c := range all {
			o := f.byHash[c.Hash]
			if o == nil {
				continue
			}
			if c.Subject != o.Subject || c.AuthorName != o.Name || c.AuthorEmail != o.Email ||
				c.UnixTimestamp != o.At || strings.Join(c.Parents, " ") != strings.Join(o.Parents, " ") ||
				!strings.HasPrefix(c.Hash, c.ShortHash) || c.ShortHash == "" {
				bad++
				if bad <= 5 {
					t.Errorf("page=%d: commit %s metadata differs: got %+v want %+v", page, c.Hash[:10], c, *o)
				}
			}
		}
		if bad > 0 {
			t.Errorf("page=%d: %d commits with wrong metadata", page, bad)
		}
	}
}

// The order of RecentCommits must equal git's own topological order, which is
// what the lazygit loader asks for; this catches an ordering regression even
// when every parent/child rule still holds.
func TestConformanceRecentCommitsEqualsGitOrder(t *testing.T) {
	f := loadConformanceRepo(t)
	commits, err := f.session.RecentCommits(context.Background(), len(f.all)+10, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := hashesOf(commits)
	if sameSequence(t, "RecentCommits vs git --topo-order", got, f.topo) {
		return
	}
	sameSequence(t, "RecentCommits vs git default order", got, f.filter(func(*oracleCommit) bool { return true }))
}

func TestConformanceLineStatsMatchGitNumstat(t *testing.T) {
	f := loadConformanceRepo(t)
	ctx := context.Background()
	rng := rand.New(rand.NewSource(7))
	var sample []Commit
	commits, err := f.session.SearchCommits(ctx, CommitFilter{Limit: f.limit()})
	if err != nil {
		t.Fatal(err)
	}
	merges, roots := 0, 0
	for i, c := range commits {
		switch {
		case i < 40 || i >= len(commits)-40:
			sample = append(sample, c)
		case len(c.Parents) == 0:
			sample = append(sample, c)
			roots++
		case len(c.Parents) > 1 && merges < 60:
			sample = append(sample, c)
			merges++
		case rng.Intn(100) == 0:
			sample = append(sample, c)
		}
	}
	const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
	bad := 0
	for _, c := range sample {
		base := emptyTree
		if len(c.Parents) > 0 {
			base = c.Parents[0]
		}
		added, deleted := 0, 0
		for _, line := range strings.Split(gitIn(t, f.root, "diff", "--numstat", "--find-renames=50%", base, c.Hash), "\n") {
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) < 3 {
				continue
			}
			if a, err := strconv.Atoi(parts[0]); err == nil {
				added += a
			}
			if d, err := strconv.Atoi(parts[1]); err == nil {
				deleted += d
			}
		}
		if c.LinesAdded != added || c.LinesDeleted != deleted || c.StatsPending {
			bad++
			if bad <= 8 {
				t.Errorf("%s (%d parents): app +%d -%d pending=%v, git numstat +%d -%d", c.Hash[:10], len(c.Parents), c.LinesAdded, c.LinesDeleted, c.StatsPending, added, deleted)
			}
		}
	}
	t.Logf("checked line counts of %d commits (%d merges, %d roots), %d wrong", len(sample), merges, roots, bad)
}

func TestConformanceCommitFilesMatchGit(t *testing.T) {
	f := loadConformanceRepo(t)
	ctx := context.Background()
	rng := rand.New(rand.NewSource(11))
	commits, err := f.session.RecentCommits(ctx, 4000, 0)
	if err != nil {
		t.Fatal(err)
	}
	var sample []Commit
	for i, c := range commits {
		if i < 25 || len(c.Parents) > 1 && len(sample) < 80 || rng.Intn(60) == 0 {
			sample = append(sample, c)
		}
	}
	const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
	for _, c := range sample {
		base := emptyTree
		if len(c.Parents) > 0 {
			base = c.Parents[0]
		}
		want := map[string]string{}
		for _, line := range strings.Split(gitIn(t, f.root, "diff", "--name-status", "--find-renames=50%", base, c.Hash), "\n") {
			p := strings.Split(line, "\t")
			if len(p) >= 2 {
				want[p[len(p)-1]] = p[0][:1]
			}
		}
		files, err := f.session.CommitFiles(ctx, c)
		if err != nil {
			t.Errorf("%s: %v", c.Hash[:10], err)
			continue
		}
		got := map[string]string{}
		for _, file := range files {
			if _, dup := got[file.Path]; dup {
				t.Errorf("%s: file %s listed twice", c.Hash[:10], file.Path)
			}
			got[file.Path] = file.Status
		}
		if len(got) != len(want) {
			t.Errorf("%s: %d files, git lists %d", c.Hash[:10], len(got), len(want))
			continue
		}
		for path, status := range want {
			g, ok := got[path]
			if !ok {
				t.Errorf("%s: file %s missing", c.Hash[:10], path)
			} else if !strings.HasPrefix(strings.ToUpper(g), status) {
				t.Errorf("%s: file %s status %q, git says %q", c.Hash[:10], path, g, status)
			}
		}
	}
}

func TestConformanceSnapshotMatchesRefs(t *testing.T) {
	f := loadConformanceRepo(t)
	snap, err := f.session.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantCommits := min(SnapshotCommitLimit, len(f.all))
	if len(snap.Commits) != wantCommits {
		t.Errorf("snapshot has %d commits, want %d", len(snap.Commits), wantCommits)
	}
	recent, err := f.session.RecentCommits(context.Background(), wantCommits, 0)
	if err != nil {
		t.Fatal(err)
	}
	sameSequence(t, "snapshot commits vs RecentCommits", hashesOf(snap.Commits), hashesOf(recent))

	wantTags := strings.Fields(gitIn(t, f.root, "for-each-ref", "--format=%(refname:short)", "refs/tags"))
	var gotTags []string
	for _, tag := range snap.Tags {
		gotTags = append(gotTags, tag.Name)
	}
	sort.Strings(wantTags)
	sort.Strings(gotTags)
	if strings.Join(gotTags, "\n") != strings.Join(wantTags, "\n") {
		t.Errorf("snapshot tags differ: got %d, want %d", len(gotTags), len(wantTags))
	}
	wantBranches := strings.Fields(gitIn(t, f.root, "for-each-ref", "--format=%(refname:short)", "refs/heads"))
	var gotBranches []string
	for _, b := range snap.Branches {
		gotBranches = append(gotBranches, b.Name)
	}
	sort.Strings(wantBranches)
	sort.Strings(gotBranches)
	if strings.Join(gotBranches, "\n") != strings.Join(wantBranches, "\n") {
		t.Errorf("snapshot local branches differ: got %v, want %v", gotBranches, wantBranches)
	}
	head := strings.TrimSpace(gitIn(t, f.root, "rev-parse", "HEAD"))
	if snap.HeadHash != head {
		t.Errorf("snapshot HEAD %s, git %s", snap.HeadHash, head)
	}

	// Each commit's tag list is exactly the tags that point at it (peeled).
	pointing := map[string][]string{}
	for _, line := range strings.Split(gitIn(t, f.root, "for-each-ref", "--format=%(refname:short) %(objectname) %(*objectname)", "refs/tags"), "\n") {
		p := strings.Fields(line)
		switch len(p) {
		case 2:
			pointing[p[1]] = append(pointing[p[1]], p[0])
		case 3:
			pointing[p[2]] = append(pointing[p[2]], p[0])
		}
	}
	bad := 0
	for _, c := range recent {
		w := append([]string(nil), pointing[c.Hash]...)
		g := append([]string(nil), c.Tags...)
		sort.Strings(w)
		sort.Strings(g)
		if strings.Join(w, ",") != strings.Join(g, ",") {
			bad++
			if bad <= 5 {
				t.Errorf("commit %s tags: got %v want %v", c.Hash[:10], g, w)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Filters

func TestConformanceSearchWithoutFilterAndLimits(t *testing.T) {
	f := loadConformanceRepo(t)
	ctx := context.Background()
	all := f.filter(func(*oracleCommit) bool { return true })
	got, err := f.session.SearchCommits(ctx, CommitFilter{Limit: f.limit()})
	if err != nil {
		t.Fatal(err)
	}
	sameSequence(t, "unfiltered search", hashesOf(got), all)
	for _, n := range []int{1, 2, 49, 50, 51, 500, 5000, len(all) - 1, len(all), len(all) + 1} {
		got, err := f.session.SearchCommits(ctx, CommitFilter{Limit: n})
		if err != nil {
			t.Fatalf("limit %d: %v", n, err)
		}
		sameSequence(t, fmt.Sprintf("limit %d", n), hashesOf(got), all[:min(n, len(all))])
	}
	if _, err := f.session.SearchCommits(ctx, CommitFilter{}); err == nil {
		t.Error("a search without a limit must be refused")
	}
}

var conformanceQueries = []string{
	"fix", "FIX", "Fix", "fish", "FISH", "fish_", "fish_indent", "fish-shell",
	"Merge pull request", "merge pull request #", "Merge branch",
	"[", "]", "(", ")", ".*", ".", "*", "+", "?", "^", "$", "|", "\\", "/", "'", "\"", "`", "%", "%s", "%H", "!", "~", "@", "#", "{", "}", "<", ">", "=", ",", ":", ";", "&",
	"#1", "#12", "(#", "Fixes #", "fixes #1", "closes #",
	"é", "ü", "日本", "—", "→", "…",
	"-x", "--all", "--help", "-", "--",
	"multiple words in a row", "the", "a",
	// Hexadecimal-looking words: also valid hash prefixes.
	"dead", "face", "fade", "feed", "beef", "cafe", "added", "decade", "deface", "accede", "bad", "abc", "1234", "0000",
	"nonexistentzzqq", "zzzzzzzz",
}

func TestConformanceQueryFilterMatchesGitSemantics(t *testing.T) {
	f := loadConformanceRepo(t)
	ctx := context.Background()
	for _, query := range conformanceQueries {
		q := strings.TrimSpace(query)
		want := f.filter(func(c *oracleCommit) bool { return q == "" || messageHas(c, q) })
		// A query that is 4-64 hex digits and names exactly one commit lists
		// that commit first, then every message that contains the text.
		if len(q) >= 4 && len(q) <= 64 && strings.Trim(asciiLower(q), "0123456789abcdef") == "" {
			var named []string
			for _, c := range f.all {
				if strings.HasPrefix(c.Hash, asciiLower(q)) {
					named = append(named, c.Hash)
				}
			}
			if len(named) == 1 {
				rest := want[:0:0]
				for _, h := range want {
					if h != named[0] {
						rest = append(rest, h)
					}
				}
				want = append([]string{named[0]}, rest...)
			}
		}
		got, err := f.session.SearchCommits(ctx, CommitFilter{Query: query, Limit: f.limit()})
		if err != nil {
			t.Errorf("query %q: %v", query, err)
			continue
		}
		if len(want) == 0 && len(got) == 0 {
			continue
		}
		sameSequence(t, fmt.Sprintf("query %q", query), hashesOf(got), want)
	}
}

// A query that is a full or abbreviated hash finds that commit; every other
// commit hash prefix behaves the same through the whole-history filter.
func TestConformanceHashQueries(t *testing.T) {
	f := loadConformanceRepo(t)
	ctx := context.Background()
	rng := rand.New(rand.NewSource(3))
	for n := 0; n < 40; n++ {
		c := f.all[rng.Intn(len(f.all))]
		for _, l := range []int{40, 20, 12, 8, 7} {
			got, err := f.session.SearchCommits(ctx, CommitFilter{Query: c.Hash[:l], Limit: 50})
			if err != nil {
				t.Errorf("hash %s: %v", c.Hash[:l], err)
				continue
			}
			var want []string
			for _, o := range f.all {
				if strings.HasPrefix(o.Hash, c.Hash[:l]) {
					want = append(want, o.Hash)
				}
			}
			if len(want) > 1 {
				continue // an ambiguous prefix has no single right answer
			}
			// The hash's own commit must be there; messages that quote it too.
			found := false
			for _, g := range got {
				if g.Hash == c.Hash {
					found = true
				}
			}
			if !found {
				t.Errorf("query %s: commit %s not found (%d results)", c.Hash[:l], c.Hash[:10], len(got))
			}
		}
		upper, err := f.session.SearchCommits(ctx, CommitFilter{Query: strings.ToUpper(c.Hash[:12]), Limit: 50})
		if err != nil || len(upper) == 0 || upper[0].Hash != c.Hash {
			t.Errorf("upper-case hash query for %s: %v %d", c.Hash[:10], err, len(upper))
		}
	}
}

func TestConformanceAuthorsListMatchesHistory(t *testing.T) {
	f := loadConformanceRepo(t)
	got, err := f.session.Authors(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range f.all {
		if n := strings.TrimSpace(c.Name); n != "" {
			seen[n] = true
		}
	}
	var want []string
	for n := range seen {
		want = append(want, n)
	}
	sort.Slice(want, func(i, j int) bool {
		l, r := strings.ToLower(want[i]), strings.ToLower(want[j])
		if l == r {
			return want[i] < want[j]
		}
		return l < r
	})
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("Authors: got %d names, want %d", len(got), len(want))
		gs := map[string]bool{}
		for _, n := range got {
			gs[n] = true
		}
		for _, n := range want {
			if !gs[n] {
				t.Errorf("missing author %q", n)
				break
			}
		}
	}
	t.Logf("%d distinct authors", len(got))
}

func TestConformanceAuthorFilterMatchesGitSemantics(t *testing.T) {
	f := loadConformanceRepo(t)
	ctx := context.Background()
	authors, err := f.session.Authors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(5))
	var picks []string
	for _, a := range authors {
		if strings.ContainsAny(a, `.[]()*+?^$|\{}`) {
			picks = append(picks, a) // regex metacharacters in a real name
		}
	}
	for i := 0; i < 40; i++ {
		picks = append(picks, authors[rng.Intn(len(authors))])
	}
	picks = append(picks, "gmail.com", "noreply", "@", "users.noreply.github.com", "ZANCHEY", "zanchey", ".", "[", "(", `\`, "*", "+", "<", " ", "a")
	for _, author := range picks {
		a := strings.TrimSpace(author)
		want := f.filter(func(c *oracleCommit) bool { return a == "" || authorHas(c, a) })
		got, err := f.session.SearchCommits(ctx, CommitFilter{Author: author, Limit: f.limit()})
		if err != nil {
			t.Errorf("author %q: %v", author, err)
			continue
		}
		if len(want) == 0 && len(got) == 0 {
			continue
		}
		sameSequence(t, fmt.Sprintf("author %q", author), hashesOf(got), want)
	}
}

func minuteFloor(unix int64) int64 { return unix - unix%60 }

func TestConformanceTimeWindowMatchesGitSemantics(t *testing.T) {
	f := loadConformanceRepo(t)
	ctx := context.Background()
	rng := rand.New(rand.NewSource(9))
	type window struct{ since, until int64 }
	var windows []window
	min0, max0 := f.all[0].Ct, f.all[0].Ct
	for _, c := range f.all {
		min0, max0 = min(min0, c.Ct), max(max0, c.Ct)
	}
	for i := 0; i < 50; i++ {
		a, b := min0+rng.Int63n(max0-min0), min0+rng.Int63n(max0-min0)
		if a > b {
			a, b = b, a
		}
		windows = append(windows, window{a, b})
	}
	// Edges: a single minute around real commits, open ends, whole range.
	for i := 0; i < 15; i++ {
		c := f.all[rng.Intn(len(f.all))]
		windows = append(windows, window{c.Ct, c.Ct}, window{c.Ct - 60, c.Ct}, window{c.Ct, c.Ct + 60}, window{0, c.Ct}, window{c.Ct, 0})
	}
	windows = append(windows, window{min0, max0}, window{max0 + 120, 0}, window{0, min0 - 120})
	for _, w := range windows {
		filter := CommitFilter{Limit: f.limit()}
		lo, hi := int64(-1<<62), int64(1<<62)
		if w.since != 0 {
			filter.Since = time.Unix(w.since, 0).UTC()
			lo = minuteFloor(w.since)
		}
		if w.until != 0 {
			filter.Until = time.Unix(w.until, 0).UTC()
			hi = minuteFloor(w.until) + 59
		}
		name := fmt.Sprintf("window %s..%s", fmtUnix(w.since), fmtUnix(w.until))
		if lo > hi {
			if _, err := f.session.SearchCommits(ctx, filter); err == nil {
				t.Errorf("%s: an inverted window must be refused", name)
			}
			continue
		}
		want := f.filter(func(c *oracleCommit) bool { return c.Ct >= lo && c.Ct <= hi })
		got, err := f.session.SearchCommits(ctx, filter)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(want) == 0 && len(got) == 0 {
			continue
		}
		sameSequence(t, name, hashesOf(got), want)
	}
}

func fmtUnix(u int64) string {
	if u == 0 {
		return "open"
	}
	return time.Unix(u, 0).UTC().Format("2006-01-02T15:04:05")
}

func TestConformanceBranchFilterMatchesRevList(t *testing.T) {
	f := loadConformanceRepo(t)
	ctx := context.Background()
	refs := []string{"master", "HEAD", "origin/master", "refs/heads/master", "refs/remotes/origin/master"}
	rng := rand.New(rand.NewSource(13))
	branches := strings.Fields(gitIn(t, f.root, "for-each-ref", "--format=%(refname:short)", "refs/remotes"))
	tags := strings.Fields(gitIn(t, f.root, "for-each-ref", "--format=%(refname:short)", "refs/tags"))
	for i := 0; i < 15 && len(branches) > 0; i++ {
		refs = append(refs, branches[rng.Intn(len(branches))])
	}
	for i := 0; i < 15 && len(tags) > 0; i++ {
		refs = append(refs, tags[rng.Intn(len(tags))])
	}
	for _, ref := range refs {
		if ref == "origin" || ref == "origin/HEAD" {
			continue
		}
		want := f.revList(t, ref)
		got, err := f.session.SearchCommits(ctx, CommitFilter{Branch: ref, Limit: f.limit()})
		if err != nil {
			t.Errorf("branch %q: %v", ref, err)
			continue
		}
		sameSequence(t, fmt.Sprintf("branch %q", ref), hashesOf(got), want)
		some, err := f.session.BranchCommits(ctx, ref, 300)
		if err != nil {
			t.Errorf("BranchCommits %q: %v", ref, err)
			continue
		}
		sameSequence(t, fmt.Sprintf("BranchCommits %q (300)", ref), hashesOf(some), want[:min(300, len(want))])
	}
	if _, err := f.session.SearchCommits(ctx, CommitFilter{Branch: "no-such-branch-zz", Limit: 10}); err == nil {
		t.Error("an unknown branch must be an error, not an empty history")
	}
	if _, err := f.session.SearchCommits(ctx, CommitFilter{Branch: "--all", Limit: 10}); err == nil {
		t.Error("a branch beginning with a dash must be refused")
	}
}

// Every filter at once, over many random combinations.
func TestConformanceCombinedFiltersRandom(t *testing.T) {
	f := loadConformanceRepo(t)
	ctx := context.Background()
	rng := rand.New(rand.NewSource(21))
	words := []string{"fix", "add", "test", "docs", "completion", "Merge", "revert", "bind", "prompt", "string", "history", "cmake", "Rust", "port", "typo", "release", "fish"}
	authors, _ := f.session.Authors(ctx)
	branches := []string{"", "", "master", "origin/master", "1.16.0"}
	orderCache := map[string][]string{}
	order := func(branch string) []string {
		if _, ok := orderCache[branch]; !ok {
			if branch == "" {
				orderCache[branch] = f.filter(func(*oracleCommit) bool { return true })
			} else {
				orderCache[branch] = f.revList(t, branch)
			}
		}
		return orderCache[branch]
	}
	min0, max0 := f.all[len(f.all)-1].Ct, f.all[0].Ct
	for i := 0; i < 120; i++ {
		filter := CommitFilter{Limit: f.limit(), Branch: branches[rng.Intn(len(branches))]}
		if rng.Intn(3) > 0 {
			filter.Query = words[rng.Intn(len(words))]
		}
		if rng.Intn(3) == 0 {
			a := authors[rng.Intn(len(authors))]
			if runes := []rune(a); rng.Intn(2) == 0 && len(runes) > 3 {
				a = string(runes[:len(runes)/2+1]) // a prefix, cut between characters
			}
			filter.Author = a
		}
		lo, hi := int64(-1<<62), int64(1<<62)
		if rng.Intn(2) == 0 {
			a, b := min0+rng.Int63n(max0-min0), min0+rng.Int63n(max0-min0)
			if a > b {
				a, b = b, a
			}
			filter.Since, filter.Until = time.Unix(a, 0), time.Unix(b, 0)
			lo, hi = minuteFloor(a), minuteFloor(b)+59
		}
		q, a := strings.TrimSpace(filter.Query), strings.TrimSpace(filter.Author)
		var want []string
		for _, h := range order(filter.Branch) {
			c := f.byHash[h]
			if c == nil {
				continue
			}
			if (q == "" || messageHas(c, q)) && (a == "" || authorHas(c, a)) && c.Ct >= lo && c.Ct <= hi {
				want = append(want, h)
			}
		}
		got, err := f.session.SearchCommits(ctx, filter)
		if err != nil {
			t.Errorf("filter %+v: %v", filter, err)
			continue
		}
		if len(want) == 0 && len(got) == 0 {
			continue
		}
		sameSequence(t, fmt.Sprintf("combined #%d branch=%q query=%q author=%q since=%s until=%s", i, filter.Branch, q, a,
			fmtUnix(filter.Since.Unix()*boolInt(!filter.Since.IsZero())), fmtUnix(filter.Until.Unix()*boolInt(!filter.Until.IsZero()))),
			hashesOf(got), want)
	}
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// The progressive variant publishes a first page then completes it: both must
// describe the same commits and the final one must carry line counts.
func TestConformanceProgressiveSearchPublishesThenCompletes(t *testing.T) {
	f := loadConformanceRepo(t)
	ctx := context.Background()
	var published []Commit
	progressive, ok := f.session.(interface {
		SearchCommitsProgressively(context.Context, CommitFilter, func([]Commit)) ([]Commit, error)
	})
	if !ok {
		t.Fatal("session has no progressive search")
	}
	final, err := progressive.SearchCommitsProgressively(ctx, CommitFilter{Query: "fix", Limit: 400},
		func(page []Commit) { published = append([]Commit(nil), page...) })
	if err != nil {
		t.Fatal(err)
	}
	plain, err := f.session.SearchCommits(ctx, CommitFilter{Query: "fix", Limit: 400})
	if err != nil {
		t.Fatal(err)
	}
	sameSequence(t, "progressive final vs plain", hashesOf(final), hashesOf(plain))
	sameSequence(t, "progressive published vs final", hashesOf(published), hashesOf(final))
	for _, c := range published {
		if !c.StatsPending {
			t.Fatalf("published commit %s is not marked pending", c.Hash[:10])
		}
	}
	for i, c := range final {
		if c.StatsPending {
			t.Fatalf("final commit %s still pending", c.Hash[:10])
		}
		if c.LinesAdded != plain[i].LinesAdded || c.LinesDeleted != plain[i].LinesDeleted {
			t.Fatalf("counts differ for %s", c.Hash[:10])
		}
	}
}

// ---------------------------------------------------------------------------
// Overview / period history (committer-time ordering).

type historyOrderSession interface {
	CommitHistoryHead(context.Context) (branch, revision string, err error)
	CommitHistoryOrder(context.Context, string, time.Time, time.Time, CommitHistorySearch) ([]string, error)
	CommitHistoryPage(context.Context, []string) ([]Commit, error)
}

func TestConformanceCommitHistoryOrderAndPages(t *testing.T) {
	f := loadConformanceRepo(t)
	ctx := context.Background()
	hs, ok := f.session.(historyOrderSession)
	if !ok {
		t.Fatal("session does not provide the period history")
	}
	branch, revision, err := hs.CommitHistoryHead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(gitIn(t, f.root, "rev-parse", "HEAD"))
	if branch != "master" || revision != head {
		t.Fatalf("head = %q %q, want master %s", branch, revision, head)
	}
	reach := f.revList(t, head)
	reachSet := map[string]bool{}
	for _, h := range reach {
		reachSet[h] = true
	}
	expect := func(keep func(*oracleCommit) bool) []string {
		var out []*oracleCommit
		for _, h := range reach {
			if c := f.byHash[h]; c != nil && keep(c) {
				out = append(out, c)
			}
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Ct > out[j].Ct })
		hs := make([]string, len(out))
		for i, c := range out {
			hs[i] = c.Hash
		}
		return hs
	}
	rng := rand.New(rand.NewSource(17))
	type tc struct {
		name     string
		from, to time.Time
		search   CommitHistorySearch
		keep     func(*oracleCommit) bool
	}
	cases := []tc{{name: "everything", keep: func(*oracleCommit) bool { return true }}}
	for _, w := range []string{"fix", "FIX", "Merge", "completion", "[", ".", "%", "nonexistentzzqq"} {
		w := w
		cases = append(cases, tc{name: "term " + w, search: CommitHistorySearch{Terms: []string{w}}, keep: func(c *oracleCommit) bool { return messageHas(c, w) }})
	}
	cases = append(cases,
		tc{name: "two terms (all must match)", search: CommitHistorySearch{Terms: []string{"fix", "test"}},
			keep: func(c *oracleCommit) bool { return messageHas(c, "fix") && messageHas(c, "test") }},
		tc{name: "author", search: CommitHistorySearch{Author: "zanchey"}, keep: func(c *oracleCommit) bool { return authorHas(c, "zanchey") }},
		tc{name: "term+author", search: CommitHistorySearch{Terms: []string{"fix"}, Author: "zanchey"},
			keep: func(c *oracleCommit) bool { return messageHas(c, "fix") && authorHas(c, "zanchey") }},
	)
	for i := 0; i < 25; i++ {
		a, b := f.all[rng.Intn(len(f.all))].Ct, f.all[rng.Intn(len(f.all))].Ct
		if a > b {
			a, b = b, a
		}
		if i%5 == 0 { // one-second window on a real commit time: inclusive both ends
			b = a
		}
		from, to := time.Unix(a, 0), time.Unix(b, 0)
		lo, hi := a, b
		cases = append(cases, tc{name: fmt.Sprintf("period %s..%s", fmtUnix(a), fmtUnix(b)), from: from, to: to,
			keep: func(c *oracleCommit) bool { return c.Ct >= lo && c.Ct <= hi }})
	}
	for _, tt := range cases {
		order, err := hs.CommitHistoryOrder(ctx, revision, tt.from, tt.to, tt.search)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		want := expect(tt.keep)
		if len(want) == 0 && len(order) == 0 {
			continue
		}
		if !sameSequence(t, tt.name, order, want) {
			continue
		}
		// Pages come back in the requested order with the committer time.
		for start := 0; start < len(order) && start < 600; start += 97 {
			chunk := order[start:min(start+97, len(order))]
			page, err := hs.CommitHistoryPage(ctx, chunk)
			if err != nil {
				t.Errorf("%s page %d: %v", tt.name, start, err)
				break
			}
			if !sameSequence(t, fmt.Sprintf("%s page %d", tt.name, start), hashesOf(page), chunk) {
				break
			}
			for _, c := range page {
				if o := f.byHash[c.Hash]; o != nil && (c.UnixTimestamp != o.Ct || c.Subject != o.Subject) {
					t.Errorf("%s: page commit %s has time %d subject %q, want %d %q", tt.name, c.Hash[:10], c.UnixTimestamp, c.Subject, o.Ct, o.Subject)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// File history

type conformancePaths struct {
	name  string
	paths []string
}

func (f *conformanceRepo) interestingPaths(t *testing.T) []conformancePaths {
	t.Helper()
	files := strings.Split(strings.TrimRight(gitIn(t, f.root, "ls-files", "-z"), "\x00"), "\x00")
	counts := map[string]int{}
	for _, line := range strings.Split(gitIn(t, f.root, "log", "--name-only", "--format=", "HEAD"), "\n") {
		if line != "" {
			counts[line]++
		}
	}
	existing := map[string]bool{}
	for _, p := range files {
		existing[p] = true
	}
	var top []string
	for p := range counts {
		if existing[p] {
			top = append(top, p)
		}
	}
	sort.Slice(top, func(i, j int) bool {
		if counts[top[i]] != counts[top[j]] {
			return counts[top[i]] > counts[top[j]]
		}
		return top[i] < top[j]
	})
	var renamedNew, renamedOld, deleted []string
	for _, line := range strings.Split(gitIn(t, f.root, "log", "--diff-filter=R", "-M", "--name-status", "--format=", "HEAD"), "\n") {
		p := strings.Split(line, "\t")
		if len(p) == 3 {
			renamedOld = append(renamedOld, p[1])
			renamedNew = append(renamedNew, p[2])
		}
	}
	for _, line := range strings.Split(gitIn(t, f.root, "log", "--diff-filter=D", "--name-only", "--format=", "HEAD"), "\n") {
		if line != "" && !existing[line] {
			deleted = append(deleted, line)
		}
	}
	var odd []string
	for _, p := range files {
		if strings.ContainsAny(p, " []*?\\'\"#%&;()$!~{}") || strings.IndexFunc(p, func(r rune) bool { return r > 127 }) >= 0 {
			odd = append(odd, p)
		}
	}
	rng := rand.New(rand.NewSource(31))
	var random []string
	for i := 0; i < 25; i++ {
		random = append(random, files[rng.Intn(len(files))])
	}
	pick := func(s []string, n int) []string {
		seen := map[string]bool{}
		var out []string
		for _, p := range s {
			if !seen[p] && len(out) < n {
				seen[p] = true
				out = append(out, p)
			}
		}
		return out
	}
	return []conformancePaths{
		{"most changed", pick(top, 12)},
		{"renamed (new path)", pick(renamedNew, 12)},
		{"renamed (old path)", pick(renamedOld, 12)},
		{"deleted", pick(deleted, 12)},
		{"odd names", pick(odd, 15)},
		{"random", random},
		{"missing", []string{"no/such/file.txt", "src/does-not-exist.rs"}},
	}
}
