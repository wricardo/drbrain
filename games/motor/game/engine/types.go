package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Facing is a compass direction: 0=up, 1=right, 2=down, 3=left.
type Facing int

const (
	FacingUp Facing = iota
	FacingRight
	FacingDown
	FacingLeft
)

var dirs = [4][2]int{{0, -1}, {1, 0}, {0, 1}, {-1, 0}}

// Position is a grid coordinate.
type Position struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Instruction is one tape slot value.
type Instruction string

const (
	Empty       Instruction = "EMPTY"
	TurnLeft    Instruction = "TURN_LEFT"
	TurnRight   Instruction = "TURN_RIGHT"
	MoveForward Instruction = "MOVE_FORWARD"
	CallSub1    Instruction = "CALL_SUB_1"
	CallSub2    Instruction = "CALL_SUB_2"
	CallSub3    Instruction = "CALL_SUB_3"
	CallSub4    Instruction = "CALL_SUB_4"
	CallSub5    Instruction = "CALL_SUB_5"
	CallSub6    Instruction = "CALL_SUB_6"
	CallSub7    Instruction = "CALL_SUB_7"
	CallSub8    Instruction = "CALL_SUB_8"
	CallSub9    Instruction = "CALL_SUB_9"
	CallSub10   Instruction = "CALL_SUB_10"
)

// SubIndex returns the 0-based sub index for CALL_SUB_k instructions.
func (i Instruction) SubIndex() (int, bool) {
	const prefix = "CALL_SUB_"
	s := string(i)
	if !strings.HasPrefix(s, prefix) {
		return 0, false
	}
	rest := s[len(prefix):]
	// Canonical form only: decimal 1..MaxSubs (no leading zeros, no signs, no
	// digits beyond what MaxSubs needs).
	if len(rest) == 0 || len(rest) > 2 || rest[0] == '0' {
		return 0, false
	}
	n := 0
	for _, c := range rest {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	if n < 1 || n > MaxSubs {
		return 0, false
	}
	return n - 1, true
}

// Valid reports whether i is a known instruction.
func (i Instruction) Valid() bool {
	switch i {
	case Empty, TurnLeft, TurnRight, MoveForward:
		return true
	}
	_, ok := i.SubIndex()
	return ok
}

// Program is a main tape plus N sub tapes.
type Program struct {
	Main []Instruction   `json:"main"`
	Subs [][]Instruction `json:"subs"`
}

// Clone deep-copies the program.
func (p Program) Clone() Program {
	out := Program{Main: append([]Instruction(nil), p.Main...), Subs: make([][]Instruction, len(p.Subs))}
	for i, s := range p.Subs {
		out.Subs[i] = append([]Instruction(nil), s...)
	}
	return out
}

// Frame is a call stack entry. Tape -1 is main. PC is the index of the next
// instruction to run on that tape.
type Frame struct {
	Tape int `json:"tape"`
	PC   int `json:"pc"`
}

// Status is the VM lifecycle status.
type Status string

const (
	StatusReady   Status = "READY"
	StatusRunning Status = "RUNNING"
	StatusWon     Status = "WON"
	StatusLost    Status = "LOST"
)

// LossReason explains a LOST status.
type LossReason string

const (
	LossNone         LossReason = ""
	LossStepLimit    LossReason = "STEP_LIMIT"
	LossCallDepth    LossReason = "CALL_DEPTH"
	LossProgramEnded LossReason = "PROGRAM_ENDED"
)

// Terminal reports whether the status is WON or LOST.
func (s Status) Terminal() bool { return s == StatusWon || s == StatusLost }

// Limits and defaults.
const (
	MinSide             = 3
	MaxSide             = 32
	MaxMainTapeLength   = 32
	MaxSubTapeLength    = 16
	MaxSubs             = 10
	MaxMaxSteps         = 10000
	MaxMaxCallDepth     = 64
	DefaultMainTape     = 10
	DefaultSubTape      = 10
	DefaultMaxSteps     = 200
	DefaultMaxCallDepth = 16
)

var idRe = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

// ValidID reports whether id is a legal map id (also a safe filename).
func ValidID(id string) bool { return idRe.MatchString(id) }

// MapStatus says whether a community map is public. Shipped and legacy maps
// are published.
type MapStatus string

const (
	MapStatusDraft     MapStatus = "DRAFT"
	MapStatusPublished MapStatus = "PUBLISHED"
)

// MapConfig is the JSON representation of a map (maps/*.json). OwnerID and
// Status are community metadata: shipped maps leave them empty, and a stored
// community map always carries both.
type MapConfig struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Description    string    `json:"description,omitempty"`
	Difficulty     string    `json:"difficulty,omitempty"`
	Layout         []string  `json:"layout"`
	MainTapeLength int       `json:"main_tape_length,omitempty"`
	SubTapeLengths []int     `json:"sub_tape_lengths"` // nil = default [10]; [] = no subs (never omitted)
	MaxSteps       int       `json:"max_steps,omitempty"`
	MaxCallDepth   int       `json:"max_call_depth,omitempty"`
	AllowRecursion bool      `json:"allow_recursion"`
	OwnerID        string    `json:"owner_id,omitempty"`
	Status         MapStatus `json:"status,omitempty"`
}

