package repository

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	OperationCommit       = "commit"
	OperationMerge        = "merge"
	OperationCherryPick   = "cherry-pick"
	OperationRevert       = "revert"
	OperationStashApply   = "stash-apply"
	OperationStashSave    = "stash-save"
	OperationCheckout     = "checkout"
	OperationFileRestore  = "file-restore"
	OperationBranchCreate = "branch-create"
	OperationBranchRename = "branch-rename"
	OperationStashRewrite = "stash-rewrite"
	OperationTransition   = "operation-transition"
)

// alwaysRecorded names the actions that keep an exact recovery point whatever
// the Safety Journal switch says, because they can destroy work that no commit
// holds: everything done to the working tree and index from the Changes page
// (commit, stash) and the operation transitions Git's reflog cannot rebuild
// (unmerged index stages and operation metadata after Abort, Continue, Skip).
// Staging, unstaging, partial staging, discard and ignore rules take their own
// always-on path. The switch governs the remaining actions.
func alwaysRecorded(kind string) bool {
	switch kind {
	case OperationTransition, OperationCommit, OperationStashSave:
		return true
	}
	return false
}

func stashStateTrackedOperation(kind string) bool {
	switch kind {
	case OperationStashDrop, OperationStashSave, OperationStashApply, OperationStashRewrite:
		return true
	}
	return false
}

func supportsOperationTransitionRecovery(type_ OperationType) bool {
	switch type_ {
	case OperationTypeMerge, OperationTypeCherryPick, OperationTypeRevert, OperationTypeAM:
		return true
	}
	return false
}

// withWorkspaceMutation serializes local mutations and captures exact
// before/after restore points. The action itself supplies any user-facing
// confirmation and remains responsible for trusted Git behavior. Arbitrary
// custom shell commands and remote effects do not belong to this seam.
type workspaceMutationCapture func(context.Context) (WorkSnapshot, error)

func (s *gitSession) withWorkspaceMutation(ctx context.Context, kind, summary string, movesHead bool, mutate func() error, captures ...workspaceMutationCapture) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if mutate == nil {
		return fmt.Errorf("%s: missing mutation", kind)
	}
	unlock := s.lockWorktreeMutation()
	defer unlock()
	return s.runWorkspaceMutation(ctx, kind, summary, movesHead, mutate, captures...)
}

// runWorkspaceMutation must be called with the checkout's mutation lock held.
// It lets existing history actions reuse the same recovery contract without
// recursively taking the lock that they already own.
func (s *gitSession) runWorkspaceMutation(ctx context.Context, kind, summary string, movesHead bool, mutate func() error, captures ...workspaceMutationCapture) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(captures) > 1 {
		return fmt.Errorf("%s: multiple recovery captures", kind)
	}
	if !SafetyJournalEnabled() && !alwaysRecorded(kind) {
		// The user turned the safety journal off: the actions that reach beyond
		// the Changes page (checkout, merge, revert, branch changes) run without
		// a recovery point. See alwaysRecorded for the ones that never do.
		return mutate()
	}
	capture := func(ctx context.Context) (WorkSnapshot, error) {
		return s.captureWorkSnapshot(ctx, RecoverySnapshotKind, summary)
	}
	if len(captures) == 1 {
		if captures[0] == nil {
			return fmt.Errorf("%s: missing recovery capture", kind)
		}
		capture = captures[0]
	}
	_, record, err := s.captureAndBeginOperation(ctx, capture, kind, summary, UndoRestoreWork)
	if err != nil {
		return err
	}
	if movesHead && record.HeadBefore != "" {
		record.UndoKind = UndoResetHead
	}
	// An unborn HEAD cannot be reset to an earlier commit. RestoreWork also
	// restores refs, so removing the new head safely recreates the unborn state.
	if record, err = s.pinOperationBefore(record); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return s.failJournalOperation(record, err)
	}
	actionErr := mutate()
	if err := s.completeJournalOperation(record, actionErr); err != nil {
		return err
	}
	return s.pruneRecovery(ctx)
}

// recoveryAttempts is how often a recovery point is taken again when the
// workspace changed under it. The action has not run yet at that stage, so
// trying again is always safe.
const recoveryAttempts = 4

// isTransientRecoveryError recognizes the two ways a busy checkout defeats one
// attempt at a recovery point: a file changed while it was being saved, or the
// workspace changed between taking the point and checking it.
func isTransientRecoveryError(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "unstable object source data") ||
		strings.Contains(text, "workspace changed while creating the recovery point") ||
		strings.Contains(text, "the file kept changing while it was being saved")
}

