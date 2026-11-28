package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/FleetingEcho/lazygitGo/internal/repository"
)

// A whole commit without the mouse: stage from the list, open the commit form,
// type the message and commit.
func TestKeyboardOnlyStageAndCommit(t *testing.T) {
	h, calls := changesTestApp(t, []repository.File{
		{Path: "a.txt", Status: " M", Tracked: true, Unstaged: true},
		{Path: "b.txt", Status: "M ", Tracked: true, Staged: true},
	})
	h.press(t, ui.KeySpace)
	h.waitFor(t, "stage", func() bool { return calls.hasPrefix("stage:") || calls.hasPrefix("unstage:") })
	h.press(t, ui.KeyC)
	if !h.app.showCommitForm {
		t.Fatalf("c did not open the commit form: %v", h.texts())
	}
	h.tester.Type("Keyboard commit")
	h.tester.Frame()
	h.tester.Key(ui.Ctrl, ui.KeyEnter)
	h.tester.Frame()
	h.settle(t)
	h.waitFor(t, "commit", func() bool { return calls.has("commit:Keyboard commit") })
}

// The number keys reach every visible section.
func TestKeyboardOnlyDigitsReachEverySection(t *testing.T) {
	h := menuTestApp(t)
	seen := map[int]bool{}
	reachable := min(len(sectionKeys), len(h.app.visibleSections()))
	for i := 0; i < reachable; i++ {
		h.press(t, sectionKeys[i])
		seen[h.app.workspaceTab] = true
	}
	if len(seen) != reachable {
		t.Fatalf("digits reached %d of %d sections", len(seen), reachable)
	}
	t.Logf("%d sections show; the digits reach %d", len(h.app.visibleSections()), reachable)
}

// The command palette runs a catalog command from the keyboard alone.
func TestKeyboardOnlyPaletteRunsACommand(t *testing.T) {
	h, calls := changesTestApp(t, []repository.File{{Path: "a.txt", Status: " M", Tracked: true, Unstaged: true}})
	h.tester.Frame()
	h.tester.Key(ui.Cmd|ui.Shift, ui.KeyP)
	h.tester.Frame()
	h.tester.Type("stage")
	h.tester.Frame()
	h.tester.Key(0, ui.KeyEnter)
	h.tester.Frame()
	h.settle(t)
	if len(calls.all()) == 0 {
		t.Fatalf("the palette ran nothing: %v", h.texts())
	}
}

// A remapped key runs its action and the old key no longer does.
func TestKeyboardOnlyRemappedKeyRunsAndOldKeyDoesNot(t *testing.T) {
	h, calls := changesTestApp(t, []repository.File{{Path: "a.txt", Status: " M", Tracked: true, Unstaged: true}})
	h.app.settings.KeyBindings = map[string][]string{"git/Changes/Stage or unstage all changes": {"x"}}
	h.tester.Frame()
	h.press(t, ui.KeyA)
	if calls.has("stage-all") {
		t.Fatal("the old key still stages everything")
	}
	h.press(t, ui.KeyX)
	h.waitFor(t, "stage all", func() bool { return calls.has("stage-all") })
}

// Sections beyond the ten digits are reached with the next / previous keys.
func TestKeyboardOnlyNextSectionReachesTheLastSection(t *testing.T) {
	h := menuTestApp(t)
	visible := h.app.visibleSections()
	h.press(t, sectionKeys[0])
	for range visible {
		h.tester.Frame()
		h.tester.Key(ui.Alt, ui.KeyPageDown)
		h.tester.Frame()
		h.settle(t)
		if h.app.workspaceTab == visible[len(visible)-1] {
			return
		}
	}
	t.Fatal("Alt+PageDown never reached the last section")
}

// Every control of the Home page can be reached by Tab alone.
func TestKeyboardOnlyHomePageTabReachesEveryAction(t *testing.T) {
	a := newApp(fakeOpener{})
	a.settings = defaultSettings()
	a.recentRepositories = []recentRepository{{Path: "/w/project", LastOpened: timeNow()}}
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	want := []string{"Browse folder", "Clone repository", "Open SSH", "Keyboard shortcuts"}
	reached := map[string]bool{}
	for i := 0; i < 80; i++ {
		for _, label := range want {
			if tt.Focused(label) {
				reached[label] = true
			}
		}
		tt.Key(0, ui.KeyTab)
		tt.Frame()
	}
	for _, label := range want {
		if !reached[label] {
			t.Errorf("Tab never focuses %q", label)
		}
	}
}

// A context-menu item shows and answers to the key the user gave its action.
func TestKeyboardOnlyMenuItemFollowsRemappedKey(t *testing.T) {
	a := newApp(fakeOpener{})
	a.settings = defaultSettings()
	a.settings.KeyBindings = map[string][]string{
		"git/Changes/Discard the chosen file's changes": {"x"},
		"git/Changes/Blame the chosen file":             {},
	}
	if mods, key := a.menuKey("Changes", 0, ui.KeyD); mods != 0 || key != ui.KeyX {
		t.Fatalf("Discard follows d to %v %v, want x", mods, key)
	}
	if _, key := a.menuKey("Changes", 0, ui.KeyB); key != ui.KeyUnknown {
		t.Fatalf("a cleared action still has key %v", key)
	}
	if mods, key := a.menuKey("Changes", 0, ui.KeyQ); mods != 0 || key != ui.KeyQ {
		t.Fatal("a menu item with no customizable action must keep its key")
	}
}

