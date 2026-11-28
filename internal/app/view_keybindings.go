package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// keyBinding is one line of the shortcut reference: the keys to press and
// what they do. keys is display text, so a combination and a pair of
// alternatives both fit in the single key chip beside the description.
type keyBinding struct {
	keys  string
	label string
	// id names a customizable action (see keyCommandRefs); it is empty for
	// the keys the window keeps for itself.
	id string
}

// keyBindingGroup keeps the reference readable by naming the part of the
// window each shortcut acts on: the same letter does different things in
// different sections.
type keyBindingGroup struct {
	title    string
	icon     *ui.SVG
	bindings []keyBinding
}

// keyBindingGroups is the shortcut reference in the order the app presents
// its parts: the gestures that work everywhere, the window-wide navigation,
// then each workspace section, and finally the forms.
func keyBindingGroups(customCommands ...customCommandSetting) []keyBindingGroup {
	return keyBindingGroupsFor(defaultKeyResolution(), customCommands)
}

// keyBindingGroupsFor is keyBindingGroups with the keys the user chose.
func keyBindingGroupsFor(keys keyResolution, customCommands []customCommandSetting) []keyBindingGroup {
	groups := []keyBindingGroup{
		{
			title: "Everywhere",
			icon:  keyboardIcon,
			bindings: []keyBinding{
				{keys: "Cmd/Ctrl+Shift+P", label: "Open Command Palette"},
				{keys: "Cmd/Ctrl+Shift+A", label: "Open Activity Center"},
				{keys: "Ctrl+` / Ctrl+~", label: "Toggle the terminal"},
				{keys: "Ctrl+F", label: "Search the active terminal"},
				{keys: "Tab / Shift+Tab", label: "Move between panels"},
				{keys: "j / k", label: "Move through a list"},
				{keys: "Shift+F10", label: "Open the selected item’s context menu"},
				{keys: "Escape", label: "Close a dialog or cancel the current step"},
			},
		},
		{
			title: "Tabs",
			icon:  newWindowIcon,
			bindings: []keyBinding{
				{keys: "Cmd/Ctrl+T", label: "Open a new tab or the picker"},
				{keys: "Cmd/Ctrl+W", label: "Close the active tab"},
				{keys: "Ctrl+Tab", label: "Switch to the next tab"},
				{keys: "Ctrl+Shift+Tab", label: "Switch to the previous tab"},
			},
		},
		{
			title: "Home",
			icon:  folderOpenIcon,
			bindings: []keyBinding{
				{keys: "C", label: "Clone a remote repository"},
				{keys: "Escape", label: "Cancel cloning or close the clone form"},
			},
		},
		{
			title: "Workspace",
			icon:  folderIcon,
			bindings: []keyBinding{
				{keys: "1 – 9", label: "Switch to a workspace section"},
				{keys: "0", label: "Switch to the last section"},
				{keys: "Up / Down", label: "Move through a list"},
				{keys: "Home / End", label: "Jump to the first or last item"},
				{keys: "PageUp / PageDown", label: "Move a page at a time"},
				{keys: "Enter", label: "Open the chosen item"},
			},
		},
		{title: "Changes", icon: stageIcon, bindings: []keyBinding{
			{keys: "F7 / Shift+F7", label: "Move to the next / previous conflict"},
			{keys: "Alt+1 / Alt+2", label: "Keep the current / ancestor conflict hunk"},
			{keys: "Alt+3 / Alt+4", label: "Keep the incoming conflict hunk / both hunks"},
			{keys: "Alt+Z", label: "Undo the last conflict resolution"},
		}},
		{title: "History and graph", icon: historyIcon},
		{title: "Branches", icon: branchesIcon},
		{title: "Remotes", icon: remotesIcon},
		{title: "Stash", icon: stashIcon},
		{title: "Tags", icon: tagsIcon},
		{title: "Worktrees", icon: worktreesIcon},
		{title: "Agent tasks", icon: fileBracesIcon},
		{title: "Submodules", icon: submodulesIcon},

		{
			title: "Forms",
			icon:  commitIcon,
			bindings: []keyBinding{
				{keys: "Tab / Shift+Tab", label: "Move focus between controls"},
				{keys: "Enter", label: "Submit the form"},
				{keys: "Escape", label: "Cancel the form"},
			},
		},
	}
	// Operation keys come from the same definitions that dispatch them.
	replacements := map[string][]keyBinding{}
	refs := keyCommandRefs()
	for i, command := range keyboardCommands() {
		replacements[command.group] = append(replacements[command.group],
			keyBinding{keys: keys.keys(refs[i]), label: command.label, id: refs[i].id})
	}
	// Window-wide actions come from their own table, in the Everywhere group.
	for _, ref := range refs[keyRefsGit+len(diffKeyboardCommands()):] {
		replacements["Everywhere"] = append(replacements["Everywhere"],
			keyBinding{keys: keys.keys(ref), label: ref.label, id: ref.id})
	}
	for i := range groups {
		if bindings, ok := replacements[groups[i].title]; ok {
			if groups[i].title == "Everywhere" {
				groups[i].bindings = append(groups[i].bindings, bindings...)
			} else {
				if groups[i].title == "Changes" {
					bindings = append(bindings, groups[i].bindings...)
				}
				groups[i].bindings = bindings
			}
			delete(replacements, groups[i].title)
		}
	}
	// Diff selection has its own focused-pane scope. Current-page help changes
	// Space's label for Custom Patch and hides its commands elsewhere.
	diffBindings := make([]keyBinding, 0, len(diffKeyboardCommands())+2)
	for i, command := range diffKeyboardCommands() {
		ref := refs[keyRefsGit+i]
		diffBindings = append(diffBindings, keyBinding{keys: keys.keys(ref), label: command.label, id: ref.id})
	}
	diffBindings = append(diffBindings, keyBinding{keys: "A", label: "Add hunk"}, keyBinding{keys: "B", label: "Review Custom Patch"})
	// Remote branches have their own scope rather than acting on the selected remote.
	groups = append(groups,
		keyBindingGroup{title: "Remote branches", icon: remotesIcon, bindings: replacements["Remote branches"]},
		keyBindingGroup{title: "Historical files", icon: historyIcon, bindings: replacements["Historical files"]},
		keyBindingGroup{title: "Diff review", icon: selectLinesIcon, bindings: diffBindings},
	)
	if len(customCommands) > 0 {
		bindings := make([]keyBinding, 0, len(customCommands))
		for _, command := range customCommands {
			keys := customCommandDisplayKey(command.Key)
			if keys == "" {
				keys = "menu"
			}
			bindings = append(bindings, keyBinding{keys: keys, label: command.Name})
		}
		groups = append(groups, keyBindingGroup{title: "Custom commands", icon: commandIcon, bindings: bindings})
	}
	return groups

}

