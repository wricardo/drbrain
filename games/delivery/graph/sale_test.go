package graph

import "testing"

// TestSellCarMutationRejectsTheLastVehicle covers the resolver wiring on the
// refusal path: the mutation is reachable and reports the engine's conflict
// instead of selling the only car. The success path (car removal, 70% payout,
// charging settlement) is covered by the engine's sale tests.
func TestSellCarMutationRejectsTheLastVehicle(t *testing.T) {
	ts := newTestServer(t)

	var created struct {
		CreateSession struct {
			Session struct {
				ID   string
				Cars []struct{ ID string }
			}
			OwnerToken string
		}
	}
	mustQuery(t, ts, `mutation { createSession(seed: 3) { session { id cars { id } } ownerToken } }`, nil, &created)
	cars := created.CreateSession.Session.Cars
	if len(cars) != 1 {
		t.Fatalf("starter fleet = %d cars, want 1", len(cars))
	}
	sessionID := created.CreateSession.Session.ID
	tok := created.CreateSession.OwnerToken

	res := query(t, ts, `mutation { sellCar(id: "`+sessionID+`", carId: "`+cars[0].ID+`", ownerToken: "`+tok+`") { cars { id } } }`, nil)
	if code := cityErrCode(res); code != codeConflict {
		t.Fatalf("selling the last car code = %q, want %s", code, codeConflict)
	}
}
