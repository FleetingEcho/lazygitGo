package repository

import "sync/atomic"

// safetyJournalOn is the user's explicit choice to take a recovery point
// first. Its zero value is off. It is process-wide because it is a preference
// of the window, and every repository tab and SSH workspace reads the same one.
var safetyJournalOn atomic.Bool

// SetSafetyJournalEnabled turns the recovery point and journal entry that
// precede the actions reaching beyond the Changes page on or off. It is off by
// default.
//
// Everything done from the Changes page (stage, unstage, partial staging,
// discard, ignore, stash, commit) is always recorded and can be undone, with the
// switch on or off: that work is not in any commit. With it off, checkout,
// merge, revert, branch changes, stash apply and drop, and similar actions just
// run: nothing is saved for Undo and they are not listed in the Safety Journal.
// History rewrites and operation transitions that Git's reflog cannot
// faithfully reconstruct (rebase, reset, patch import/editing, Continue, Skip
// and Abort) keep their recovery point regardless.
func SetSafetyJournalEnabled(enabled bool) {
	safetyJournalOn.Store(enabled)
}

// SafetyJournalEnabled reports whether actions take a recovery point first.
func SafetyJournalEnabled() bool {
	return safetyJournalOn.Load()
}