func (a *app) openKeybindings() {
	a.keybindingsCurrentOnly = false
	a.keybindingsOpen = true
}

func (a *app) openCurrentKeybindings() {
	a.keybindingFilter = ""
	a.keybindingsCurrentOnly = true
	a.keybindingsScope = ""
	if a.state == stateReady {
		a.keybindingsScope = a.keyboardCurrentGroup
	}
	a.keybindingsOpen = true
}

func (a *app) closeKeybindings() {
	a.stopKeyRecording()
	a.keyEdit.editing = false
	a.keyEdit.searchByKey, a.keyEdit.searchChord, a.keyEdit.searchLabel = false, "", ""
	a.keybindingsOpen = false
	a.keybindingsCurrentOnly = false
	a.keybindingFilter = ""
}

// keybindingsModal is the shortcut reference: the gestures the app answers
// to, grouped by where they act, a field to narrow them, and the switch that
// turns every shortcut off for users who would rather click.
func (a *app) keybindingsModal(c *ui.Context) {
	if !a.keybindingsOpen {
		return
	}
	theme := c.Theme()
	ui.Overlay(c, func() {
		back := ui.Row(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).
			Justify(ui.Center).AlignItems(ui.Center).
			Background(ui.RGBA(0, 0, 0, 0.35)).Modal()
		a.registerTerminalModalShortcuts(back)
		back.Children(func() {
			panel := ui.Column(c).Width(760).MaxWidthPercent(94).MaxHeightPercent(96).
				Padding(22).Gap(14).Background(theme.Background).
				Border(1, theme.Border).Radius(theme.Radius + 4).
				Role(ui.RoleDialog).Label(tr("Keyboard shortcuts")).
				Children(func() {
					a.keybindingsHeader(c)
					ui.Divider(c)
					a.keybindingsFilterField(c)
					ui.Divider(c)
					ui.Scroll(c).Grow(1).Children(func() {
						a.keybindingsBody(c)
					})
					ui.Divider(c)
					a.keybindingsFooter(c)
				})
			// Overlay commands, unlike the window registrations, keep working in
			// the modal's focus scope. This makes ? and F1 respond immediately
			// after the dialog's switch re-enables the keyboard.
			if !a.settings.DisableShortcuts && !c.FocusTakesText() && a.keyEdit.idle() {
				for _, b := range a.keysOf("global/shortcuts-page") {
					if a.onOverlayShortcut(back, b.mods, b.key, shortcut("keybindings/modal-current", boundKeyLabel(b), tr("Show shortcuts for the current page")), nil) {
						a.openCurrentKeybindings()
					}
				}
				for _, b := range a.keysOf("global/shortcuts-all") {
					if a.onOverlayShortcut(back, b.mods, b.key, shortcut("keybindings/modal-all", boundKeyLabel(b), tr("Open the complete shortcut list")), nil) {
						a.openKeybindings()
					}
				}
			}
			if (a.keyEdit.idle() && panel.PressedOutside()) || a.keyEdit.idle() && a.onOverlayShortcut(back, 0, ui.KeyEscape, shortcut("keybindings/modal-close", "Escape", tr("Close a dialog or cancel the current step")), nil) {
				a.closeKeybindings()
			}
		})
	})
}

