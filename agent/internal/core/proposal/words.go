package proposal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
)

// Writer words a card before anyone sees it. Given the card as the code that
// raised it wrote it, it returns the card as it should read - that same card
// when it has nothing better. The card waits on it, so it must give up soon.
type Writer interface {
	Write(ctx context.Context, p Proposal) action.Card
}

// Digest names a card by what the code that raised it put on it, whoever
// worded it after: the same card asked again has the same digest.
func Digest(c action.Card) string {
	c.Writer, c.Draft = "", ""
	raw, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

// keepWords is a card revised by the operator's edits as it reads: the
// module's card for the edited action, in the words it was already in. An
// edit changes values, not how the card puts them.
func keepWords(was, now action.Card) action.Card {
	if was.Writer == "" {
		return now
	}
	now.Title, now.Body, now.Consequence = was.Title, was.Body, was.Consequence
	now.Choices, now.Writer, now.Draft = was.Choices, was.Writer, was.Draft
	worded := map[string]action.Field{}
	for _, f := range was.Fields {
		worded[f.Key] = f
	}
	fields := append([]action.Field(nil), now.Fields...)
	for i, f := range fields {
		if w, ok := worded[f.Key]; ok {
			fields[i].Label, fields[i].Hint = w.Label, w.Hint
		}
	}
	now.Fields = fields
	return now
}

// SetWriter has w word the cards raised from now on. Set before serving.
func (s *Store) SetWriter(w Writer) { s.writer = w }

// word is the card as the writer words it, with the digest of the code's card
// it was worded from. No writer: the card as it came.
func (s *Store) word(p Proposal) action.Card {
	if s.writer == nil {
		return p.Card
	}
	draft := Digest(p.Card)
	c := s.writer.Write(context.Background(), p)
	c.Draft = draft
	return c
}
