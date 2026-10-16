package interpret

import (
	"context"
	"slices"
	"strings"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// What a fact must clear before it goes to the ledger, past the evidence
// check: the model words the same fact many ways, takes a pet name for a
// marriage, and reads "또 담타..." said to the operator as the speaker's habit.
// These are the mistakes it made reading two thousand messages of a couple's
// chat; each check here is one of them.

// relationAlias folds the names the model makes up for one relation into
// the one the rest of mind reads.
var relationAlias = map[string]string{
	"IS_SPOUSE_OF": "MARRIED_TO", "SPOUSE_OF": "MARRIED_TO", "IS_MARRIED_TO": "MARRIED_TO", "MARRIED": "MARRIED_TO",
	"HUSBAND_OF": "MARRIED_TO", "WIFE_OF": "MARRIED_TO",
	"IS_IN_RELATIONSHIP_WITH": "PARTNER_OF", "IN_RELATIONSHIP_WITH": "PARTNER_OF", "IS_PARTNER_OF": "PARTNER_OF",
	"DATING": "PARTNER_OF", "DATES": "PARTNER_OF", "GIRLFRIEND_OF": "PARTNER_OF", "BOYFRIEND_OF": "PARTNER_OF",
	"COUPLE_WITH": "PARTNER_OF", "IS_COUPLE_WITH": "PARTNER_OF",
	"HAS_CHILD": "PARENT_OF", "IS_PARENT_OF": "PARENT_OF",
	"IS_NON_SMOKER": "DOES_NOT_SMOKE", "NON_SMOKER": "DOES_NOT_SMOKE", "DOESNT_SMOKE": "DOES_NOT_SMOKE",
	"IS_SMOKER":     "SMOKES",
	"DOES_NOT_LIKE": "DISLIKES", "HATES": "DISLIKES",
}

// spellings is a relation and the other names it was stored under before
// they were folded into it.
func spellings(relation string) []string {
	out := []string{relation}
	for alias, to := range relationAlias {
		if to == relation {
			out = append(out, alias)
		}
	}
	return out
}

// symmetric relations are one fact whichever end is the subject.
var symmetric = []string{"PARTNER_OF", "MARRIED_TO", "FRIEND_OF", "FAMILY_OF", "SIBLING_OF"}

// opposite is the relation that says the reverse about the same object.
var opposite = map[string]string{
	"LIKES": "DISLIKES", "DISLIKES": "LIKES",
	"SMOKES": "DOES_NOT_SMOKE", "DOES_NOT_SMOKE": "SMOKES",
	"DRINKS": "DOES_NOT_DRINK", "DOES_NOT_DRINK": "DRINKS",
	"DRIVES": "DOES_NOT_DRIVE", "DOES_NOT_DRIVE": "DRIVES",
}

// recurring relations are events: the same one again is another time it
// happened, not a repeat.
var recurring = []string{"VISITED", "BOUGHT", "GAVE_TO", "RECEIVED_FROM", "DECIDED", "INVESTED_BECAUSE", "EXPIRES_ON", "HAS_HEALTH_ISSUE"}

// weighty relations change who someone is to others. They are not guessed
// from a pet name ("여보", "우리 애"): only the person's own clear words, said
// with confidence, make one.
var weighty = []string{"MARRIED_TO", "PARENT_OF", "CHILD_OF", "DIVORCED_FROM", "PREGNANT", "DIED"}

// unnamedKin is a child or spouse with no name: "은주에게 아이가 있다" from
// "우리 애를 어떻게 데리고 가지?" said of a doll.
var unnamedKin = []string{"아이", "자녀", "아기", "애기", "아들", "딸", "남편", "아내", "와이프", "배우자"}

const weightyConfidence = 0.8

// isWeighty is a fact that needs its subject's own words to stand.
func isWeighty(relation, object string) bool {
	return slices.Contains(weighty, relation) || relation == "FAMILY_OF" && slices.Contains(unnamedKin, strings.TrimSpace(object))
}

// ends is a fact's two ends and relation as compared: names normalised, the
// operator as "나", a symmetric relation's ends in order.
type ends struct{ subject, relation, object string }

func (c *Conversation) endsOf(subject, relation, object string) ends {
	name := func(n string) string {
		n = strings.TrimSuffix(strings.TrimSpace(n), "(나)")
		if c.party(n, "").Self {
			return "나"
		}
		return norm(n)
	}
	e := ends{name(subject), relation, name(object)}
	if slices.Contains(symmetric, relation) && e.object < e.subject {
		e.subject, e.object = e.object, e.subject
	}
	return e
}

// knownLike is the facts already held with these ends or their opposite: in the
// ledger (any room, any time) and in what memory showed for this read.
func (c *Conversation) knownLike(ctx context.Context, e ends, remembered []Memory) (same bool, against []model.Fact, err error) {
	rels := spellings(e.relation)
	if o := opposite[e.relation]; o != "" {
		rels = append(rels, spellings(o)...)
	}
	ledger, err := c.st.FactsWith(ctx, rels...)
	if err != nil {
		return false, nil, err
	}
	for _, f := range ledger {
		k := c.endsOf(f.Subject.Name, relationName(f.Relation), f.Object.Name)
		if k.subject != e.subject || k.object != e.object {
			continue
		}
		if k.relation == e.relation {
			same = true
		} else {
			against = append(against, f)
		}
	}
	for _, m := range remembered {
		if m.Relation == "" {
			continue
		}
		k := c.endsOf(m.Subject, relationName(m.Relation), m.Object)
		if k == e {
			same = true
		}
	}
	return same, against, nil
}

// saidBy is whether any evidence line was said by this person (the operator
// for "나").
func saidBy(evidence []model.Evidence, fresh []model.Message, subject string, self bool) bool {
	by := map[string]model.Message{}
	for _, m := range fresh {
		by[m.Ref] = m
	}
	return slices.ContainsFunc(evidence, func(e model.Evidence) bool {
		m, ok := by[e.Ref]
		if !ok {
			return false
		}
		if self {
			return m.IsMine
		}
		return !m.IsMine && norm(m.Sender) == norm(subject)
	})
}