func (a *app) keybindingsHeader(c *ui.Context) {
	theme := c.Theme()
	ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
		ui.Icon(c, keyboardIcon).FontSize(20).TextColor(theme.Accent)
		ui.Column(c).Gap(2).Grow(1).Children(func() {
			ui.Text(c, tr("Keyboard shortcuts")).FontSize(19).Bold()
			subtitle := tr("Every action has a key. Type below to find the one you want.")
			if a.keybindingsCurrentOnly && a.state == stateReady {
				subtitle = trf("Shortcuts for %s and the actions available everywhere.", tr(workspaceSections[a.workspaceTab]))
			}
			ui.Text(c, subtitle).TextColor(theme.TextMuted).FontSize(theme.FontSize - 1)
		})
		a.keyEditToggle(c)
		iconButton(c, "Close keyboard shortcuts", "Escape", closeIcon, false, a.closeKeybindings)
	})
}

func (a *app) keybindingsFilterField(c *ui.Context) {
	theme := c.Theme()
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Icon(c, searchIcon).FontSize(14).TextColor(theme.TextMuted)
		if a.keyEdit.searchByKey {
			a.keySearchRecorder(c)
		} else {
			ui.TextInput(c, &a.keybindingFilter).
				Label(tr("Filter shortcuts")).
				Placeholder(tr("Type to filter shortcuts")).
				Grow(1)
		}
		toggle := ui.ButtonBase(c).Padding(6).Radius(theme.Radius).
			Label(tr("Find a shortcut by pressing it")).Tooltip(tr("Find a shortcut by pressing it"))
		if a.keyEdit.searchByKey {
			toggle.Background(theme.Accent.Alpha(0.18))
		} else if toggle.Hovered() {
			toggle.Background(theme.SurfaceHover)
		}
		toggle.Children(func() { ui.Icon(c, keyboardIcon).FontSize(16).TextColor(theme.Accent) })
		if toggle.Clicked() {
			a.keyEdit.searchByKey = !a.keyEdit.searchByKey
			a.keyEdit.searchChord = ""
		}
	})
	if a.keyEdit.editing {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, infoIcon).FontSize(13).TextColor(theme.TextMuted)
			ui.Text(c, tr("Click a key to change it; the new key works at once. Backspace clears it, Escape cancels, and a key already in use is refused.")).
				TextColor(theme.TextMuted).FontSize(theme.FontSize - 1)
		})
	}
	issue := a.keyResolution().issue
	if issue != nil {
		ui.Text(c, trf("Some stored shortcuts were ignored: %s", issue.Error())).
			TextColor(theme.Warning).FontSize(theme.FontSize - 1)
	}
}

