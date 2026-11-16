package engine

// Tile features are floor cells that change what happens when the robot
// arrives on them. They are encoded in the layout next to the original five
// glyphs and are completely optional: a map that uses only '.', '#', '*',
// '^', '>', 'v' and '<' behaves exactly as before.
//
//	U D L R   conveyor: after moving onto it, the robot is pushed one extra
//	          cell in that direction (never a second step). If the push is
//	          blocked by a rock, the edge or a closed door, the robot stays on
//	          the conveyor and StepEvent.PushBlocked is set.
//	/ \       mirror: on arrival the robot's facing reflects 90 degrees.
//	          '/' maps RIGHT->UP, UP->RIGHT, DOWN->LEFT, LEFT->DOWN;
//	          '\' maps RIGHT->DOWN, DOWN->RIGHT, UP->LEFT, LEFT->UP.
//	a b c     key: collected on arrival. It opens every matching door and stays
//	          collected (doors opened are permanent for the run).
//	A B C     door: impassable, like a rock, until its key is collected.
//	1 … 9     portal: exactly two tiles per digit; arriving on one teleports the
//	          robot to the other.
//
// At most one tile effect fires per step and effects never chain: a push never
// fires the tile it lands on, and a teleport does not fire the twin's tile.
// Key collection and treat collection still happen on the cell the robot
// actually occupies at the end of the effect.

// Conveyor is a one-way tile and the direction it pushes.
type Conveyor struct {
	Pos Position `json:"pos"`
	Dir Facing   `json:"dir"`
}

// Mirror is a 45-degree reflector; Slash is true for '/'.
type Mirror struct {
	Pos   Position `json:"pos"`
	Slash bool     `json:"slash"`
}

// KeyTile is a collectable key of one letter ('a'..'c').
type KeyTile struct {
	Pos    Position `json:"pos"`
	Letter string   `json:"letter"`
}

// DoorTile is a door that opens when its key is collected ('A'..'C').
type DoorTile struct {
	Pos    Position `json:"pos"`
	Letter string   `json:"letter"`
}

// Portal is one end of a pair; Pair is the shared digit ('1'..'9').
type Portal struct {
	Pos  Position `json:"pos"`
	Pair string   `json:"pair"`
}

// ConveyorAt returns the push direction of the conveyor at (x,y).
func (m *Map) ConveyorAt(x, y int) (Facing, bool) {
	if v := m.cellIndex(x, y); v >= 0 {
		if f := m.conveyorAt[v]; f != 0 {
			return Facing(f - 1), true
		}
	}
	return FacingUp, false
}

// MirrorAt reports the mirror at (x,y) and whether it is '/' (true) or '\'.
func (m *Map) MirrorAt(x, y int) (slash, ok bool) {
	if v := m.cellIndex(x, y); v >= 0 {
		switch m.mirrorAt[v] {
		case 1:
			return true, true
		case -1:
			return false, true
		}
	}
	return false, false
}

// KeyAt returns the key letter at (x,y), or 0.
func (m *Map) KeyAt(x, y int) byte {
	if v := m.cellIndex(x, y); v >= 0 {
		return m.keyAt[v]
	}
	return 0
}

// DoorAt returns the door letter at (x,y), or 0.
func (m *Map) DoorAt(x, y int) byte {
	if v := m.cellIndex(x, y); v >= 0 {
		return m.doorAt[v]
	}
	return 0
}

// PortalAt returns the portal digit at (x,y), or 0.
func (m *Map) PortalAt(x, y int) byte {
	if v := m.cellIndex(x, y); v >= 0 {
		return m.portalAt[v]
	}
	return 0
}

// PortalTwin returns the other end of the portal at (x,y).
func (m *Map) PortalTwin(x, y int) (Position, bool) {
	v := m.cellIndex(x, y)
	if v < 0 || m.portalAt[v] == 0 {
		return Position{}, false
	}
	t := m.portalTwin[v]
	if t < 0 {
		return Position{}, false
	}
	return Position{X: t % m.Width, Y: t / m.Width}, true
}

// DoorIndex returns the index of a door letter in DoorLetters, or -1.
func (m *Map) DoorIndex(letter byte) int {
	for i, l := range m.DoorLetters {
		if l == letter {
			return i
		}
	}
	return -1
}

// OpenDoorLetters returns the door letters whose doors are open, for the API.
func (m *Map) OpenDoorLetters(open []bool) []string {
	out := make([]string, 0, len(m.DoorLetters))
	for i, l := range m.DoorLetters {
		if i < len(open) && open[i] {
			out = append(out, string(l))
		}
	}
	return out
}

// cellIndex maps (x,y) to the internal arrays, or -1.
func (m *Map) cellIndex(x, y int) int {
	if x < 0 || y < 0 || x >= m.Width || y >= m.Height || len(m.conveyorAt) == 0 {
		return -1
	}
	return y*m.Width + x
}

// wall reports whether the robot cannot stand on (x,y) for a reason other than
// a door: out of bounds or a rock.
func (m *Map) wall(x, y int) bool {
	if x < 0 || y < 0 || x >= m.Width || y >= m.Height {
		return true
	}
	return m.rock[y*m.Width+x]
}

// reflectFacing turns f by 90 degrees as a mirror does.
func reflectFacing(f Facing, slash bool) Facing {
	if slash {
		switch f {
		case FacingUp:
			return FacingRight
		case FacingRight:
			return FacingUp
		case FacingDown:
			return FacingLeft
		default:
			return FacingDown
		}
	}
	switch f {
	case FacingUp:
		return FacingLeft
	case FacingLeft:
		return FacingUp
	case FacingRight:
		return FacingDown
	default:
		return FacingRight
	}
}

// glyphTile records one parsed tile glyph in NewMap.
type glyphTile struct {
	kind   byte // 'C' conveyor, 'M' mirror, 'K' key, 'D' door, 'P' portal
	dir    Facing
	slash  bool
	letter byte
}
