package proposal

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/jsonfile"
)

// The live file holds the cards still waiting and those that changed lately;
// the rest are in an archive file beside it. Every change to a card rewrites
// the live file and every page of the thread carries what it holds, so
// neither may grow with every card there ever was. An archived card is still
// found by its id - the archive is read the first time one is asked for - so
// a turn of months ago still shows its card, and the record lists the
// archive beside the live cards (Archived).
//
// A card in the archive is decided: one still waiting never goes there. A
// change to an archived card - what acts on its decision settling it
// (Settle) - brings it back among the live ones, written to the live file
// before the archive is written without it, and it is archived again once it
// has been still for archiveAfter.

// archiveAfter is how long a decided card stays live after it last changed
// (Proposal.Changed): the record and the thread's pages show what moved in
// the last two weeks, and what acts on a decision finds its card live.
const archiveAfter = 14 * 24 * time.Hour

// archiveEvery is how often a running store looks for cards to archive: a
// pass rewrites the archive, so it is not done on every change.
const archiveEvery = 24 * time.Hour

// archive is the archive file's shape: the cards, oldest archived first.
type archive struct {
	Proposals []*Proposal `json:"proposals"`
}

// archivePathFor is where the live file's archive is kept: beside it, named
// after it ("proposals.json" → "proposals-archive.json").
func archivePathFor(path string) string {
	ext := filepath.Ext(path)
	return strings.TrimSuffix(path, ext) + "-archive" + ext
}

// due is whether a card has had its time in the live file: decided, and not
// changed since archiveAfter.
func (p *Proposal) due(now time.Time) bool {
	return p.State != Pending && now.Sub(p.Changed()) >= archiveAfter
}

// archiveLocked moves the cards that are due from the live list to the
// archive file, and says whether it moved any. A card stays live until the
// archive holding it is written: a pass that cannot write loses nothing.
func (s *Store) archiveLocked(now time.Time) bool {
	if s.archivePath == "" {
		return false
	}
	s.archivedAt = now
	var due []*Proposal
	for _, p := range s.order {
		if p.due(now) {
			due = append(due, p)
		}
	}
	if len(due) == 0 || !s.readArchiveLocked() {
		return false
	}
	for _, p := range due {
		if old, ok := s.archived[p.ID]; ok {
			*old = *p
			continue
		}
		s.archived[p.ID] = p
		s.archivedOrder = append(s.archivedOrder, p)
	}
	if err := jsonfile.Save(s.archivePath, archive{Proposals: s.archivedOrder}); err != nil {
		if s.log != nil {
			s.log.Error("지난 카드를 보관 파일에 옮기지 못했습니다", "err", err)
		}
		return false
	}
	gone := map[string]bool{}
	for _, p := range due {
		gone[p.ID] = true
		delete(s.byID, p.ID)
	}
	live := make([]*Proposal, 0, len(s.order)-len(due))
	for _, p := range s.order {
		if !gone[p.ID] {
			live = append(live, p)
		}
	}
	s.order = live
	if s.log != nil {
		s.log.Info("지난 카드를 보관 파일로 옮겼습니다", "cards", len(due), "live", len(s.order), "archived", len(s.archivedOrder))
	}
	return true
}

// readArchiveLocked reads the archive the first time it is needed and says
// whether it is there to use. An archive that cannot be read is said once and
// then left alone: written over, what it holds would be gone. A card the live
// file holds too is the live one - the archive's copy is what a write cut
// short left behind (saveChange) - and it leaves the archive at its next
// writing.
func (s *Store) readArchiveLocked() bool {
	if s.archived != nil {
		return true
	}
	if s.archivePath == "" || s.archiveBroken {
		return false
	}
	var saved archive
	if err := jsonfile.Load(s.archivePath, &saved); err != nil {
		s.archiveBroken = true
		if s.log != nil {
			s.log.Error("카드 보관 파일을 읽지 못했습니다 - 지난 카드를 옮기지 않습니다", "err", err)
		}
		return false
	}
	s.archived = map[string]*Proposal{}
	for _, p := range saved.Proposals {
		if p == nil {
			continue
		}
		if _, live := s.byID[p.ID]; live {
			continue
		}
		if old, ok := s.archived[p.ID]; ok {
			*old = *p
			continue
		}
		s.archived[p.ID] = p
		s.archivedOrder = append(s.archivedOrder, p)
	}
	return true
}

