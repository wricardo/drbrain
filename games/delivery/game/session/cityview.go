package session

import "github.com/wricardo/drbrain/games/delivery/game/engine"

// CityOnlyView describes the static city with every district revealed, for a
// client that wants to show the world before a session exists (a map preview,
// a docs page, or an agent deciding whether to play).
//
// It mutates the city it is handed by revealing everything. Callers pass a
// freshly loaded city, never a live session's, because revealing a live city
// would delete that session's fog.
func CityOnlyView(city *engine.City) CityView {
	city.RevealAll()
	v := CityView{
		Name:      city.Name,
		GridSize:  city.GridSize,
		Rows:      city.RenderRows(nil),
		Hidden:    0,
		Kitchens:  []PointView{},
		Customers: []PointView{},
		Depots:    []PointView{},
		Chargers:  []PointView{},
	}
	for _, k := range city.Kitchens {
		v.Kitchens = append(v.Kitchens, PointView{ID: k.ID, Name: k.Name, X: k.Pos.X, Y: k.Pos.Y})
	}
	for _, c := range city.Customers {
		v.Customers = append(v.Customers, PointView{ID: c.ID, Name: c.Name, X: c.Pos.X, Y: c.Pos.Y})
	}
	for _, d := range city.Depots {
		v.Depots = append(v.Depots, PointView{ID: d.ID, X: d.Pos.X, Y: d.Pos.Y})
	}
	for _, ch := range city.Chargers {
		v.Chargers = append(v.Chargers, PointView{ID: ch.ID, X: ch.Pos.X, Y: ch.Pos.Y})
	}
	return v
}