// Map is a parsed, validated map.
type Map struct {
	ID            string
	Name          string
	Description   string
	Difficulty    string
	Layout        []string
	Width, Height int
	Start         Position
	StartFacing   Facing
	Rocks         []Position
	Treats        []Position
	Conveyors     []Conveyor
	Mirrors       []Mirror
	Keys          []KeyTile
	Doors         []DoorTile
	Portals       []Portal
	// DoorLetters lists the door letters used by the map, in sorted order.
	// VMState.DoorsOpen is indexed by position in this slice.
	DoorLetters    []byte
	MainTapeLength int
	SubTapeLengths []int
	MaxSteps       int
	MaxCallDepth   int
	AllowRecursion bool

	rock       []bool // Width*Height
	treatIdx   []int  // Width*Height -> index in Treats or -1
	conveyorAt []int8 // Width*Height -> facing+1, 0 = none
	mirrorAt   []int8 // Width*Height -> 1 '/', -1 '\', 0 = none
	keyAt      []byte // Width*Height -> key letter, 0 = none
	doorAt     []byte // Width*Height -> door letter, 0 = none
	portalAt   []byte // Width*Height -> portal digit, 0 = none
	portalTwin []int  // Width*Height -> index in Portals, -1 = none
}

// Config returns the JSON form of the map (all fields explicit).
func (m *Map) Config() MapConfig {
	return MapConfig{
		ID: m.ID, Name: m.Name, Description: m.Description, Difficulty: m.Difficulty,
		Layout:         append([]string(nil), m.Layout...),
		MainTapeLength: m.MainTapeLength, SubTapeLengths: append([]int{}, m.SubTapeLengths...),
		MaxSteps: m.MaxSteps, MaxCallDepth: m.MaxCallDepth, AllowRecursion: m.AllowRecursion,
	}
}

// Clone deep-copies the map.
func (m *Map) Clone() *Map {
	m2, err := NewMap(m.Config())
	if err != nil {
		// A *Map can only exist if NewMap succeeded; cannot fail.
		panic(err)
	}
	return m2
}

// IsRock reports whether (x,y) is a rock. Out of bounds counts as blocked.
func (m *Map) IsRock(x, y int) bool {
	if x < 0 || y < 0 || x >= m.Width || y >= m.Height {
		return true
	}
	return m.rock[y*m.Width+x]
}

