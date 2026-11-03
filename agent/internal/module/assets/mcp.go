package assets

import (
	"context"

	"github.com/choigonyok/jarvis/agent/internal/tool"
)

type PortfolioInput struct{}

type HistoryInput struct {
	Days int `json:"days,omitempty" jsonschema:"최근 며칠치를 볼지. 1~3650, 비우면 90."`
}

// Look puts the money on the board. All of it only reads: a card in front of
// "how is my portfolio" would only train the operator to approve without
// looking. A failed lookup is a failure said, not a retry: the model should say
// The tools' words name what they answer and leave the numbers to the answer:
// the targets, their bands and the tax are assets-svc's, and a copy here goes
// stale (the target labels changed under it).
const (
	portfolioDesc = "투자 자산 현황: 총자산, 현금, 보유 종목별 평가·손익, 원금 대비 수익, " +
		"1일/1개월/1년 변동, 계좌별(업비트·한국투자증권·금현물) 원금 대비, 종목별 매도 실현손익(다 판 종목 포함)과 " +
		"올해 양도세 추정(공제와 세율은 결과에 적힌 대로). 최대 30초 전 값."
	allocationDesc = "목표 비중 대비 현재 비중, 허용 범위 이탈 여부(목표와 범위는 결과에 적힌 대로), " +
		"목표로 돌아가려면 어느 자산군에서 어느 자산군으로 얼마를 옮겨야 하는지."
)

// the brokerage did not answer.
func (m *Module) Look(b *tool.Board) {
	tool.Add(b, "portfolio", portfolioDesc,
		func(ctx context.Context, _ PortfolioInput) (string, error) {
			p, err := m.store.Portfolio(ctx)
			if err != nil {
				return "", err
			}
			return renderPortfolio(p), nil
		})
	tool.Add(b, "allocation", allocationDesc,
		func(ctx context.Context, _ PortfolioInput) (string, error) {
			p, err := m.store.Portfolio(ctx)
			if err != nil {
				return "", err
			}
			return renderAllocation(p), nil
		})
	tool.Add(b, "asset_history", "기록된 일별 총자산 스냅샷(그날 실제로 들고 있던 것 기준). 입출금이 섞여 있어 수익률이 아니다. "+
		"수익률은 portfolio 의 기간 변동이나 원금 대비를 쓴다.",
		func(ctx context.Context, in HistoryInput) (string, error) {
			days := in.Days
			if days <= 0 {
				days = 90
			}
			if days > 3650 {
				days = 3650
			}
			rows, err := m.store.History(ctx, days)
			if err != nil {
				return "", err
			}
			return renderHistory(rows), nil
		})
}