// captureAndBeginOperation takes a recovery point and opens the journal entry
// that protects it. Other programs write into a checkout all the time (an
// indexer's database, a build, an editor), and one write between the two steps
// used to refuse the whole action. Because nothing has run yet, the attempt is
// repeated a few times before it is given up; a point that did not hold is
// dropped rather than left behind.
func (s *gitSession) captureAndBeginOperation(ctx context.Context, capture func(context.Context) (WorkSnapshot, error), kind, summary, undoKind string) (WorkSnapshot, OperationRecord, error) {
	var lastErr error
	for attempt := 0; attempt < recoveryAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return WorkSnapshot{}, OperationRecord{}, ctx.Err()
			case <-time.After(time.Duration(attempt) * 150 * time.Millisecond):
			}
		}
		snapshot, err := capture(ctx)
		if err != nil {
			if !isTransientRecoveryError(err) {
				return WorkSnapshot{}, OperationRecord{}, err
			}
			lastErr = err
			continue
		}
		record, err := s.beginOperation(kind, summary, undoKind, &snapshot)
		if err == nil {
			return snapshot, record, nil
		}
		_ = s.DeleteWorkSnapshot(ctx, snapshot)
		if !isTransientRecoveryError(err) {
			return WorkSnapshot{}, OperationRecord{}, err
		}
		lastErr = err
	}
	return WorkSnapshot{}, OperationRecord{}, lastErr
}

// changesCapture is the recovery point for a stash from the Changes page, which
// never writes an ignored file: it changes the index and tracked or untracked
// files, and Git leaves ignored files alone (a commit has its own scoped point). The point
// therefore leaves the ignored tree out of the archive and its fingerprint, which
// is what keeps a repository with a large build directory fast. Its scope says
// so, so the post-action capture and Undo read the same scope.
//
// One thing breaks that rule: an ignore rule file that the action rewrites. A
// stash that takes a changed .gitignore turns ignored files into untracked ones,
// and Undo must then know them. When such a file is among the changes the full
// recovery contract applies.
func (s *gitSession) changesCapture(summary string, touchesWorktree bool) workspaceMutationCapture {
	return func(ctx context.Context) (WorkSnapshot, error) {
		if touchesWorktree {
			dirty, err := s.ignoreRuleFileChanged()
			if err != nil {
				return WorkSnapshot{}, err
			}
			if dirty {
				return s.captureWorkSnapshot(ctx, RecoverySnapshotKind, summary)
			}
		}
		return s.captureWorkSnapshotWithIgnored(ctx, RecoverySnapshotKind, summary, nil, workspaceArchiveScope{AllIgnored: false})
	}
}

// ignoreRuleFileChanged reports whether any .gitignore is modified, staged,
// deleted or untracked.
func (s *gitSession) ignoreRuleFileChanged() (bool, error) {
	output, err := s.cmd.New([]string{"git", "--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=all", "--"}).RunWithOutput()
	if err != nil {
		return false, fmt.Errorf("check ignore rules: %w: %s", err, strings.TrimSpace(output))
	}
	fields := strings.Split(output, "\x00")
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if len(entry) < 4 {
			continue
		}
		if hasIgnoreRuleFile([]string{entry[3:]}) {
			return true, nil
		}
		if entry[0] == 'R' || entry[0] == 'C' { // the next field is the source path
			i++
			if i < len(fields) && hasIgnoreRuleFile([]string{fields[i]}) {
				return true, nil
			}
		}
	}
	return false, nil
}

// hasIgnoreRuleFile reports whether any path is a .gitignore file, whose
// content decides which files are ignored.
func hasIgnoreRuleFile(paths []string) bool {
	for _, path := range paths {
		if path == ".gitignore" || strings.HasSuffix(path, "/.gitignore") {
			return true
		}
	}
	return false
}

// captureChangesSnapshot is the recovery point for discarding the given paths.
// Discarding rewrites those paths (and removes an untracked directory with
// everything in it), so an ignored file at, under or above one of them is part
// of the point, and so is every ignored file when a .gitignore is among them
// (discarding its edit changes which files are ignored). When neither applies
// the ignored tree stays out of the archive; otherwise the full recovery
// contract applies, as for a merge that can overwrite ignored files.
func (s *gitSession) captureChangesSnapshot(ctx context.Context, summary string, targets []string) (WorkSnapshot, error) {
	if hasIgnoreRuleFile(targets) {
		return s.captureWorkSnapshot(ctx, RecoverySnapshotKind, summary)
	}
	ignored, err := s.ignoredWorkspacePaths()
	if err != nil {
		return WorkSnapshot{}, err
	}
	for _, path := range ignored {
		for _, target := range targets {
			if pathsOverlap(path, target) {
				return s.captureWorkSnapshot(ctx, RecoverySnapshotKind, summary)
			}
		}
	}
	return s.captureWorkSnapshotWithIgnored(ctx, RecoverySnapshotKind, summary, nil, workspaceArchiveScope{AllIgnored: false})
}

// pathsOverlap reports whether two repository paths are the same, or one lies
// inside the other (a directory and something in it).
func pathsOverlap(a, b string) bool {
	a, b = strings.TrimSuffix(a, "/"), strings.TrimSuffix(b, "/")
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
