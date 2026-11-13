package uniple

// The space read from the partner's side. uniple holds the operator's session
// only, so the guest's console reads the same rows; flipped, "me" is the
// guest and "partner" the operator, the way their own app would say it.

func flipWho(w string) string {
	switch w {
	case "me":
		return "partner"
	case "partner":
		return "me"
	}
	return w
}

// Flipped is the space as the partner sees it.
func (s Space) Flipped() Space {
	if s.Partner != nil {
		me, partner := *s.Partner, s.Me
		s.Me, s.Partner = me, &partner
	}
	blocks := make([]Block, len(s.Blocks))
	for i, b := range s.Blocks {
		blocks[i] = b.Flipped()
	}
	s.Blocks = blocks
	return s
}

// Flipped is a block as the partner sees it: whose each item is.
func (b Block) Flipped() Block {
	b.Free = flipEach(b.Free, func(f *Free) { f.By = flipWho(f.By) })
	b.Album = flipEach(b.Album, func(p *AlbumPhoto) { p.By = flipWho(p.By) })
	b.Log = flipEach(b.Log, func(e *LogEntry) { e.By = flipWho(e.By) })
	b.Food = flipEach(b.Food, func(e *FoodEntry) { e.By = flipWho(e.By) })
	b.Diary = flipEach(b.Diary, func(e *DiaryEntry) {
		e.By = flipWho(e.By)
		e.Replies = flipEach(e.Replies, func(r *DiaryReply) { r.By = flipWho(r.By) })
	})
	b.Budget = flipEach(b.Budget, func(d *BudgetDate) {
		d.Expenses = flipEach(d.Expenses, func(x *Expense) { x.PaidBy = flipWho(x.PaidBy) })
	})
	return b
}

// FlippedChecks are a habit's days as the partner sees them.
func FlippedChecks(cs []Check) []Check {
	return flipEach(cs, func(c *Check) { c.Who = flipWho(c.Who) })
}

// FlippedNews is what the two did, as the partner sees it.
func FlippedNews(ns []News) []News {
	return flipEach(ns, func(n *News) { n.Who = flipWho(n.Who) })
}

// flipEach is a copy of list with each item turned; the read stays as it was.
func flipEach[T any](list []T, turn func(*T)) []T {
	if list == nil {
		return nil
	}
	out := make([]T, len(list))
	copy(out, list)
	for i := range out {
		turn(&out[i])
	}
	return out
}