// NewMap validates cfg (applying defaults) and builds a Map. All structural
// problems are returned together as a *ValidationError.
func NewMap(cfg MapConfig) (*Map, error) {
	var issues []string
	add := func(f string, a ...any) { issues = append(issues, fmt.Sprintf(f, a...)) }

	if !ValidID(cfg.ID) {
		add("id %q must match %s", cfg.ID, idRe.String())
	}
	if strings.TrimSpace(cfg.Name) == "" {
		add("name is required")
	}
	m := &Map{
		ID: cfg.ID, Name: cfg.Name, Description: cfg.Description, Difficulty: cfg.Difficulty,
		Layout: append([]string(nil), cfg.Layout...), AllowRecursion: cfg.AllowRecursion,
		MainTapeLength: cfg.MainTapeLength, MaxSteps: cfg.MaxSteps, MaxCallDepth: cfg.MaxCallDepth,
	}
	if m.Difficulty == "" {
		m.Difficulty = "easy"
	}
	if m.MainTapeLength == 0 {
		m.MainTapeLength = DefaultMainTape
	}
	if m.MaxSteps == 0 {
		m.MaxSteps = DefaultMaxSteps
	}
	if m.MaxCallDepth == 0 {
		m.MaxCallDepth = DefaultMaxCallDepth
	}
	if cfg.SubTapeLengths == nil {
		m.SubTapeLengths = []int{DefaultSubTape}
	} else {
		m.SubTapeLengths = append([]int{}, cfg.SubTapeLengths...)
	}

	if m.MainTapeLength < 1 || m.MainTapeLength > MaxMainTapeLength {
		add("main_tape_length must be 1..%d", MaxMainTapeLength)
	}
	if len(m.SubTapeLengths) > MaxSubs {
		add("at most %d subs allowed", MaxSubs)
	}
	for i, l := range m.SubTapeLengths {
		if l < 1 || l > MaxSubTapeLength {
			add("sub_tape_lengths[%d] must be 1..%d", i, MaxSubTapeLength)
		}
	}
	if m.MaxSteps < 1 || m.MaxSteps > MaxMaxSteps {
		add("max_steps must be 1..%d", MaxMaxSteps)
	}
	if m.MaxCallDepth < 1 || m.MaxCallDepth > MaxMaxCallDepth {
		add("max_call_depth must be 1..%d", MaxMaxCallDepth)
	}

	m.Height = len(m.Layout)
	if m.Height < MinSide || m.Height > MaxSide {
		add("layout must have %d..%d rows", MinSide, MaxSide)
	}
	if m.Height > 0 {
		m.Width = utf8.RuneCountInString(m.Layout[0])
	}
	if m.Width < MinSide || m.Width > MaxSide {
		add("layout rows must be %d..%d wide", MinSide, MaxSide)
	}
	starts := 0
	if m.Height >= MinSide && m.Height <= MaxSide && m.Width >= MinSide && m.Width <= MaxSide {
		m.rock = make([]bool, m.Width*m.Height)
		m.treatIdx = make([]int, m.Width*m.Height)
		m.conveyorAt = make([]int8, m.Width*m.Height)
		m.mirrorAt = make([]int8, m.Width*m.Height)
		m.keyAt = make([]byte, m.Width*m.Height)
		m.doorAt = make([]byte, m.Width*m.Height)
		m.portalAt = make([]byte, m.Width*m.Height)
		m.portalTwin = make([]int, m.Width*m.Height)
		for i := range m.treatIdx {
			m.treatIdx[i] = -1
			m.portalTwin[i] = -1
		}
		portalCells := map[byte][]Position{}
		for y, row := range m.Layout {
			if w := utf8.RuneCountInString(row); w != m.Width {
				add("layout row %d has width %d, want %d", y, w, m.Width)
				continue
			}
			rs := []rune(row)
			for x, c := range rs {
				i := y*m.Width + x
				switch c {
				case '.':
				case '#':
					m.rock[i] = true
					m.Rocks = append(m.Rocks, Position{x, y})
				case '*':
					m.treatIdx[i] = len(m.Treats)
					m.Treats = append(m.Treats, Position{x, y})
				case '^', '>', 'v', '<':
					starts++
					m.Start = Position{x, y}
					m.StartFacing = Facing(strings.IndexRune("^>v<", c))
				case 'U', 'D', 'L', 'R':
					f := Facing(strings.IndexRune("URDL", c))
					m.conveyorAt[i] = int8(f) + 1
					m.Conveyors = append(m.Conveyors, Conveyor{Pos: Position{x, y}, Dir: f})
				case '/':
					m.mirrorAt[i] = 1
					m.Mirrors = append(m.Mirrors, Mirror{Pos: Position{x, y}, Slash: true})
				case '\\':
					m.mirrorAt[i] = -1
					m.Mirrors = append(m.Mirrors, Mirror{Pos: Position{x, y}, Slash: false})
				case 'a', 'b', 'c':
					m.keyAt[i] = byte(c)
					m.Keys = append(m.Keys, KeyTile{Pos: Position{x, y}, Letter: string(c)})
				case 'A', 'B', 'C':
					m.doorAt[i] = byte(c - 'A' + 'a')
					m.Doors = append(m.Doors, DoorTile{Pos: Position{x, y}, Letter: string(c)})
				case '1', '2', '3', '4', '5', '6', '7', '8', '9':
					m.portalAt[i] = byte(c)
					m.Portals = append(m.Portals, Portal{Pos: Position{x, y}, Pair: string(c)})
					portalCells[byte(c)] = append(portalCells[byte(c)], Position{x, y})
				default:
					add("layout (%d,%d): unknown glyph %q", x, y, c)
				}
			}
		}
		for digit, cells := range portalCells {
			if len(cells) != 2 {
				add("portal %q appears %d times, want exactly 2", string(digit), len(cells))
				continue
			}
			a := cells[0].Y*m.Width + cells[0].X
			b := cells[1].Y*m.Width + cells[1].X
			m.portalTwin[a] = b
			m.portalTwin[b] = a
		}
		keyLetters := map[byte]bool{}
		for _, k := range m.Keys {
			keyLetters[k.Letter[0]] = true
		}
		doorLetters := map[byte]bool{}
		for _, d := range m.Doors {
			lower := d.Letter[0] - 'A' + 'a'
			doorLetters[lower] = true
			if !keyLetters[lower] {
				add("door %q has no matching key %q, so it can never open", d.Letter, string(lower))
			}
		}
		for _, k := range m.Keys {
			if !doorLetters[k.Letter[0]] {
				add("key %q has no matching door %q", k.Letter, string(k.Letter[0]-'a'+'A'))
			}
		}
		for l := byte('a'); l <= 'c'; l++ {
			if doorLetters[l] {
				m.DoorLetters = append(m.DoorLetters, l)
			}
		}
	}
	if starts != 1 {
		add("layout must contain exactly one start glyph (^ > v <), found %d", starts)
	}
	if len(m.Treats) == 0 {
		add("layout must contain at least one treat (*)")
	}
	if len(issues) > 0 {
		return nil, &ValidationError{Issues: issues}
	}
	return m, nil
}

