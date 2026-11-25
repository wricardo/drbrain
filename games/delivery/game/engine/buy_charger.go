package engine

import (
	"fmt"
	"strings"
)

const (
	placedChargerPrefix = "ch_p"
)

// BuyCharger buys a standalone charger and builds it on pos in one step. The
// tile must be a revealed, open road or ground tile with nothing built on it
// and no car standing on it, and a car must be able to drive there from a depot.
// Price and limit come from the city's charging config. The charger inherits the city's default rate and price.
func (w *World) BuyCharger(pos Position) (Charger, error) {
	price, limit := w.City.Charging.ChargerPurchasePrice, w.City.Charging.MaxPurchasedChargers
	if limit <= 0 {
		return Charger{}, fmt.Errorf("this city does not sell chargers")
	}
	if len(w.PlacedChargers) >= limit {
		return Charger{}, fmt.Errorf("at most %d chargers can be bought in this city", limit)
	}
	if w.City.IsHidden(pos) {
		return Charger{}, fmt.Errorf("%s is unexplored; chargers can only be placed on revealed tiles", pos)
	}
	t, ok := w.City.At(pos)
	if !ok {
		return Charger{}, fmt.Errorf("%s is off the %dx%d grid", pos, w.City.GridSize, w.City.GridSize)
	}
	if t != RoadTile && t != GroundTile {
		return Charger{}, fmt.Errorf("%s is %s; a charger needs an open road or ground tile", pos, t)
	}
	if w.City.hasEntityAt(pos) {
		return Charger{}, fmt.Errorf("%s is already occupied by a building", pos)
	}
	if w.carsAt(pos) > 0 {
		return Charger{}, fmt.Errorf("a car is standing on %s", pos)
	}
	if !w.RouteWalkable(pos) {
		return Charger{}, fmt.Errorf("%s is closed right now", pos)
	}
	if !w.reachableFromDepot(pos) {
		return Charger{}, fmt.Errorf("%s cannot be reached from a depot", pos)
	}
	if w.Ledger.Money < price {
		return Charger{}, fmt.Errorf("a charger costs $%d, you have $%d", price, w.Ledger.Money)
	}

	cfg := ChargerConfig{ID: w.nextPlacedChargerID(), Pos: pos, Capacity: 1}
	charger, err := w.installCharger(cfg)
	if err != nil {
		return Charger{}, err
	}
	w.PlacedChargers = append(w.PlacedChargers, cfg)
	w.recordCash(CashBuild, -price, nil, "", "bought charger "+cfg.ID)
	w.emit(EventBoughtCharger, nil, fmt.Sprintf("bought charger %s at %s for $%d", cfg.ID, pos, price))
	return charger, nil
}

// installCharger writes the charger tile and entity, the same two steps the
// add_charger growth effect performs.
func (w *World) installCharger(cfg ChargerConfig) (Charger, error) {
	charger, err := w.City.chargerFromConfig(cfg)
	if err != nil {
		return Charger{}, err
	}
	if err := w.City.SetTerrain(cfg.Pos, ChargerTile); err != nil {
		return Charger{}, fmt.Errorf("charger %q: %w", cfg.ID, err)
	}
	w.City.RevealTile(cfg.Pos)
	w.City.Chargers = append(w.City.Chargers, charger)
	return charger, nil
}

func (w *World) nextPlacedChargerID() string {
	for n := 1; ; n++ {
		id := fmt.Sprintf("%s%d", placedChargerPrefix, n)
		taken := false
		for _, ch := range w.City.Chargers {
			if ch.ID == id || strings.EqualFold(ch.ID, id) {
				taken = true
				break
			}
		}
		if !taken {
			return id
		}
	}
}

// reachableFromDepot reports whether a car can drive from some depot to a tile
// next to target over currently open streets.
func (w *World) reachableFromDepot(target Position) bool {
	seen := map[Position]bool{}
	var queue []Position
	for _, d := range w.City.Depots {
		seen[d.Pos] = true
		queue = append(queue, d.Pos)
	}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, dir := range AllDirections {
			n, ok := p.Neighbour(dir)
			if !ok || seen[n] {
				continue
			}
			if n == target {
				return true
			}
			if !w.RouteWalkable(n) {
				continue
			}
			seen[n] = true
			queue = append(queue, n)
		}
	}
	return false
}
