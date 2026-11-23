// Command dac renders the Perses dashboard for drbrain's observability stack.
//
// It is a standalone Go module so the Perses SDK never enters the drbrain
// server build. Run it through `just perses-build`; the generated JSON is
// applied with `just perses-apply`.
package main

import (
	"flag"

	"github.com/perses/perses/go-sdk"
	"github.com/perses/perses/go-sdk/dashboard"
	"github.com/perses/perses/go-sdk/panel"
	panelgroup "github.com/perses/perses/go-sdk/panel-group"
	"github.com/perses/plugins/prometheus/sdk/go/query"
	stat "github.com/perses/plugins/statchart/sdk/go"
	timeseries "github.com/perses/plugins/timeserieschart/sdk/go"
)

const project = "drbrain"

func main() {
	flag.Parse()
	exec := sdk.NewExec()

	builder, buildErr := dashboard.New("drbrain-delivery",
		dashboard.ProjectName(project),
		dashboard.Name("DrBrain Delivery"),
		dashboard.Description("Live delivery cash and host health from Prometheus."),
		dashboard.DurationAsString("1h"),
		dashboard.RefreshIntervalAsString("30s"),

		dashboard.AddPanelGroup("Cash",
			panelgroup.PanelsPerLine(2),
			panelgroup.Collapsed(false),
			panelgroup.PanelHeight(4),
			panelgroup.AddPanel("Live delivery cash",
				stat.Chart(),
				panel.AddQuery(query.PromQL("sum(drbrain_delivery_money)")),
			),
			panelgroup.AddPanel("Live sessions",
				stat.Chart(),
				panel.AddQuery(query.PromQL("count(drbrain_delivery_money)")),
			),
		),

		dashboard.AddPanelGroup("Delivery money",
			panelgroup.PanelsPerLine(2),
			panelgroup.Collapsed(false),
			panelgroup.AddPanel("Cash per city",
				timeseries.Chart(
					timeseries.WithVisual(timeseries.Visual{Display: timeseries.LineDisplay}),
				),
				panel.AddQuery(query.PromQL("sum by (city) (drbrain_delivery_money)")),
			),
			panelgroup.AddPanel("Cash per session",
				timeseries.Chart(
					timeseries.WithVisual(timeseries.Visual{Display: timeseries.LineDisplay}),
				),
				panel.AddQuery(query.PromQL("drbrain_delivery_money")),
			),
		),

		dashboard.AddPanelGroup("Host",
			panelgroup.PanelsPerLine(2),
			panelgroup.Collapsed(false),
			panelgroup.AddPanel("Go goroutines",
				timeseries.Chart(
					timeseries.WithVisual(timeseries.Visual{Display: timeseries.LineDisplay}),
				),
				panel.AddQuery(query.PromQL("go_goroutines")),
			),
			panelgroup.AddPanel("Process CPU",
				timeseries.Chart(
					timeseries.WithVisual(timeseries.Visual{Display: timeseries.LineDisplay}),
				),
				panel.AddQuery(query.PromQL("rate(process_cpu_seconds_total[1m])")),
			),
		),
	)
	exec.BuildDashboard(builder, buildErr)
}