// visibleKeyBindingGroups keeps ? focused on the current page. Navigation and
// global commands remain visible because they work alongside its row actions;
// F1 bypasses this filter and still lists every section.
func (a *app) visibleKeyBindingGroups() []keyBindingGroup {
	groups := append(keyBindingGroupsFor(a.keyResolution(), a.settings.CustomCommands), a.editorShortcutHelpGroup())
	if !a.keybindingsCurrentOnly {
		return groups
	}
	active := ""
	if a.state == stateReady {
		switch a.workspaceTab {
		case sectionHistory, sectionGraph:
			active = "History and graph"
		case sectionReflog:
			active = "Reflog"
		default:
			active = workspaceSections[a.workspaceTab]
		}
	}
	if a.keybindingsScope != "" {
		active = a.keybindingsScope
	}
	visible := make([]keyBindingGroup, 0, 4)
	for i, group := range groups {
		if group.title == "Custom commands" {
			// Custom commands can fire on any page; keep them visible in the
			// current-page view too.
			visible = append(visible, group)
			continue
		}
		editorActive := group.title == "Code editor" && a.state == stateReady &&
			a.workspaceTab == sectionCode
		if i < 2 || (a.state != stateReady && group.title == "Home") ||
			(a.state == stateReady && (group.title == "Workspace" || group.title == active)) || editorActive {
			if group.title == "Everywhere" && a.inlineConflictResultActive() {
				bindings := group.bindings[:0]
				for _, binding := range group.bindings {
					switch {
					case binding.id == "global/undo":
						binding.label = "Undo unsaved Result text, or the last hunk resolution when Result has no unsaved edits"
					case binding.id == "global/redo", binding.id == "git/Everywhere/Pull from the upstream branch",
						binding.id == "git/Everywhere/Push to the remote",
						binding.id == "git/Everywhere/Undo the latest eligible Git operation",
						binding.id == "git/Everywhere/Redo the latest undone Git operation":
						// Result owns its text/hunk history here; Git Operation Redo
						// and the Git actions on their own keys are not registered.
						continue
					}
					bindings = append(bindings, binding)
				}
				group.bindings = bindings
			}
			if group.title == "Changes" {
				group.bindings = a.contextualChangesBindings(group.bindings)
				group.bindings = append(group.bindings, a.changesActionHelp()...)
			}
			if group.title == "Diff review" {
				custom := a.customPatchActive()
				group.bindings = nil
				keys := a.keyResolution()
				for i, command := range diffKeyboardCommands() {
					if command.selection && !a.keyboardDiffCanSelect || command.changesHunk && !a.keyboardDiffCanMutateHunks {
						continue
					}
					label := command.label
					if custom && command.key == ui.KeySpace {
						label = "Add selected lines"
					}
					ref := keyCommandRefs()[keyRefsGit+i]
					group.bindings = append(group.bindings, keyBinding{keys: keys.keys(ref), label: label, id: ref.id})
				}
				if custom {
					group.bindings = append(group.bindings, keyBinding{keys: "A", label: "Add hunk"}, keyBinding{keys: "B", label: "Review Custom Patch"})
				}
			}
			visible = append(visible, group)
		}
	}
	return visible
}

