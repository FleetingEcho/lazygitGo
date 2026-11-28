package app

import (
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
)

// Customizable keys. Every Git action in keyboardCommands and every diff action
// in diffKeyboardCommands has a stable id, a scope (the part of the window it
// acts in) and a default key. appSettings.KeyBindings maps an id to the keys
// the user chose instead; an id that is absent keeps its default, and an empty
// list leaves the action without a key.

// boundKey is one key press: modifiers and a key.
type boundKey struct {
	mods ui.Modifiers
	key  ui.Key
}

// keyDiffScope is the scope of the diff review actions.
const keyDiffScope = "Diff review"

// keyCommandRef describes one customizable action.
type keyCommandRef struct {
	id, scope, label, keys string
	def                    boundKey
	// logID is the id the command log files the shortcut under, when it is not
	// derived from the label; window marks a key that works whatever has focus.
	logID  string
	window bool
}

// globalKeyCommand is a window-wide action with a customizable key. The action
// itself is run by the place that registers it (see onGlobalKey), because what
// it needs, and when it applies, differs from key to key.
type globalKeyCommand struct {
	id, logID, label, keys string
	mods                   ui.Modifiers
	key                    ui.Key
	window                 bool
}

func globalKeyCommands() []globalKeyCommand {
	return []globalKeyCommand{
		{"global/settings", "settings/open", "Open settings", "Ctrl+,", ui.Ctrl, ui.KeyComma, false},
		{"global/shortcuts-all", "keybindings/open-all", "Open the complete shortcut list", "F1", 0, ui.KeyF1, false},
		{"global/shortcuts-page", "keybindings/open-current", "Show shortcuts for the current page", "?", ui.Shift, ui.KeySlash, false},
		{"global/command-log", "command-log/open", "Show the command log", "Ctrl+L", ui.Ctrl, ui.KeyL, false},
		{"global/pane-next", "panes/cycle-next", "Move to the next panel", "F6", 0, ui.KeyF6, false},
		{"global/pane-previous", "panes/cycle-previous", "Move to the previous panel", "Shift+F6", ui.Shift, ui.KeyF6, false},
		{"global/section-next", "workspace/next-section", "Go to the next section", "Alt+PageDown", ui.Alt, ui.KeyPageDown, false},
		{"global/section-previous", "workspace/previous-section", "Go to the previous section", "Alt+PageUp", ui.Alt, ui.KeyPageUp, false},
		{"global/quit", "app/quit", "Quit lazygitGo", "Ctrl+Q", ui.Ctrl, ui.KeyQ, false},
		{"global/toggle-list-pane", "workspace/toggle-list-pane", "Collapse or expand the list pane", "Alt+B", ui.Alt, ui.KeyB, false},
		{"global/toggle-sidebar", "workspace/toggle-sidebar", "Collapse or expand the workspace sidebar", "Alt+N", ui.Alt, ui.KeyN, false},
		{"global/refresh", "repository/refresh", "Refresh the repository", "F5", 0, ui.KeyF5, false},
		{"global/search-next", "search/next", "Move to the next quick-search result", "F3", 0, ui.KeyF3, false},
		{"global/search-previous", "search/previous", "Move to the previous quick-search result", "Shift+F3", ui.Shift, ui.KeyF3, false},
		{"global/search", "search/focus-active", "Search the active panel", "/", 0, ui.KeySlash, false},
		{"global/shell-prompt", "commands/prompt", "Run a one-off shell command", ":", ui.Shift, ui.KeySemicolon, false},
		{"global/undo", "repository/undo", "Undo the latest eligible Git operation", "Ctrl+Z", ui.Ctrl, ui.KeyZ, true},
		{"global/redo", "repository/redo", "Redo the latest undone Git operation", "Ctrl+Shift+Z", ui.Ctrl | ui.Shift, ui.KeyZ, true},
		{"global/compare", "compare/open", "Compare two refs", "W", 0, ui.KeyW, false},
		{"global/whitespace", "diff/toggle-whitespace", "Ignore or show whitespace-only diff changes", "Alt+W", ui.Alt, ui.KeyW, false},
		{"global/context-lines", "diff/context-lines", "Cycle ordinary diff context lines", "Alt+U", ui.Alt, ui.KeyU, false},
	}
}

