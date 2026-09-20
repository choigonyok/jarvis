package proposal

import "github.com/choigonyok/jarvis/agent/internal/core/action"

// Route is what should happen to a proposal a detector raised on its own.
type Route string

const (
	RouteDrop Route = "drop" // 확신이 너무 낮다. 기록만 하고 사람을 부르지 않는다.
	RouteAsk  Route = "ask"  // 카드로 올린다.
	RouteAuto Route = "auto" // 실행하고 알린다.
)

// Gate turns a confidence score into a route. Thresholds live in one struct
// so tuning them is a config change, not a hunt through call sites.
type Gate struct {
	Ask  float64 // 이 아래로는 조용히 기록만
	Auto float64 // 이 위로는 자동 실행
}

func DefaultGate() Gate { return Gate{Ask: 0.3, Auto: 0.85} }

// Route decides what to do with a proposal a detector raised on its own.
// Anything the operator asked for in words skips this entirely and goes
// straight to a card - the person is right there, and this product's promise
// is that state changes are seen before they happen.
//
// The one rule that confidence cannot buy its way past:
// an irreversible action always gets a card. A model that is 99% sure it
// should delete something is still a model that might be wrong, and there is
// no undo to fall back on.
func (g Gate) Route(confidence float64, spec action.Spec) Route {
	switch {
	case confidence < g.Ask:
		return RouteDrop
	case confidence >= g.Auto && spec.Reversible:
		return RouteAuto
	default:
		return RouteAsk
	}
}