// keybindingsBody draws the groups that still have a matching shortcut, and a
// quiet line when the filter matches nothing.
func (a *app) keybindingsBody(c *ui.Context) {
	theme := c.Theme()
	filter := strings.ToLower(strings.TrimSpace(a.keybindingFilter))
	shown := 0
	ui.Column(c).FillWidth().Gap(14).Children(func() {
		for _, group := range a.visibleKeyBindingGroups() {
			matches := matchingKeyBindings(group, filter)
			if a.keyEdit.searchByKey && a.keyEdit.searchChord != "" {
				pressed := make([]keyBinding, 0, len(matches))
				for _, binding := range matches {
					if bindingHasChord(binding.keys, a.keyEdit.searchChord) {
						pressed = append(pressed, binding)
					}
				}
				matches = pressed
			}
			if len(matches) == 0 {
				continue
			}
			shown += len(matches)
			ui.Column(c).FillWidth().Gap(5).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(7).Children(func() {
					ui.Icon(c, group.icon).FontSize(14).TextColor(theme.Accent)
					ui.Text(c, tr(group.title)).Bold().Grow(1)
					ui.Badge(c, fmt.Sprintf("%d", len(matches)))
				})
				for _, binding := range matches {
					a.keyBindingRow(c, binding)
				}
			})
		}
		if shown == 0 {
			ui.Column(c).Grow(1).Center().Gap(8).Padding(24).Children(func() {
				ui.Icon(c, searchIcon).FontSize(24).TextColor(theme.TextMuted)
				ui.Text(c, tr("No shortcut matches ")+strings.TrimSpace(a.keybindingFilter)).
					TextColor(theme.TextMuted)
			})
		}
	})
}

func (a *app) inlineConflictResultActive() bool {
	return a.state == stateReady && a.workspaceTab == sectionChanges && a.conflictSides == nil &&
		a.conflictState != nil && a.conflictStageTab == 3
}

// contextualChangesBindings lists only shortcuts registered by the active
// editor scope. Ordinary Changes commands do not leak into Result's modified
// conflict shortcuts, and ordinary files do not advertise unavailable Alt+1…4.
func (a *app) contextualChangesBindings(bindings []keyBinding) []keyBinding {
	conflictKeys := map[string]bool{
		"F7 / Shift+F7": true, "Alt+1 / Alt+2": true, "Alt+3 / Alt+4": true, "Alt+Z": true,
	}
	active := a.inlineConflictResultActive()
	kept := make([]keyBinding, 0, len(bindings))
	for _, binding := range bindings {
		if conflictKeys[binding.keys] == active {
			kept = append(kept, binding)
		}
	}
	return kept
}

// changesActionHelp explains action scope without inventing keyboard
// shortcuts. Empty keys render as explanatory rows, not as key chips.
func (a *app) changesActionHelp() []keyBinding {
	if a.conflictSides != nil {
		return []keyBinding{
			{label: "Mark resolved stages the file exactly as it is. Combined conflict diffs cannot use Stage hunk or Discard hunk; resolve the conflict or choose a whole-file action instead."},
		}
	}
	if a.conflictState != nil {
		return []keyBinding{
			{label: "Undo resolution restores Result to before the last hunk pick. It does not change the index or Git operation history; Undo can also reverse Restore initial conflict."},
			{label: "Restore initial conflict resets Result to the conflict first opened, including markers removed by earlier picks. It does not reset a branch, index, or commit."},
			{label: "Mark resolved stages the file exactly as it is. Combined conflict diffs cannot use Stage hunk or Discard hunk; resolve the conflict or choose a whole-file action instead."},
			{label: "Conflict Assistant runs only after an explicit request. It previews suggestions on a clone; Accept writes Result only with V2 recovery and never stages, continues, or commits."},
		}
	}
	return []keyBinding{
		{label: "Stage hunk applies only the current hunk to the index. Unstage hunk reverses only that index hunk and leaves its content in the working tree."},
		{label: "Discard hunk asks for confirmation, removes only the current working-tree hunk, leaves the index untouched, and keeps a recovery copy for 7 days in Saved work."},
	}
}