var (
	keyRefsOnce  sync.Once
	keyRefs      []keyCommandRef
	keyRefsIndex map[string]int
	// keyRefsGit is how many of keyRefs come from keyboardCommands, in order;
	// the diff actions follow them.
	keyRefsGit int
)

// keyCommandRefs lists every customizable action: the Git commands in the
// order of keyboardCommands, then the diff commands in the order of
// diffKeyboardCommands. The ids match the command catalog's.
func keyCommandRefs() []keyCommandRef {
	keyRefsOnce.Do(func() {
		seen := map[string]int{}
		for _, command := range keyboardCommands() {
			id := "git/" + command.group + "/" + command.label
			seen[id]++
			if seen[id] > 1 {
				// Two bindings of one action share a label; the later ones are
				// told apart by their default key.
				id += "/" + command.keys
			}
			keyRefs = append(keyRefs, keyCommandRef{
				id: id, scope: command.group, label: command.label, keys: command.keys,
				def: boundKey{command.mods, command.key},
			})
		}
		keyRefsGit = len(keyRefs)
		for _, command := range diffKeyboardCommands() {
			keyRefs = append(keyRefs, keyCommandRef{
				id: "diff/" + command.label, scope: keyDiffScope, label: command.label, keys: command.keys,
				def: boundKey{command.mods, command.key},
			})
		}
		for _, command := range globalKeyCommands() {
			keyRefs = append(keyRefs, keyCommandRef{
				id: command.id, scope: "Everywhere", label: command.label, keys: command.keys,
				def: boundKey{command.mods, command.key}, logID: command.logID, window: command.window,
			})
		}
		keyRefsIndex = make(map[string]int, len(keyRefs))
		for i, ref := range keyRefs {
			keyRefsIndex[ref.id] = i
		}
	})
	return keyRefs
}

func keyCommandRefByID(id string) (keyCommandRef, bool) {
	refs := keyCommandRefs()
	i, ok := keyRefsIndex[id]
	if !ok {
		return keyCommandRef{}, false
	}
	return refs[i], true
}

// keyScopesOverlap reports whether two actions can be live at once, so that
// one key could not tell them apart. Everywhere actions run beside every other
// scope; two other scopes only meet when they are the same.
func keyScopesOverlap(a, b string) bool {
	return a == b || a == "Everywhere" || b == "Everywhere"
}

// keyChordText is the settings spelling of a key press, such as "p",
// "shift+p" or "ctrl+alt+d".
func keyChordText(b boundKey) string {
	name, ok := keyDisplayNames[b.key]
	if !ok {
		return ""
	}
	parts := make([]string, 0, 5)
	if b.mods&ui.Ctrl != 0 {
		parts = append(parts, "ctrl")
	}
	if b.mods&ui.Alt != 0 {
		parts = append(parts, "alt")
	}
	if b.mods&ui.Shift != 0 {
		parts = append(parts, "shift")
	}
	if b.mods&ui.Super != 0 {
		parts = append(parts, "super")
	}
	return strings.ToLower(strings.Join(append(parts, name), "+"))
}

