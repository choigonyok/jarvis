// Package listing holds the rules of a Joongna listing that do not need a
// database: what a new listing must have, which changes a listing in a given
// state may take, and where each report moves it.
package listing

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Statuses. queued and failed exist only here; the other four mirror Joongna.
const (
	Queued   = "queued"
	Active   = "active"
	Reserved = "reserved"
	Sold     = "sold"
	Deleted  = "deleted"
	Failed   = "failed"
)

// Task kinds.
const (
	KindPost   = "post"
	KindPrice  = "price"
	KindStatus = "status"
	KindBump   = "bump"
	KindDelete = "delete"
)

// Report outcomes.
const (
	OutcomeDone          = "done"
	OutcomeFailed        = "failed"
	OutcomeLoginRequired = "login_required"
)

// MaxAttempts is how many times one task is tried before it is given up.
const MaxAttempts = 3

// RetryAfter is how long a failed attempt waits before the next.
const RetryAfter = 15 * time.Minute

// PhotoPattern is the shape of an upload name the agent hands out. Anything
// else would be a path, and a path is not this service's to accept.
var PhotoPattern = regexp.MustCompile(`^[0-9a-f]{32}\.(jpg|png|webp)$`)

type New struct {
	Title       string   `json:"title"`
	PriceKrw    int64    `json:"priceKrw"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Condition   string   `json:"condition"`
	Shipping    string   `json:"shipping"`
	Photos      []string `json:"photos"`
}

// Normalize trims and validates a new listing in place.
func (n *New) Normalize() error {
	n.Title = strings.TrimSpace(n.Title)
	n.Description = strings.TrimSpace(n.Description)
	n.Category = strings.TrimSpace(n.Category)
	n.Condition = strings.TrimSpace(n.Condition)
	if n.Shipping == "" {
		n.Shipping = "included"
	}
	switch {
	case n.Title == "":
		return errors.New("제목이 비어 있습니다.")
	case len([]rune(n.Title)) > 40:
		return errors.New("제목은 40자까지입니다.")
	case n.PriceKrw <= 0:
		return errors.New("가격을 적어 주세요.")
	case n.PriceKrw > 100_000_000:
		return errors.New("가격이 너무 큽니다.")
	case n.Shipping != "included" && n.Shipping != "separate":
		return errors.New("택배비는 included 또는 separate 입니다.")
	case len(n.Photos) == 0:
		return errors.New("사진이 한 장은 있어야 합니다.")
	case len(n.Photos) > 12:
		return errors.New("사진은 12장까지입니다.")
	}
	for _, p := range n.Photos {
		if !PhotoPattern.MatchString(p) {
			return fmt.Errorf("사진 이름이 올바르지 않습니다: %s", p)
		}
	}
	return nil
}

// TaskInput is a change asked for from the tab.
type TaskInput struct {
	Kind     string `json:"kind"`
	PriceKrw int64  `json:"priceKrw,omitempty"`
	ToStatus string `json:"toStatus,omitempty"`
}

// CheckTask says whether a listing in status may take this change.
func CheckTask(status string, posted bool, in TaskInput) error {
	if in.Kind == KindPost {
		if status != Failed {
			return errors.New("등록은 등록하지 못한 글만 다시 시도할 수 있습니다.")
		}
		return nil
	}
	if !posted {
		return errors.New("아직 중고나라에 올라가지 않은 글입니다.")
	}
	if status == Deleted {
		return errors.New("이미 지운 글입니다.")
	}
	switch in.Kind {
	case KindPrice:
		if status == Sold {
			return errors.New("판매완료된 글의 가격은 바꿀 수 없습니다.")
		}
		if in.PriceKrw <= 0 || in.PriceKrw > 100_000_000 {
			return errors.New("새 가격을 적어 주세요.")
		}
	case KindStatus:
		switch in.ToStatus {
		case Active, Reserved, Sold:
		default:
			return errors.New("상태는 active, reserved, sold 중 하나입니다.")
		}
		if in.ToStatus == status {
			return errors.New("이미 그 상태입니다.")
		}
	case KindBump:
		if status != Active {
			return errors.New("끌어올리기는 판매중인 글만 됩니다.")
		}
	case KindDelete:
	default:
		return fmt.Errorf("모르는 작업입니다: %s", in.Kind)
	}
	return nil
}

// FromJoongna maps the status words Joongna shows to ours. Unknown words map
// to "" and are ignored by the sync.
func FromJoongna(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), " ", "")
	switch {
	case s == Active || s == Reserved || s == Sold || s == Deleted:
		return s
	case strings.Contains(s, "예약"):
		return Reserved
	case strings.Contains(s, "완료"):
		return Sold
	case strings.Contains(s, "판매중"), strings.Contains(s, "판매"):
		return Active
	}
	return ""
}