func matchingKeyBindings(group keyBindingGroup, filter string) []keyBinding {
	if filter == "" {
		return group.bindings
	}
	matches := make([]keyBinding, 0, len(group.bindings))
	for _, binding := range group.bindings {
		haystack := strings.ToLower(tr(group.title) + " " + binding.keys + " " + tr(binding.label))
		if strings.Contains(haystack, filter) {
			matches = append(matches, binding)
		}
	}
	return matches
}

// keyBindingRow is one shortcut: its keys in a chip, then what they do.
func (a *app) keyBindingRow(c *ui.Context, binding keyBinding) {
	theme := c.Theme()
	if ref, ok := keyCommandRefByID(binding.id); ok && a.keyEdit.editing {
		a.keyBindingEditRow(c, ref, binding)
		return
	}
	ui.Row(c).AlignItems(ui.Center).Gap(12).Padding(4, 6).Children(func() {
		if binding.keys == "" {
			ui.Icon(c, questionIcon).FontSize(14).TextColor(theme.Accent)
			ui.Text(c, tr(binding.label)).TextColor(theme.TextMuted).Grow(1)
			return
		}
		// While customizing, the keys the window still keeps for itself are
		// dimmed, and spaced like the rows that can change.
		fixed := a.keyEdit.editing && binding.id == ""
		chip := ui.Box(c).Width(150).Padding(3, 8).Radius(theme.Radius-1).
			Background(theme.Surface).Border(1, theme.Border)
		if fixed {
			chip.Tooltip(tr("This key is built in and cannot be changed yet"))
		}
		chip.Children(func() {
			if fixed {
				keyChipTextColor(c, binding.keys, theme.TextMuted)
			} else {
				keyChipText(c, binding.keys)
			}
		})
		if a.keyEdit.editing {
			ui.Box(c).Width(2 * 28).MinWidth(2 * 28).Height(28)
		}
		ui.Text(c, tr(binding.label)).TextColor(theme.TextMuted).Grow(1)
	})
}

func (a *app) keybindingsFooter(c *ui.Context) {
	ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
		a.hotkeysSwitch(c)
		ui.Box(c).Grow(1)
		if a.keyEdit.editing {
			restore := ui.Button(c, "").Gap(6).Label(tr("Restore all defaults")).
				Tooltip(tr("Remove every customized key and use the defaults again")).
				Disabled(len(a.settings.KeyBindings) == 0)
			restore.Children(func() {
				ui.Icon(c, revertIcon).FontSize(14)
				ui.Text(c, tr("Restore defaults"))
			})
			if restore.Clicked() {
				a.restoreCommandKeys(c)
			}
		}
		if ui.PrimaryButton(c, tr("Done")).Clicked() {
			a.closeKeybindings()
		}
	})
}