// boundKeyLabel is how a key press reads in the shortcut list: a bare letter
// as "p", Shift and a letter as "P", anything else as "Ctrl+Alt+D".
func boundKeyLabel(b boundKey) string {
	name := keyDisplayNames[b.key]
	letter := len(name) == 1 && name[0] >= 'A' && name[0] <= 'Z'
	switch {
	case b.mods == 0 && letter:
		return strings.ToLower(name)
	case b.mods == ui.Shift && letter:
		return name
	}
	parts := make([]string, 0, 5)
	if b.mods&ui.Ctrl != 0 {
		parts = append(parts, "Ctrl")
	}
	if b.mods&ui.Alt != 0 {
		parts = append(parts, "Alt")
	}
	if b.mods&ui.Shift != 0 {
		parts = append(parts, "Shift")
	}
	if b.mods&ui.Super != 0 {
		if ui.Cmd == ui.Super {
			parts = append(parts, "Cmd")
		} else {
			parts = append(parts, "Super")
		}
	}
	return strings.Join(append(parts, name), "+")
}

// parseBoundKey reads a key press from its settings spelling. Unlike the code
// editor's shortcuts, a Git action may sit on a bare key, as lazygit's do; only
// the keys that move focus and close things stay out.
func parseBoundKey(text string) (boundKey, error) {
	mods, key, ok := parseCustomCommandKey(text)
	if !ok {
		return boundKey{}, errors.New(tr("shortcuts must be like p, shift+p or ctrl+alt+d"))
	}
	b := boundKey{mods, key}
	if keyChordText(b) == "" {
		return boundKey{}, errors.New(tr("This key cannot be used as a shortcut"))
	}
	if key == ui.KeyEscape || key == ui.KeyTab || (key == ui.KeyEnter && mods == 0) {
		return boundKey{}, errors.New(tr("Escape, Tab and Enter are kept for moving around and closing"))
	}
	return b, nil
}

// reservedKey is a key the window answers to itself, outside the customizable
// table. Until those move into the table too, an action cannot take them.
type reservedKey struct {
	b     boundKey
	label string
}

func reservedKeys() []reservedKey {
	cmd, cmdShift := ui.Cmd, ui.Cmd|ui.Shift
	keys := []reservedKey{
		{boundKey{cmdShift, ui.KeyP}, "Open Command Palette"},
		{boundKey{cmd, ui.KeyP}, "Search files"},
		{boundKey{cmdShift, ui.KeyA}, "Open Activity Center"},
		{boundKey{cmd, ui.KeyT}, "Open a new tab or the picker"},
		{boundKey{cmd, ui.KeyW}, "Close the active tab"},
		{boundKey{ui.Ctrl, ui.KeyTab}, "Switch to the next tab"},
		{boundKey{ui.Ctrl | ui.Shift, ui.KeyTab}, "Switch to the previous tab"},
		{boundKey{ui.Ctrl, ui.KeyBackquote}, "Toggle the terminal"},
		{boundKey{ui.Ctrl | ui.Shift, ui.KeyBackquote}, "Toggle the terminal"},
		{boundKey{ui.Ctrl, ui.KeyF}, "Search the active terminal"},
		{boundKey{0, ui.KeyF7}, "Move to the next / previous conflict"},
		{boundKey{ui.Shift, ui.KeyF7}, "Move to the next / previous conflict"},
		{boundKey{cmd, ui.KeyS}, "Save"},
		{boundKey{ui.Ctrl, ui.KeyS}, "Save"},
		{boundKey{ui.Shift, ui.KeyW}, "Compare two refs"},
		{boundKey{ui.Alt, ui.Key1}, "Keep the current / ancestor conflict hunk"},
		{boundKey{ui.Alt, ui.Key2}, "Keep the current / ancestor conflict hunk"},
		{boundKey{ui.Alt, ui.Key3}, "Keep the incoming conflict hunk / both hunks"},
		{boundKey{ui.Alt, ui.Key4}, "Keep the incoming conflict hunk / both hunks"},
		{boundKey{ui.Alt, ui.KeyZ}, "Undo the last conflict resolution"},
		{boundKey{0, ui.KeyJ}, "Move through a list"},
		{boundKey{0, ui.KeyK}, "Move through a list"},
		{boundKey{0, ui.KeyUp}, "Move through a list"},
		{boundKey{0, ui.KeyDown}, "Move through a list"},
	}
	for digit := 0; digit < 10; digit++ {
		keys = append(keys, reservedKey{boundKey{0, ui.Key(int(ui.Key0) + digit)}, "Switch to a workspace section"})
	}
	return keys
}

