package repository

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildConformanceRepository writes a deterministic repository that has every
// shape the History, File History and filter code has to get right, so the
// conformance tests run anywhere and with every full test run:
//
//   - a few thousand commits with merge commits, side branches, annotated and
//     lightweight tags, remote-tracking branches
//   - committer clocks that run behind their parents (so a plain --since would
//     lose commits), and author times that differ from committer times
//   - authors whose names hold regular-expression characters ("[Redacted]",
//     "J. Doe", "a+b (c)"), non-ASCII names and noreply emails
//   - messages with brackets, percent signs, backslashes, quotes, non-ASCII,
//     issue references, multi-line bodies, and words that are also hexadecimal
//     ("dead", "feed", "face", "1234") and so valid hash prefixes
//   - files that are edited often, renamed, deleted, and named with spaces,
//     brackets, '#', '%', '&' and non-ASCII letters
//
// It is generated with git fast-import, so it takes about a second.
func buildConformanceRepository(t testing.TB, root string, commits int) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
		}
		return string(out)
	}
	git("init", "-q", "--initial-branch=master")
	git("config", "user.email", "conformance@example.com")
	git("config", "user.name", "Conformance")

	rng := rand.New(rand.NewSource(20260101))
	type person struct{ name, email string }
	people := []person{
		{"zanchey", "zanchey@example.org"}, {"[Redacted]", "redacted@example.org"}, {"J. Doe", "jdoe@example.org"},
		{"a+b (c)", "ab@example.org"}, {"Zoë Müller", "zoe@example.org"}, {"Renovate Bot", "29139614+renovate[bot]@users.noreply.github.com"},
		{"Ada", "ada@users.noreply.github.com"}, {"Grace", "grace@example.com"}, {"Linus", "linus@example.com"},
		{"D. Knuth", "knuth@example.edu"}, {"star*name", "star@example.com"}, {"back\\slash", "slash@example.com"},
	}
	for i := 0; i < 14; i++ {
		people = append(people, person{fmt.Sprintf("Contributor %02d", i), fmt.Sprintf("c%02d@example.com", i)})
	}
	words := []string{
		"fix", "Fix", "FIX", "add", "test", "docs", "completion", "revert", "bind", "prompt", "string", "history", "cmake", "port", "typo",
		"release", "fish", "dead code", "feed the parser", "face lift", "decade", "deface", "cafe", "added", "1234 widgets", "0000",
		"[WIP]", "(#%d)", "100% done", "use C:\\path", "say \"hi\"", "it's", "naïve café", "日本語", "→ arrow", "a.b.c", "x*y", "^start", "end$", "a|b",
	}
	files := []string{"README.rst", "CHANGELOG.rst", "Cargo.toml"}
	for i := 0; i < 30; i++ {
		files = append(files, fmt.Sprintf("src/mod%02d.rs", i))
	}
	files = append(files, "share/completions/git.fish", "share/functions/fish_prompt.fish", "doc/guide.rst",
		"odd name/with space.txt", "odd name/brackets [1].txt", "odd name/#hash & 100%.txt", "odd name/ünï code.txt")
	content := map[string]int{}

	var stream strings.Builder
	write := func(format string, args ...any) { fmt.Fprintf(&stream, format, args...) }
	data := func(s string) { write("data %d\n%s\n", len(s), s) }

	const baseTime = int64(1_400_000_000)
	marks := []int{} // mark of every commit made on master, in order
	mark := 0
	branchTip := map[string]int{}
	currentTime := baseTime
	renamed := false
	pickMessage := func(n int) string {
		w := words[rng.Intn(len(words))]
		if strings.Contains(w, "%d") {
			w = fmt.Sprintf(w, 1000+n)
		}
		msg := fmt.Sprintf("%s %s in %s", []string{"Fix", "Add", "Update", "Refactor", "Remove"}[rng.Intn(5)], w, files[rng.Intn(len(files))])
		if rng.Intn(5) == 0 {
			msg += fmt.Sprintf("\n\nSecond paragraph %d mentions %s and closes #%d.", n, words[rng.Intn(len(words))], rng.Intn(9000))
		}
		return msg
	}
	emit := func(ref string, from, merge int, msg string, ops func()) int {
		mark++
		p := people[rng.Intn(len(people))]
		currentTime += int64(600 + rng.Intn(36000))
		committer := currentTime
		if rng.Intn(25) == 0 {
			committer -= int64(86400 * (2 + rng.Intn(40))) // a clock that runs behind
		}
		author := committer
		if rng.Intn(6) == 0 {
			author -= int64(3600 * (1 + rng.Intn(300))) // a rebased or cherry-picked commit
		}
		c := people[rng.Intn(len(people))]
		write("commit %s\nmark :%d\nauthor %s <%s> %d +0000\ncommitter %s <%s> %d +0000\n", ref, mark, p.name, p.email, author, c.name, c.email, committer)
		data(msg)
		if from > 0 {
			write("from :%d\n", from)
		}
		if merge > 0 {
			write("merge :%d\n", merge)
		}
		ops()
		write("\n")
		return mark
	}
	touch := func(n int, count int) func() {
		return func() {
			for i := 0; i < count; i++ {
				name := files[rng.Intn(len(files))]
				if renamed && name == "src/mod00.rs" {
					name = "src/renamed00.rs"
				}
				content[name]++
				write("M 100644 inline %s\n", name)
				data(fmt.Sprintf("%s revision %d by commit %d\nline two\nline three %d\n", name, content[name], n, rng.Intn(1000)))
			}
		}
	}
	masterTip := 0
	for n := 1; n <= commits; n++ {
		switch {
		case n == 1:
			masterTip = emit("refs/heads/master", 0, 0, "Initial import", func() {
				for _, name := range files {
					content[name]++
					write("M 100644 inline %s\n", name)
					data(fmt.Sprintf("%s initial\nbody\n", name))
				}
			})
		case n == commits/3: // a rename and a deletion
			masterTip = emit("refs/heads/master", masterTip, 0, "Rename mod00 and drop mod01", func() {
				write("R src/mod00.rs src/renamed00.rs\n")
				write("D src/mod01.rs\n")
				renamed = true
			})
		case n%97 == 0: // a side branch of a few commits, merged back
			branch := fmt.Sprintf("refs/heads/topic%d", n)
			tip := masterTip
			for k := 0; k < 3+rng.Intn(3); k++ {
				tip = emit(branch, tip, 0, pickMessage(n*10+k), touch(n, 1+rng.Intn(2)))
			}
			branchTip[branch] = tip
			masterTip = emit("refs/heads/master", masterTip, tip, fmt.Sprintf("Merge pull request #%d from contributor/topic%d", n, n), touch(n, rng.Intn(2)))
		default:
			masterTip = emit("refs/heads/master", masterTip, 0, pickMessage(n), touch(n, 1+rng.Intn(3)))
		}
		marks = append(marks, masterTip)
	}
	cmd := exec.Command("git", "-C", root, "fast-import", "--quiet", "--export-marks="+filepath.Join(root, ".git", "marks"))
	cmd.Stdin = strings.NewReader(stream.String())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git fast-import: %v\n%s", err, stderr.String())
	}
	git("reset", "-q", "--hard", "master")

	// Tags (lightweight and annotated) and remote-tracking branches.
	revs := strings.Fields(git("rev-list", "--reverse", "master"))
	at := func(fraction float64) string { return revs[int(float64(len(revs)-1)*fraction)] }
	git("tag", "1.16.0", at(0.10))
	git("tag", "-a", "-m", "release 2.0", "2.0.0", at(0.30))
	git("tag", "3.0.0", at(0.60))
	git("tag", "-a", "-m", "release 4.0", "4.0.0", at(0.90))
	git("update-ref", "refs/remotes/origin/master", at(0.95))
	git("update-ref", "refs/remotes/origin/Integration_3.0.0", at(0.55))
	git("update-ref", "refs/remotes/origin/topic", at(0.40))
	git("config", "branch.master.remote", "origin")
	git("config", "branch.master.merge", "refs/heads/master")
}