// keyBindingEditRow is one customizable action while the dialog is in its
// customizing mode. The key to record sits where the chip does, so the
// descriptions stay in one column; the small buttons follow the description.
func (a *app) keyBindingEditRow(c *ui.Context, ref keyCommandRef, binding keyBinding) {
	theme := c.Theme()
	c.Key("key-edit:" + ref.id)
	bound := a.keysOf(ref.id)
	refused := a.keyEdit.errID == ref.id && a.keyEdit.err != ""
	ui.Column(c).FillWidth().Gap(2).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Padding(2, 6).Children(func() {
			if a.keyEdit.recording == ref.id {
				a.keySlotRecorder(c, ref, 0, refused)
			} else {
				text := tr("Not set")
				if len(bound) > 0 {
					text = keyDefinitionChord(ref, bound[0])
				}
				borderColor := theme.Border
				if refused {
					borderColor = theme.Danger
				}
				button := ui.ButtonBase(c).Width(keySlotWidth).MinWidth(keySlotWidth).Height(30).Gap(6).Padding(3, 8).
					Radius(theme.Radius-1).Background(theme.Surface).Border(1, borderColor).
					Label(trf("Record shortcut for %s", tr(ref.label))).Tooltip(tr("Click, then press a shortcut"))
				if button.Hovered() {
					button.Background(theme.SurfaceHover)
				}
				button.Children(func() {
					ui.Icon(c, keyboardIcon).FontSize(12).TextColor(theme.TextMuted)
					keyChipText(c, text).Ellipsis("…").Grow(1)
				})
				if button.Clicked() {
					a.beginKeyRecording(ref, 0)
				}
			}
			keyActionCell(c, func() {
				flatIconButton(c, trf("Clear %s shortcut", tr(ref.label)), "Remove this key so the action has none", trashIcon, 13, 4,
					len(bound) == 0, func() { a.setCommandKey(c, ref, 0, nil) })
			})
			keyActionCell(c, func() {
				if _, customized := a.settings.KeyBindings[ref.id]; customized {
					flatIconButton(c, trf("Reset %s shortcuts", tr(ref.label)), "Restore this action's default shortcuts", revertIcon, 13, 4, false, func() {
						a.resetCommandKey(c, ref)
					})
				}
			})
			ui.Text(c, tr(binding.label)).TextColor(theme.TextMuted).Grow(1).SingleLine().Ellipsis("…")
		})
		if refused {
			ui.Text(c, a.keyEdit.err).TextColor(theme.Danger).FontSize(theme.FontSize-1).Padding(0, 6, 4)
		}
	})
}

// keySlotWidth fits the longest chord and the recording prompt.
const keySlotWidth = 150

func (a *app) keySlotRecorder(c *ui.Context, ref keyCommandRef, slot int, refused bool) {
	theme := c.Theme()
	border := theme.Accent
	if refused {
		border = theme.Danger
	}
	input := ui.ButtonBase(c).Width(keySlotWidth).MinWidth(keySlotWidth).Height(30).Gap(5).Padding(2, 7).
		AutoFocus().Border(1, border).Radius(theme.Radius).Background(border.Alpha(0.09)).
		Label(trf("Record shortcut for %s", tr(ref.label))).
		Tooltip(tr("Escape cancels; Backspace clears this shortcut"))
	input.Children(func() {
		ui.Icon(c, keyboardIcon).FontSize(12).TextColor(theme.Accent)
		ui.Text(c, tr("Press a shortcut")).FontSize(theme.FontSize - 1).TextColor(theme.TextMuted).
			Grow(1).SingleLine().Ellipsis("…")
	})
	input.HandleInput(func(event ui.InputEvent) bool {
		return a.keyCaptureEvent(c, ref, slot, event)
	})
}

// keyChipText draws a key in its chip. A single letter is easy to misread at
// the usual size, so it is drawn larger, and a capital (Shift and the letter)
// in bold, so p and P cannot be mistaken for each other.
func keyChipText(c *ui.Context, text string) ui.Element {
	return keyChipTextColor(c, text, c.Theme().Accent)
}

func keyChipTextColor(c *ui.Context, text string, color ui.Color) ui.Element {
	theme := c.Theme()
	size := theme.FontSize - 1
	letter := len(text) == 1 && (text[0] >= 'a' && text[0] <= 'z' || text[0] >= 'A' && text[0] <= 'Z')
	if letter {
		size = theme.FontSize + 1
	}
	e := ui.Text(c, text).FontSize(size).TextColor(color).SingleLine()
	if letter && text[0] <= 'Z' {
		e.Bold()
	}
	return e
}

// keyActionCell gives a row's small buttons a fixed width, so the key boxes of
// rows with and without them line up.
func keyActionCell(c *ui.Context, content func()) {
	ui.Row(c).Width(28).MinWidth(28).Height(28).Justify(ui.Center).AlignItems(ui.Center).Children(content)
}