// The collapse buttons have keys: Alt+B for the list pane, Alt+N for the sidebar.
func TestKeyboardOnlyCollapseKeys(t *testing.T) {
	h := menuTestApp(t)
	h.tester.Frame()
	for _, tc := range []struct {
		key  ui.Key
		flag func() bool
	}{
		{ui.KeyB, func() bool { return h.app.filePaneCollapsed }},
		{ui.KeyN, func() bool { return h.app.navPaneCollapsed }},
	} {
		h.tester.Key(ui.Alt, tc.key)
		h.tester.Frame()
		if !tc.flag() {
			t.Fatalf("Alt+%v did not collapse", tc.key)
		}
		h.tester.Key(ui.Alt, tc.key)
		h.tester.Frame()
		if tc.flag() {
			t.Fatalf("Alt+%v did not expand again", tc.key)
		}
	}
}

// Digits name the section they switch to, and a click that activates a row is
// not recorded as an Enter key press.
func TestKeyboardOnlyShortcutRecordsAreSpecific(t *testing.T) {
	h := menuTestApp(t)
	h.app.attachRepositoryCommandLog(repository.NewCommandLog())
	h.press(t, ui.Key2)
	h.app.lastInputPointer = true
	h.app.invokeShortcut(shortcut("list/x/activate", "Enter", "Open the chosen item in Changes"), nil)
	var labels []string
	for _, entry := range h.app.commandLogEntries() {
		if entry.Shortcut != nil {
			labels = append(labels, entry.Shortcut.Chord+" "+entry.Shortcut.Label)
		}
	}
	if len(labels) != 1 || labels[0] != "2 Switch to "+workspaceSections[h.app.workspaceTab] {
		t.Fatalf("records = %q", labels)
	}
}

// Right takes the focus from the sidebar into the page; Left brings it back.
func TestKeyboardOnlySidebarRightEntersPageAndLeftReturns(t *testing.T) {
	for _, tab := range []int{sectionOverview, sectionChanges} {
		h := menuTestApp(t)
		h.app.fileTreeMode = false
		h.app.setWorkspaceTab(tab)
		h.tester.Frame()
		h.app.keyboardFocus = "sidebar"
		h.tester.Frame()
		h.tester.Frame()
		if !h.tester.Focused("Workspace") {
			t.Fatalf("tab %d: the sidebar did not take the focus: %v", tab, h.texts())
		}
		h.tester.Key(0, ui.KeyRight)
		h.tester.Frame()
		h.tester.Frame()
		if h.tester.Focused("Workspace") {
			t.Fatalf("tab %d: Right left the focus in the sidebar", tab)
		}
		h.tester.Key(0, ui.KeyLeft)
		h.tester.Frame()
		h.tester.Frame()
		if !h.tester.Focused("Workspace") {
			t.Fatalf("tab %d: Left did not return to the sidebar", tab)
		}
	}
}

// A conflict's Result editor shows by default when a merge is open, but it
// owns the letters only while it has the focus; the digits must still switch
// sections otherwise.
func TestKeyboardOnlyDigitsWorkWhileConflictResultShows(t *testing.T) {
	h := menuTestApp(t)
	h.app.conflictState = &conflictState{}
	h.app.conflictStageTab = 3
	h.tester.Frame()
	before := h.app.workspaceTab
	h.press(t, sectionKeys[0])
	if h.app.workspaceTab == before {
		t.Fatal("the digit did nothing while the conflict Result editor was showing")
	}
}

// Right enters the Changes page even when it has no file to list, and Left
// comes back out of it.
func TestKeyboardOnlyRightEntersAnEmptyChangesPage(t *testing.T) {
	for name, files := range map[string][]repository.File{
		"no files":  nil,
		"conflict":  {{Path: "c.txt", Status: "UU", HasMergeConflicts: true, Tracked: true}},
		"one files": {{Path: "a.txt", Status: " M", Tracked: true, Unstaged: true}},
	} {
		h, _ := changesTestApp(t, files)
		h.app.fileTreeMode = false
		h.tester.Frame()
		h.app.keyboardFocus = "sidebar"
		h.tester.Frame()
		h.tester.Frame()
		h.tester.Key(0, ui.KeyRight)
		h.tester.Frame()
		h.tester.Frame()
		if h.tester.Focused("Workspace") {
			t.Errorf("%s: Right stayed in the sidebar", name)
		}
		h.tester.Key(0, ui.KeyLeft)
		h.tester.Frame()
		h.tester.Frame()
		if !h.tester.Focused("Workspace") {
			t.Errorf("%s: Left did not return to the sidebar", name)
		}
	}
}

// The shortcut list can be searched by pressing the keys, not only by words.
func TestKeyboardOnlyFindShortcutByPressingIt(t *testing.T) {
	a := newApp(fakeOpener{})
	a.settings = defaultSettings()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	a.openKeybindings()
	tt.Frame()
	if err := tt.Click("Find a shortcut by pressing it"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(ui.Ctrl, ui.KeyEnter)
	tt.Frame()
	if !tt.HasText("Commit the staged changes") {
		t.Fatalf("Ctrl+Enter did not find its action: %v", tt.Texts())
	}
	if tt.HasText("Check out the chosen branch") {
		t.Fatal("the search kept an action that does not use the pressed keys")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if !a.keybindingsOpen || a.keyEdit.searchByKey {
		t.Fatal("Escape should stop the key search and leave the list open")
	}
}
