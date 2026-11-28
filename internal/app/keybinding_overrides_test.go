package app

import "testing"

func TestKeyCommandRefsHaveUniqueIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, ref := range keyCommandRefs() {
		if seen[ref.id] {
			t.Errorf("duplicate key command id %q", ref.id)
		}
		seen[ref.id] = true
	}
}

func TestKeyOverridesResolveAndRefuseConflicts(t *testing.T) {
	const pull = "git/Everywhere/Pull from the upstream branch"
	const stash = "git/Changes/Stash changes"
	const drop = "git/Stash/Drop the chosen stash"

	if res := resolveKeyOverrides(map[string][]string{pull: {"ctrl+shift+u"}}); res.issue != nil {
		t.Fatalf("a free key was refused: %v", res.issue)
	} else if got := res.keys(mustKeyRef(t, pull)); got != "Ctrl+Shift+U" {
		t.Fatalf("pull shows %q", got)
	}
	// Same scope as another action.
	if res := resolveKeyOverrides(map[string][]string{stash: {"d"}}); res.issue == nil {
		t.Fatal("Stash changes on d should clash with Discard in Changes")
	}
	// Different scopes may share a key.
	if res := resolveKeyOverrides(map[string][]string{drop: {"s"}}); res.issue != nil {
		t.Fatalf("Stash and Changes do not overlap: %v", res.issue)
	}
	// An Everywhere action meets every scope.
	if res := resolveKeyOverrides(map[string][]string{pull: {"d"}}); res.issue == nil {
		t.Fatal("a global key must not reuse a page's key")
	}
	// Keys the window keeps for itself.
	if res := resolveKeyOverrides(map[string][]string{stash: {"f5"}}); res.issue == nil {
		t.Fatal("F5 refreshes the repository")
	}
	if res := resolveKeyOverrides(map[string][]string{stash: {"escape"}}); res.issue == nil {
		t.Fatal("Escape is not assignable")
	}
	if res := resolveKeyOverrides(map[string][]string{stash: {"x", "y"}}); res.issue == nil {
		t.Fatal("only one key")
	}
	// An action may be left without a key.
	res := resolveKeyOverrides(map[string][]string{stash: {}})
	if res.issue != nil || len(res.byID[stash]) != 0 {
		t.Fatalf("an unassigned action: %v %v", res.issue, res.byID[stash])
	}
	// A bad entry falls back to the defaults.
	bad := resolveKeyOverrides(map[string][]string{stash: {"d"}})
	if len(bad.byID[stash]) != 1 || bad.byID[stash][0] != mustKeyRef(t, stash).def {
		t.Fatal("a refused set must leave the defaults in force")
	}
}

func mustKeyRef(t *testing.T, id string) keyCommandRef {
	t.Helper()
	ref, ok := keyCommandRefByID(id)
	if !ok {
		t.Fatalf("no key command %q", id)
	}
	return ref
}