func reservedKeyOwner(b boundKey) (string, bool) {
	for _, reserved := range reservedKeys() {
		if reserved.b == b {
			return reserved.label, true
		}
	}
	return "", false
}

// keyResolution is the keys every action ends up with, and the first problem
// the user's choices have. With a problem the defaults apply, so one bad entry
// in a settings file never leaves the window without keys.
type keyResolution struct {
	byID  map[string][]boundKey
	issue error
}

// keys is the text for an action's keys in the shortcut list.
func (r keyResolution) keys(ref keyCommandRef) string {
	bound := r.byID[ref.id]
	switch {
	case len(bound) == 0:
		return tr("Not set")
	case len(bound) == 1 && bound[0] == ref.def:
		return ref.keys
	}
	labels := make([]string, len(bound))
	for i, b := range bound {
		labels[i] = boundKeyLabel(b)
	}
	return strings.Join(labels, " / ")
}

var (
	defaultKeysOnce sync.Once
	defaultKeys     keyResolution
)

func defaultKeyResolution() keyResolution {
	defaultKeysOnce.Do(func() {
		defaultKeys = keyResolution{byID: map[string][]boundKey{}}
		for _, ref := range keyCommandRefs() {
			defaultKeys.byID[ref.id] = []boundKey{ref.def}
		}
	})
	return defaultKeys
}

func keyProblem(label, format string, args ...any) error {
	return keyProblemText(label, trf(format, args...))
}

// keyProblemText is a problem whose text is already translated.
func keyProblemText(label, text string) error {
	return errors.New(tr(label) + ": " + text)
}

// resolveKeyOverrides applies the user's keys over the defaults and refuses
// them as a whole when one cannot stand: more than two keys for an action, an
// unusable or doubled key, a key the window keeps for itself, or a key that
// another action in an overlapping scope already has. Two defaults that
// already share a key (a deliberate shadow, such as Push tag over Push) are not
// flagged; only a customized action is held to the rules.
func resolveKeyOverrides(overrides map[string][]string) keyResolution {
	refs := keyCommandRefs()
	if len(overrides) == 0 {
		return defaultKeyResolution()
	}
	fail := func(err error) keyResolution {
		res := defaultKeyResolution()
		return keyResolution{byID: res.byID, issue: err}
	}
	effective := make([][]boundKey, len(refs))
	custom := make([]bool, len(refs))
	for i, ref := range refs {
		raw, changed := overrides[ref.id]
		if !changed {
			effective[i] = []boundKey{ref.def}
			continue
		}
		custom[i] = true
		if len(raw) > 1 {
			return fail(keyProblem(ref.label, "choose only one key"))
		}
		for _, text := range raw {
			b, err := parseBoundKey(text)
			if err != nil {
				return fail(keyProblemText(ref.label, err.Error()))
			}
			if slices.Contains(effective[i], b) {
				return fail(keyProblem(ref.label, "%s is assigned twice", boundKeyLabel(b)))
			}
			if owner, reserved := reservedKeyOwner(b); reserved && b != ref.def {
				return fail(keyProblem(ref.label, "%s is reserved for %s", boundKeyLabel(b), tr(owner)))
			}
			effective[i] = append(effective[i], b)
		}
	}
	claims := map[boundKey][]int{}
	for i, ref := range refs {
		for _, b := range effective[i] {
			for _, earlier := range claims[b] {
				if !keyScopesOverlap(ref.scope, refs[earlier].scope) || (!custom[i] && !custom[earlier]) {
					continue
				}
				subject, other := i, earlier
				if !custom[i] {
					subject, other = earlier, i
				}
				return fail(keyProblem(refs[subject].label, "%s is already used by %s", boundKeyLabel(b),
					tr(refs[other].label)))
			}
			claims[b] = append(claims[b], i)
		}
	}
	res := keyResolution{byID: make(map[string][]boundKey, len(refs))}
	for i, ref := range refs {
		res.byID[ref.id] = effective[i]
	}
	return res
}

