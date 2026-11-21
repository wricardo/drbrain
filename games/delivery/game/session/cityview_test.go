package session

import (
	"strings"
	"testing"
)

// TestCityOnlyViewHasNoCustomers pins the static preview's semantics: it is a
// terrain blueprint, so houses render 'h' and no customer is listed. Anything
// else would advertise customers that are not on any board.
func TestCityOnlyViewHasNoCustomers(t *testing.T) {
	city, _ := testCity(t)
	v := CityOnlyView(city)

	if len(v.Customers) != 0 {
		t.Fatalf("static city lists %d customers, want none", len(v.Customers))
	}
	joined := strings.Join(v.Rows, "")
	if strings.Contains(joined, "H") {
		t.Fatal("static city renders a waiting customer ('H'); it has no session state")
	}
	if !strings.Contains(joined, "h") {
		t.Fatal("static city renders no houses ('h')")
	}
	if v.Hidden != 0 {
		t.Fatalf("static city hidden tiles = %d, want 0 (revealed preview)", v.Hidden)
	}
}
