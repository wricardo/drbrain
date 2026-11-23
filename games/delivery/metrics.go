package delivery

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/wricardo/drbrain/games/delivery/game/session"
)

// deliveryMoneyDesc is the one metric this game owns for now: the cash balance
// of a live city session. Sessions are series rather than a single city total
// because two players can run the same city and Prometheus can aggregate with
// sum by (city); collapsing here would lose that.
var deliveryMoneyDesc = prometheus.NewDesc(
	"drbrain_delivery_money",
	"Current cash balance of one live delivery session, labelled by city and session. Saved sessions that are not loaded are omitted.",
	[]string{"city", "session"},
	nil,
)

// cityMoneyCollector reads money at scrape time instead of mirroring it into a
// GaugeVec on every tick. LiveSummaries only reads sessions in memory, so a
// scrape never turns into a PostgreSQL scan of every save.
type cityMoneyCollector struct {
	live func() []session.Summary
}

func (c cityMoneyCollector) Describe(ch chan<- *prometheus.Desc) { ch <- deliveryMoneyDesc }

func (c cityMoneyCollector) Collect(ch chan<- prometheus.Metric) {
	for _, s := range c.live() {
		ch <- prometheus.MustNewConstMetric(
			deliveryMoneyDesc,
			prometheus.GaugeValue,
			float64(s.Money),
			s.CityID,
			s.ID,
		)
	}
}