// keyEditToggle is the header button that turns customizing on and off, so a
// key is never changed by an accidental click while reading the list.
func (a *app) keyEditToggle(c *ui.Context) {
	theme := c.Theme()
	label := tr("Customize keys")
	if a.keyEdit.editing {
		label = tr("Finish customizing")
	}
	button := ui.ButtonBase(c).Padding(6).Radius(theme.Radius).Label(label).Tooltip(label)
	if a.keyEdit.editing {
		button.Background(theme.Accent.Alpha(0.18))
	} else if button.Hovered() {
		button.Background(theme.SurfaceHover)
	}
	button.Children(func() {
		ui.Icon(c, editIcon).FontSize(16).TextColor(theme.Accent)
	})
	if button.Clicked() {
		a.keyEdit.editing = !a.keyEdit.editing
		a.stopKeyRecording()
	}
}

// keySearchRecorder is the filter while it waits for a key press: the keys
// pressed become the filter, so a shortcut can be found by its keys and not
// only by its words.
func (a *app) keySearchRecorder(c *ui.Context) {
	theme := c.Theme()
	text := tr("Press the shortcut you are looking for")
	color := theme.TextMuted
	if a.keyEdit.searchLabel != "" {
		text, color = a.keyEdit.searchLabel, theme.Accent
	}
	input := ui.ButtonBase(c).Height(32).Gap(6).Padding(3, 10).Grow(1).AutoFocus().
		Border(1, theme.Accent).Radius(theme.Radius).Background(theme.Accent.Alpha(0.09)).
		Label(tr("Find a shortcut by pressing it")).Tooltip(tr("Escape stops searching by key; Backspace clears the keys"))
	input.Children(func() {
		ui.Icon(c, keyboardIcon).FontSize(13).TextColor(theme.Accent)
		ui.Text(c, text).TextColor(color).SingleLine().Ellipsis("…").Grow(1)
	})
	input.HandleInput(func(event ui.InputEvent) bool {
		if event.Kind == ui.InputText || event.Kind == ui.InputCompose || event.Kind == ui.InputCommand {
			return true
		}
		if event.Kind != ui.InputKeyDown {
			return false
		}
		if event.Repeat {
			return true
		}
		key := event.Key
		if key == ui.KeyUnknown {
			key = event.PhysicalKey
		}
		switch {
		case key == ui.KeyUnknown:
			return true
		case key == ui.KeyEscape && event.Mods == 0:
			a.keyEdit.searchByKey, a.keyEdit.searchChord, a.keyEdit.searchLabel = false, "", ""
			return true
		case key == ui.KeyBackspace && event.Mods == 0:
			a.keyEdit.searchChord, a.keyEdit.searchLabel = "", ""
			return true
		case key == ui.KeyTab && event.Mods == 0:
			return false
		}
		b := boundKey{event.Mods, key}
		a.keyEdit.searchChord, a.keyEdit.searchLabel = normalizeChord(boundKeyLabel(b)), boundKeyLabel(b)
		return true
	})
}

// normalizeChord puts a key's spelling in one form, so the keys pressed can be
// compared with the ones the list shows: a letter keeps its case (p and P are
// different keys), Shift and a letter is the capital, and the rest ignores case.
func normalizeChord(text string) string {
	text = strings.TrimSpace(text)
	text = strings.ReplaceAll(text, "Cmd/Ctrl", "Ctrl")
	text = strings.ReplaceAll(text, "Cmd", "Ctrl")
	if len(text) == 1 {
		return text
	}
	if rest, ok := strings.CutPrefix(text, "Shift+"); ok && len(rest) == 1 && rest[0] >= 'A' && rest[0] <= 'Z' {
		return rest
	}
	lower := strings.ToLower(text)
	if lower == "esc" {
		return "escape"
	}
	return lower
}

// bindingHasChord reports whether one of the keys a binding is shown with is
// the chord pressed; a binding lists alternatives separated by " / ".
func bindingHasChord(keys, chord string) bool {
	for _, alternative := range strings.Split(keys, " / ") {
		if normalizeChord(alternative) == chord {
			return true
		}
	}
	return false
}