// ValidationError aggregates structural problems.
type ValidationError struct{ Issues []string }

func (e *ValidationError) Error() string { return strings.Join(e.Issues, "; ") }

// VMState is the mutable execution state of one run.
type VMState struct {
	Pos        Position   `json:"pos"`
	Facing     Facing     `json:"facing"`
	Collected  []bool     `json:"collected"` // indexed by Map.Treats
	TreatsLeft int        `json:"treats_left"`
	CallStack  []Frame    `json:"call_stack"` // [0] is main; top is active
	Steps      int        `json:"steps"`
	Status     Status     `json:"status"`
	LossReason LossReason `json:"loss_reason,omitempty"`
	Visited    []Position `json:"visited"`
	// DoorsOpen is indexed by Map.DoorLetters: true once that door's key has
	// been collected. Older session files omit it; a missing entry reads as
	// closed.
	DoorsOpen []bool `json:"doors_open,omitempty"`
}

// Clone deep-copies the state.
func (s VMState) Clone() VMState {
	s.Collected = append([]bool(nil), s.Collected...)
	s.CallStack = append([]Frame(nil), s.CallStack...)
	s.Visited = append([]Position(nil), s.Visited...)
	s.DoorsOpen = append([]bool(nil), s.DoorsOpen...)
	return s
}

// TreatsRemaining returns positions of treats not yet collected.
func (s *VMState) TreatsRemaining(m *Map) []Position {
	out := make([]Position, 0, s.TreatsLeft)
	for i, t := range m.Treats {
		if !s.Collected[i] {
			out = append(out, t)
		}
	}
	return out
}

// StepEvent describes one counted step.
type StepEvent struct {
	Step         int         `json:"step"`
	Instruction  Instruction `json:"instruction"`
	TapeIndex    int         `json:"tape_index"` // -1 = main
	SlotIndex    int         `json:"slot_index"`
	From         Position    `json:"from"`
	To           Position    `json:"to"`
	FacingBefore Facing      `json:"facing_before"`
	FacingAfter  Facing      `json:"facing_after"`
	Blocked      bool        `json:"blocked"`
	// CallBlocked is true when the step was a CALL that recursion rules
	// ignored: it still spent a step but pushed no frame.
	CallBlocked bool `json:"call_blocked"`
	// Pushed is true when the robot moved onto a conveyor and was carried one
	// extra cell; PushBlocked is true when that carry was blocked, so the robot
	// stayed on the conveyor.
	Pushed      bool `json:"pushed"`
	PushBlocked bool `json:"push_blocked"`
	// Mirrored is true when the arrival tile reflected the robot's facing.
	Mirrored bool `json:"mirrored"`
	// CollectedKey is the letter of a key picked up on this step, or "".
	CollectedKey string `json:"collected_key,omitempty"`
	// Teleported is the portal destination when a portal fired.
	Teleported *Position  `json:"teleported,omitempty"`
	Collected  *Position  `json:"collected,omitempty"`
	CallStack  []Frame    `json:"call_stack"`
	Status     Status     `json:"status"`
	LossReason LossReason `json:"loss_reason,omitempty"`
}

// SlotsUsed counts the non-EMPTY slots across the main tape and every sub
// tape. It is the "size" of a program: the game is about fitting a route into
// few slots.
func (p Program) SlotsUsed() int {
	n := 0
	count := func(tape []Instruction) {
		for _, in := range tape {
			if in != Empty {
				n++
			}
		}
	}
	count(p.Main)
	for _, s := range p.Subs {
		count(s)
	}
	return n
}

// Fingerprint identifies the rules of a map: layout, tape lengths, step and
// depth limits and the recursion rule. Name, description and difficulty are
// not part of it, so cosmetic edits keep it stable while any change that could
// make the puzzle easier or harder changes it. Results recorded on one
// fingerprint are comparable with each other and with no other.
func (m *Map) Fingerprint() string {
	h := sha256.New()
	for _, row := range m.Layout {
		fmt.Fprintf(h, "%s\n", row)
	}
	fmt.Fprintf(h, "main=%d subs=%v steps=%d depth=%d rec=%t", m.MainTapeLength, m.SubTapeLengths, m.MaxSteps, m.MaxCallDepth, m.AllowRecursion)
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// TotalSlots is the number of slots on the main tape and all sub tapes: the
// most a program can use.
func (m *Map) TotalSlots() int {
	n := m.MainTapeLength
	for _, l := range m.SubTapeLengths {
		n += l
	}
	return n
}
