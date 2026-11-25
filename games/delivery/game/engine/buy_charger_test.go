package engine

import "testing"

func TestBuyChargerPlacesChargesAndSurvivesRestore(t *testing.T) {
	w := paidChargerWorld(t, 2, 1, 10)
	w.Cars[0].Pos = Position{1, 1}
	w.Ledger.Money = 500
	w.City.Charging.ChargerPurchasePrice, w.City.Charging.MaxPurchasedChargers = 150, 2

	for name, p := range map[string]Position{
		"water":    {0, 0},
		"depot":    {1, 1},
		"charger":  {2, 2},
		"off grid": {9, 9},
	} {
		if _, err := w.BuyCharger(p); err == nil {
			t.Errorf("%s tile %v accepted", name, p)
		}
	}
	if w.Ledger.Money != 500 {
		t.Fatalf("rejected purchases spent money: %d", w.Ledger.Money)
	}

	ch, err := w.BuyCharger(Position{3, 3})
	if err != nil {
		t.Fatalf("buy: %v", err)
	}
	if ch.ID != "ch_p1" || !w.City.ChargingAt(Position{3, 3}) {
		t.Errorf("charger %+v, charging=%v", ch, w.City.ChargingAt(Position{3, 3}))
	}
	if w.Ledger.Money != 350 {
		t.Errorf("money = %d", w.Ledger.Money)
	}

	if _, err := w.BuyCharger(Position{3, 2}); err != nil {
		t.Fatalf("second buy: %v", err)
	}
	if _, err := w.BuyCharger(Position{1, 3}); err == nil {
		t.Error("bought past max_purchased_chargers")
	}
	w.Ledger.Money = 500

	restored := paidChargerWorld(t, 2, 1, 10)
	if err := restored.ImportState(w.ExportState()); err != nil {
		t.Fatalf("import: %v", err)
	}
	if !restored.City.ChargingAt(Position{3, 3}) {
		t.Error("placed charger lost on restore")
	}
}

func TestBuyChargerNeedsCash(t *testing.T) {
	w := paidChargerWorld(t, 2, 1, 10)
	w.City.Charging.ChargerPurchasePrice, w.City.Charging.MaxPurchasedChargers = 150, 2
	w.Ledger.Money = 149
	if _, err := w.BuyCharger(Position{3, 3}); err == nil {
		t.Fatal("bought a charger without the money")
	}
	if w.City.ChargingAt(Position{3, 3}) {
		t.Error("tile changed on a failed purchase")
	}
}

func TestBuyChargerDisabledByDefault(t *testing.T) {
	w := paidChargerWorld(t, 2, 1, 10)
	w.Ledger.Money = 500
	if _, err := w.BuyCharger(Position{3, 3}); err == nil {
		t.Fatal("bought a charger on a city with max_purchased_chargers 0")
	}
}