// keyOverridesSignature keys the resolution cache.
func keyOverridesSignature(overrides map[string][]string) string {
	ids := make([]string, 0, len(overrides))
	for id := range overrides {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, id := range ids {
		b.WriteString(id)
		b.WriteByte('=')
		b.WriteString(strings.Join(overrides[id], ","))
		b.WriteByte(';')
	}
	return b.String()
}

func cloneKeyOverrides(overrides map[string][]string) map[string][]string {
	if len(overrides) == 0 {
		return nil
	}
	cloned := make(map[string][]string, len(overrides))
	for id, chords := range overrides {
		cloned[id] = append([]string{}, chords...)
	}
	return cloned
}

// keyEditState is the shortcut dialog's customizing mode.
type keyEditState struct {
	// searchByKey turns the dialog's filter into a recorder: the keys pressed
	// are the filter. searchChord is their normalized spelling, searchLabel
	// what to show.
	searchByKey bool
	searchChord string
	searchLabel string
	editing     bool
	recording   string
	slot        int
	err         string
	// errID is the action the error belongs to, so its row can show it.
	errID     string
	cache     keyResolution
	signature string
	cached    bool
}

// keyResolution is the keys in effect for the current settings.
func (a *app) keyResolution() keyResolution {
	overrides := a.settings.KeyBindings
	if len(overrides) == 0 {
		return defaultKeyResolution()
	}
	signature := keyOverridesSignature(overrides)
	if !a.keyEdit.cached || a.keyEdit.signature != signature {
		a.keyEdit.cache, a.keyEdit.signature, a.keyEdit.cached = resolveKeyOverrides(overrides), signature, true
	}
	return a.keyEdit.cache
}

// keysOf are the keys an action answers to now.
func (a *app) keysOf(id string) []boundKey { return a.keyResolution().byID[id] }

// keyDefinitionChord is the chord shown for the first key of an action.
func keyDefinitionChord(ref keyCommandRef, b boundKey) string {
	if b == ref.def {
		return ref.keys
	}
	return boundKeyLabel(b)
}

func (a *app) beginKeyRecording(ref keyCommandRef, slot int) {
	a.keyEdit.recording, a.keyEdit.slot, a.keyEdit.err, a.keyEdit.errID = ref.id, slot, "", ""
}

func (a *app) stopKeyRecording() {
	a.keyEdit.recording, a.keyEdit.err, a.keyEdit.errID = "", "", ""
}

// setCommandKey puts a key in one slot of an action, or empties the slot when
// b is nil. Each action has one key; the change is kept only if the whole set still resolves.
func (a *app) setCommandKey(c *ui.Context, ref keyCommandRef, slot int, b *boundKey) {
	draft := cloneKeyOverrides(a.settings.KeyBindings)
	if draft == nil {
		draft = map[string][]string{}
	}
	raw := []string{}
	if b != nil {
		raw = append(raw, keyChordText(*b))
	}
	if len(raw) == 1 && raw[0] == keyChordText(ref.def) {
		delete(draft, ref.id)
	} else {
		draft[ref.id] = raw
	}
	if res := resolveKeyOverrides(draft); res.issue != nil {
		a.keyRefused(ref, res.issue)
		return
	}
	a.stopKeyRecording()
	if len(draft) == 0 {
		draft = nil
	}
	a.settings.KeyBindings = draft
	a.settingsChanged(c)
}

// resetCommandKey returns one action to its default key.
func (a *app) resetCommandKey(c *ui.Context, ref keyCommandRef) {
	draft := cloneKeyOverrides(a.settings.KeyBindings)
	delete(draft, ref.id)
	if len(draft) == 0 {
		draft = nil
	}
	if res := resolveKeyOverrides(draft); res.issue != nil {
		a.keyRefused(ref, res.issue)
		return
	}
	a.settings.KeyBindings = draft
	a.stopKeyRecording()
	a.settingsChanged(c)
}

// restoreCommandKeys drops every customized key.
func (a *app) restoreCommandKeys(c *ui.Context) {
	a.settings.KeyBindings = nil
	a.stopKeyRecording()
	a.settingsChanged(c)
}

// keyCaptureEvent reads the key press a recording slot is waiting for. It
// reports whether the event was consumed.
func (a *app) keyCaptureEvent(c *ui.Context, ref keyCommandRef, slot int, event ui.InputEvent) bool {
	if a.keyEdit.recording != ref.id || a.keyEdit.slot != slot {
		return false
	}
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
	if key == ui.KeyUnknown {
		return true
	}
	switch {
	case key == ui.KeyEscape && event.Mods == 0:
		a.stopKeyRecording()
		return true
	case key == ui.KeyTab && event.Mods == 0:
		a.stopKeyRecording()
		return false
	case key == ui.KeyBackspace && event.Mods == 0:
		a.setCommandKey(c, ref, slot, nil)
		return true
	case key == ui.KeyEnter && event.Mods == 0:
		return true
	}
	b, err := parseBoundKey(keyChordText(boundKey{event.Mods, key}))
	if err != nil {
		a.keyRefused(ref, err)
		return true
	}
	a.setCommandKey(c, ref, slot, &b)
	return true
}

// keyRefused keeps a refused key from taking effect and says why: in red under
// the action's row, and as a notification, since the row may be scrolled away
// from the message at the top.
func (a *app) keyRefused(ref keyCommandRef, err error) {
	a.keyEdit.err, a.keyEdit.errID = err.Error(), ref.id
	a.notify("error", "Shortcut not changed", err.Error())
}

// onGlobalKey registers a window-wide action on every key it now has.
func (a *app) onGlobalKey(c *ui.Context, id string, run func()) {
	ref, ok := keyCommandRefByID(id)
	if !ok {
		return
	}
	for _, b := range a.keysOf(id) {
		def := shortcut(ref.logID, keyDefinitionChord(ref, b), tr(ref.label))
		if ref.window {
			a.onWindowShortcut(c, b.mods, b.key, def, run)
		} else {
			a.onShortcut(c, b.mods, b.key, def, run)
		}
	}
}

// menuKey is the key a context-menu item shows and answers to while the menu is
// open: the one the user gave the action in that scope, found by the key the
// item had by default. An item that is not a customizable action keeps its own
// key, and an action without a key has none.
func (a *app) menuKey(scope string, mods ui.Modifiers, key ui.Key) (ui.Modifiers, ui.Key) {
	def := boundKey{mods, key}
	for _, ref := range keyCommandRefs() {
		if ref.scope != scope || ref.def != def {
			continue
		}
		bound := a.keysOf(ref.id)
		if len(bound) == 0 {
			return 0, ui.KeyUnknown
		}
		return bound[0].mods, bound[0].key
	}
	return mods, key
}

// keyText is how an action's key reads in a hint: the user's key, or the
// default when the action keeps it.
func (a *app) keyText(id, fallback string) string {
	ref, ok := keyCommandRefByID(id)
	if !ok {
		return fallback
	}
	text := a.keyResolution().keys(ref)
	if text == tr("Not set") {
		return ""
	}
	return text
}

// idle reports whether the dialog is waiting for neither a key to assign nor a
// key to search for, so the ordinary dialog keys (Escape, F1) may act.
func (k keyEditState) idle() bool { return k.recording == "" && !k.searchByKey }
