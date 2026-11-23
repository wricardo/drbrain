package delivery

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/wricardo/drbrain/games/delivery/game/session"
)

func TestCityMoneyCollectorExportsOneSeriesPerLiveSession(t *testing.T) {
	live := []session.Summary{
		{ID: "alpha", CityID: "city", Money: 125},
		{ID: "beta", CityID: "saopaulo", Money: 40},
		{ID: "gamma", CityID: "city", Money: 7},
	}
	reg := prometheus.NewRegistry()
	reg.MustRegister(cityMoneyCollector{live: func() []session.Summary { return live }})

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var found bool
	for _, f := range families {
		if f.GetName() != "drbrain_delivery_money" {
			continue
		}
		found = true
		if got := len(f.GetMetric()); got != len(live) {
			t.Fatalf("series = %d, want %d", got, len(live))
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			want := map[string]struct {
				city  string
				money float64
			}{
				"alpha": {city: "city", money: 125},
				"beta":  {city: "saopaulo", money: 40},
				"gamma": {city: "city", money: 7},
			}[labels["session"]]
			if labels["city"] != want.city {
				t.Errorf("session %s city = %q, want %q", labels["session"], labels["city"], want.city)
			}
			if got := m.GetGauge().GetValue(); got != want.money {
				t.Errorf("session %s money = %v, want %v", labels["session"], got, want.money)
			}
		}
	}
	if !found {
		t.Fatal("drbrain_delivery_money was not gathered")
	}
}
