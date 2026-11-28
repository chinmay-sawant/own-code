package agentreview

import (
	"fmt"
	"sort"
	"sync"
)

// State is a hunk's review status.
type State int

const (
	Pending State = iota
	Accepted
	Rejected
)

func (s State) String() string { return [...]string{"pending", "accepted", "rejected"}[s] }

// FileReview is one session's proposal for one file.
type FileReview struct {
	Session  string
	Path     string
	Base     string
	Proposed string
	Hunks    []Hunk
	States   []State
	// Kind is Delete or Rename for a whole-file unit (no Hunks, no per-hunk
	// states); Whole then holds its single review state.
	Kind        Kind
	RenamedFrom string
	Whole       State
}

// Registry holds hunk state per session and file, in memory.
type Registry struct {
	mu    sync.Mutex
	files map[string]map[string]*FileReview
}

func NewRegistry() *Registry { return &Registry{files: map[string]map[string]*FileReview{}} }

// Put records (replacing) a session's proposal for a path; every hunk starts pending.
func (r *Registry) Put(session, path, base, proposed string) *FileReview {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.files[session] == nil {
		r.files[session] = map[string]*FileReview{}
	}
	hs := Diff(base, proposed)
	fr := &FileReview{Session: session, Path: path, Base: base, Proposed: proposed, Hunks: hs, States: make([]State, len(hs))}
	r.files[session][path] = fr
	return fr
}

// PutWhole records a whole-file delete or rename as one pending reviewable
// unit with no hunks. path is the destination for a rename.
func (r *Registry) PutWhole(session, path string, kind Kind, renamedFrom string) (*FileReview, error) {
	if !kind.Whole() {
		return nil, fmt.Errorf("agentreview: %s is not a whole-file change", kind)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.files[session] == nil {
		r.files[session] = map[string]*FileReview{}
	}
	fr := &FileReview{Session: session, Path: path, Kind: kind, RenamedFrom: renamedFrom}
	r.files[session][path] = fr
	return fr, nil
}

// RestoreStates replaces the states of a review after verifying that they
// describe the current hunk set. It is intended for restoring persisted local
// review state; it never applies or reverts file contents.
func (r *Registry) RestoreStates(session, path string, states map[string]State) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	fr := r.files[session][path]
	if fr == nil || fr.Kind.Whole() {
		return fmt.Errorf("agentreview: no hunk review for %s", path)
	}
	if len(states) != len(fr.Hunks) {
		return fmt.Errorf("agentreview: persisted hunk set does not match %s", path)
	}
	restored := make([]State, len(fr.Hunks))
	for i, h := range fr.Hunks {
		state, ok := states[h.ID]
		if !ok || state < Pending || state > Rejected {
			return fmt.Errorf("agentreview: invalid persisted state for hunk %s", h.ID)
		}
		restored[i] = state
	}
	copy(fr.States, restored)
	return nil
}

// RestoreSession installs reviews that were reconstructed and validated by a
// background loader. It clones the prepared state and replaces the session in
// one operation; it does not read files or compute diffs.
func (r *Registry) RestoreSession(session string, reviews []*FileReview) error {
	if session == "" {
		return fmt.Errorf("agentreview: empty session")
	}
	prepared := make(map[string]*FileReview, len(reviews))
	for _, review := range reviews {
		if review == nil || review.Session != session || review.Path == "" {
			return fmt.Errorf("agentreview: invalid restored review")
		}
		if _, exists := prepared[review.Path]; exists {
			return fmt.Errorf("agentreview: duplicate restored path %s", review.Path)
		}
		if review.Kind.Whole() {
			if len(review.Hunks) != 0 || len(review.States) != 0 || (review.Kind != Delete && review.Kind != Rename) {
				return fmt.Errorf("agentreview: invalid whole-file review for %s", review.Path)
			}
		} else {
			if len(review.States) != len(review.Hunks) {
				return fmt.Errorf("agentreview: invalid restored hunk states for %s", review.Path)
			}
			for _, state := range review.States {
				if state < Pending || state > Rejected {
					return fmt.Errorf("agentreview: invalid restored hunk state for %s", review.Path)
				}
			}
		}
		copyReview := *review
		copyReview.Hunks = append([]Hunk(nil), review.Hunks...)
		copyReview.States = append([]State(nil), review.States...)
		prepared[review.Path] = &copyReview
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.files[session] = prepared
	return nil
}

// ResolveWhole marks a whole-file unit Accepted or Rejected while it is
// pending. It never touches any file.
func (r *Registry) ResolveWhole(session, path string, st State) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	fr := r.files[session][path]
	if fr == nil || !fr.Kind.Whole() {
		return fmt.Errorf("agentreview: no whole-file proposal for %s", path)
	}
	if fr.Whole != Pending || st == Pending {
		return fmt.Errorf("agentreview: proposal is %s", fr.Whole)
	}
	fr.Whole = st
	return nil
}

// Get returns the review for session and path.
func (r *Registry) Get(session, path string) *FileReview {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.files[session][path]
}

// Paths lists a session's reviewed paths in order.
func (r *Registry) Paths(session string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for p := range r.files[session] {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Drop forgets a session (on cancel or archive); no file is touched.
func (r *Registry) Drop(session string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.files, session)
}

func (r *Registry) index(session, path, id string) (*FileReview, int, error) {
	fr := r.files[session][path]
	if fr == nil {
		return nil, 0, fmt.Errorf("agentreview: no proposal for %s in session %s", path, session)
	}
	for i, h := range fr.Hunks {
		if h.ID == id {
			return fr, i, nil
		}
	}
	return nil, 0, fmt.Errorf("agentreview: unknown hunk %s", id)
}

// Accept applies the hunk to current and returns the new text. On conflict the
// state is unchanged and current is untouched.
func (r *Registry) Accept(session, path, id, current string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fr, i, err := r.index(session, path, id)
	if err != nil {
		return current, err
	}
	if fr.States[i] != Pending {
		return current, fmt.Errorf("agentreview: hunk is %s", fr.States[i])
	}
	out, err := Apply(current, fr.Hunks[i])
	if err != nil {
		return current, err
	}
	fr.States[i] = Accepted
	return out, nil
}

// Reject marks the hunk rejected. It never changes any text.
func (r *Registry) Reject(session, path, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	fr, i, err := r.index(session, path, id)
	if err != nil {
		return err
	}
	if fr.States[i] != Pending {
		return fmt.Errorf("agentreview: hunk is %s", fr.States[i])
	}
	fr.States[i] = Rejected
	return nil
}

// Revert undoes an accepted hunk in current and returns it to pending.
func (r *Registry) Revert(session, path, id, current string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fr, i, err := r.index(session, path, id)
	if err != nil {
		return current, err
	}
	if fr.States[i] != Accepted {
		return current, fmt.Errorf("agentreview: hunk is %s, not accepted", fr.States[i])
	}
	out, err := Revert(current, fr.Hunks[i])
	if err != nil {
		return current, err
	}
	fr.States[i] = Pending
	return out, nil
}

// Counts returns pending, accepted and rejected hunk totals.
func (fr *FileReview) Counts() (pending, accepted, rejected int) {
	for _, s := range fr.States {
		switch s {
		case Pending:
			pending++
		case Accepted:
			accepted++
		default:
			rejected++
		}
	}
	return
}

// Forget drops one file's review for a session.
func (r *Registry) Forget(session, path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.files[session], path)
}