// lookupLocked is the card of this id, live or archived, and whether it is
// archived.
func (s *Store) lookupLocked(id string) (p *Proposal, archived, ok bool) {
	if p, ok := s.byID[id]; ok {
		return p, false, true
	}
	if !s.readArchiveLocked() {
		return nil, false, false
	}
	p, ok = s.archived[id]
	return p, ok, ok
}

// takeLocked is the card of this id, to be changed: ErrNotFound when there is
// none, or why may refuses it. An archived card comes back among the live
// ones (unarchived), where the change is saved (saveChange); one refused
// stays where it is.
func (s *Store) takeLocked(id string, may func(*Proposal) error) (p *Proposal, unarchived bool, err error) {
	p, archived, ok := s.lookupLocked(id)
	if !ok {
		return nil, false, ErrNotFound
	}
	if may != nil {
		if err := may(p); err != nil {
			return nil, false, err
		}
	}
	if archived {
		s.reviveLocked(p)
	}
	return p, archived, nil
}

// waiting refuses a card already decided: only a waiting card is decided,
// edited, redrawn or abandoned.
func waiting(p *Proposal) error {
	if p.State != Pending {
		return ErrAlreadyDecided
	}
	return nil
}

// reviveLocked puts an archived card back among the live ones, where it was
// raised. The archive keeps it until the live file holding it is written
// (dropArchivedLocked): a write cut short loses nothing.
func (s *Store) reviveLocked(p *Proposal) {
	at := len(s.order)
	for at > 0 && s.order[at-1].Opened().After(p.Opened()) {
		at--
	}
	s.order = slices.Insert(s.order, at, p)
	s.byID[p.ID] = p
}

// dropArchivedLocked writes the archive without a card brought back live.
// One that cannot be written keeps a copy of the card there, which the live
// one hides.
func (s *Store) dropArchivedLocked(id string) {
	delete(s.archived, id)
	s.archivedOrder = slices.DeleteFunc(s.archivedOrder, func(p *Proposal) bool { return p.ID == id })
	if err := jsonfile.Save(s.archivePath, archive{Proposals: s.archivedOrder}); err != nil && s.log != nil {
		s.log.Error("보관 파일에서 다시 꺼낸 카드를 지우지 못했습니다", "card", id, "err", err)
	}
}

// ErrArchiveUnreadable is an archive file that could not be read: its cards
// are not listed, nor found by their ids.
var ErrArchiveUnreadable = errors.New("proposal archive unreadable")

// Archived is every card the archive holds, oldest archived first: those
// List no longer has, which the record lists beside the live ones.
func (s *Store) Archived() ([]Proposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.archivePath == "" {
		return []Proposal{}, nil
	}
	if !s.readArchiveLocked() {
		return nil, ErrArchiveUnreadable
	}
	out := make([]Proposal, 0, len(s.archivedOrder))
	for _, p := range s.archivedOrder {
		if _, live := s.byID[p.ID]; !live {
			out = append(out, *p)
		}
	}
	return out, nil
}

// ListWith is List, and before it the cards of these ids that are no longer
// live: those the turns on a page of the thread point at, however old the
// turns are.
func (s *Store) ListWith(ids []string) []Proposal {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Proposal, 0, len(s.order))
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if p, archived, ok := s.lookupLocked(id); ok && archived {
			out = append(out, *p)
		}
	}
	for _, p := range s.order {
		out = append(out, *p)
	}
	return out
}

// Find is the first card match accepts: of those live, oldest first, then of
// the archive's.
func (s *Store) Find(match func(Proposal) bool) (Proposal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.order {
		if match(*p) {
			return *p, true
		}
	}
	if !s.readArchiveLocked() {
		return Proposal{}, false
	}
	for _, p := range s.archivedOrder {
		if match(*p) {
			return *p, true
		}
	}
	return Proposal{}, false
}
