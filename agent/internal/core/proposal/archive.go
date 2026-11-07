package proposal

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/jsonfile"
)

// The live file holds the cards still waiting and those decided lately; the
// rest are in an archive file beside it. Every change to a card rewrites the
// live file and every page of the thread carries what it holds, so neither
// may grow with every card there ever was. An archived card is still found
// by its id - the archive is read the first time one is asked for - so a turn
// of months ago still shows its card.

// archiveAfter is how long a decided card stays live, from when it was
// raised: the record and the thread's pages show the last two weeks.
const archiveAfter = 14 * 24 * time.Hour

// archiveEvery is how often a running store looks for cards to archive: a
// pass rewrites the archive, so it is not done on every change.
const archiveEvery = 24 * time.Hour

// settledFor is how long a card changed in this process stays live, however
// old: what acts on a decision settles the card after it (Settle), and must
// find it.
const settledFor = time.Hour

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

// dueLocked is whether a card has had its time in the live file.
func (s *Store) dueLocked(p *Proposal, now time.Time) bool {
	if p.State == Pending || now.Sub(p.Opened()) < archiveAfter {
		return false
	}
	at, changed := s.changed[p.ID]
	return !changed || now.Sub(at) >= settledFor
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
		if s.dueLocked(p, now) {
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
		delete(s.changed, p.ID)
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
// then left alone: written over, what it holds would be gone.
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
