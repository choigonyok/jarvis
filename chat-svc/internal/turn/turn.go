// Package turn is the wire shape of the transcript.
//
// Identical to what the agent's thread store already sent, because the console
// renders these directly and the split should not be visible on screen.
package turn

type Role string

const (
	RoleAgent Role = "agent"
	RoleUser  Role = "user"
)

// Turn is one entry in the conversation.
//
// Paragraphs and Text both exist because the two speakers write differently:
// the agent answers in paragraphs, a person types one block. That was true
// before the split and is not worth changing while moving the storage.
type Turn struct {
	ID   string `json:"id"`
	Role Role   `json:"role"`
	// At is a wall clock ("15:04"), which is what the console prints.
	//
	// The row also carries a real timestamptz, and that is the one to build on:
	// the record surface currently parses the epoch out of the id to recover the
	// date, because this field has no date in it (see web/src/lib/ledger.ts).
	// Changing the field the console reads is a UI decision, so this stays and
	// the timestamp is available beside it.
	At string `json:"at"`
	// AtISO is the full timestamp, absent from the old shape and additive here.
	AtISO      string   `json:"atIso,omitempty"`
	Paragraphs []string `json:"paragraphs,omitempty"`
	Text       string   `json:"text,omitempty"`
	// ProposalID points at the proposal store, which is still a file in the
	// agent. The transcript records that a card was raised here; what the card
	// says, and what became of it, is the proposal's business.
	ProposalID string `json:"proposalId,omitempty"`
	// SessionID is the CLI session this turn came from. Kept so a restart can
	// tell which turns the model still has in its own context.
	SessionID string `json:"sessionId,omitempty"`
}
