package engine

import (
	"encoding/json"
	"fmt"
)

// applyEffects applies one growth stage's effects in order to this city. It
// mutates the receiver, so callers that need atomicity must run it on a Clone
// and only commit the clone if it succeeds (see World.applyGrowth).
func (c *City) applyEffects(effects []GrowthEffect) error {
	for i, e := range effects {
		if err := c.applyEffect(e); err != nil {
			return fmt.Errorf("effect %d (%s): %w", i, e.Type, err)
		}
	}
	return nil
}

// applyEffect applies one effect. Every case is deterministic and touches only
// the grid, hidden flags and entity lists.
func (c *City) applyEffect(e GrowthEffect) error {
	switch e.Type {
	case EffectBuildBridge, EffectPaveRoad:
		// Both are "write these tiles to this terrain". The distinction is
		// documentation for the player and for validation, not behaviour.
		if len(e.Tiles) == 0 {
			return fmt.Errorf("no tiles to write")
		}
		for _, t := range e.Tiles {
			ct := CellType(t.Terrain)
			if !ct.known() {
				return fmt.Errorf("unknown terrain %q at (%d,%d)", t.Terrain, t.X, t.Y)
			}
			if err := c.SetTerrain(Position{t.X, t.Y}, ct); err != nil {
				return err
			}
			// A built tile is, by definition, known to the player.
			c.RevealTile(Position{t.X, t.Y})
		}
		return nil

	case EffectRevealDistrict:
		d, ok := c.Districts[e.District]
		if !ok {
			return fmt.Errorf("no district named %q", e.District)
		}
		for _, r := range d.Rects {
			c.RevealRect(r[0], r[1], r[2], r[3])
		}
		return nil

	case EffectAddKitchen:
		for _, raw := range e.Entities {
			var k KitchenConfig
			if err := json.Unmarshal(raw, &k); err != nil {
				return fmt.Errorf("bad kitchen: %w", err)
			}
			c.Kitchens = append(c.Kitchens, Kitchen{
				ID: k.ID, Name: k.Name, Pos: k.Pos, Menu: append([]string(nil), k.Menu...),
			})
		}
		return nil

	case EffectAddCustomer:
		for _, raw := range e.Entities {
			var cu CustomerConfig
			if err := json.Unmarshal(raw, &cu); err != nil {
				return fmt.Errorf("bad customer: %w", err)
			}
			c.Customers = append(c.Customers, Customer{
				ID: cu.ID, Name: cu.Name, Pos: cu.Pos,
				Demands:            append([]string(nil), cu.Demands...),
				OrderIntervalTicks: cu.OrderIntervalTicks,
				JitterTicks:        cu.JitterTicks,
			})
		}
		return nil

	case EffectAddCharger:
		for _, raw := range e.Entities {
			var ch ChargerConfig
			if err := json.Unmarshal(raw, &ch); err != nil {
				return fmt.Errorf("bad charger: %w", err)
			}
			// The entity list is display/routing metadata; charging is decided by
			// the terrain (City.ChargingAt reads the cell type). A build that only
			// appended to Chargers left its "C" tile as plain road, so a player
			// parked there never recovered a single unit of battery.
			if err := c.SetTerrain(ch.Pos, ChargerTile); err != nil {
				return fmt.Errorf("charger %q: %w", ch.ID, err)
			}
			c.RevealTile(ch.Pos)
			charger, err := c.chargerFromConfig(ch)
			if err != nil {
				return err
			}
			c.Chargers = append(c.Chargers, charger)
		}
		return nil

	case EffectAddDepot:
		for _, raw := range e.Entities {
			var d DepotConfig
			if err := json.Unmarshal(raw, &d); err != nil {
				return fmt.Errorf("bad depot: %w", err)
			}
			// Same rule as a charger: DepotTile terrain is what parks and
			// recharges cars, so the effect has to write it.
			if err := c.SetTerrain(d.Pos, DepotTile); err != nil {
				return fmt.Errorf("depot %q: %w", d.ID, err)
			}
			c.RevealTile(d.Pos)
			c.Depots = append(c.Depots, Depot{
				ID: d.ID, Pos: d.Pos, Capacity: d.Capacity, ChargeSlots: d.ChargeSlots,
			})
		}
		return nil

	case EffectUpgradeDepot:
		for i := range c.Depots {
			if c.Depots[i].ID != e.DepotID {
				continue
			}
			if e.Capacity > 0 {
				c.Depots[i].Capacity = e.Capacity
			}
			if e.Charge > 0 {
				c.Depots[i].ChargeSlots = e.Charge
			}
			return nil
		}
		return fmt.Errorf("no depot with id %q", e.DepotID)

	case EffectGrantCarClass, EffectGrantMoney, EffectGrantRep:
		// These do not touch the grid: they unlock economy options or pay out.
		// They are carried here so a stage's effect list stays a single
		// ordered script; the world applies the economic half.
		return nil
	}

	return fmt.Errorf("unknown effect type %q", e.Type)
}

// effectCities are the effects that mutate the grid or the entity lists; the
// grant effects are applied by World.applyGrowth.
