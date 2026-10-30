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
// the brokerage did not answer.
func (m *Module) Look(b *tool.Board) {
	tool.Add(b, "portfolio", "투자 자산 현황: 총자산, 현금, 보유 종목별 평가·손익, 원금 대비 수익, "+
		"1일/1개월/1년 변동, 계좌별(업비트·한국투자증권·금현물) 원금 대비, 종목별 매도 실현손익(다 판 종목 포함)과 "+
		"올해 양도세 추정(해외주식·코인 각각 250만원 공제, 22%). 최대 30초 전 값.",
		func(ctx context.Context, _ PortfolioInput) (string, error) {
			p, err := m.store.Portfolio(ctx)
			if err != nil {
				return "", err
			}
			return renderPortfolio(p), nil
		})
	tool.Add(b, "allocation", "목표 비중(유망주45/메이저코인30/현금(달러)12/배당주8/금5) 대비 현재 비중, 허용 범위 이탈 여부, "+
		"목표로 돌아가려면 어느 자산군에서 어느 자산군으로 얼마를 옮겨야 하는지.",
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
