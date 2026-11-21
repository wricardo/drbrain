package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const usageText = `musicimport converts a melody to the drbrain music score JSON format.

Every score is written at PPQ 480 (a quarter note is 480 ticks) with a
two-space indent and a trailing newline, so importing the same input twice
produces identical bytes.

Usage:
  musicimport -in FILE [-out FILE] [-id ID] [-title TITLE] [-composer NAME]
              [-tempo BPM] [-key KEY] [-clef treble] [-transpose SEMITONES]

Input:
  A file ending in .musicxml or .xml, or one starting with "<", is read as
  MusicXML (score-partwise). It must contain one <part>, one voice, a treble
  clef and a 4/4, 3/4, 2/4, 6/8, 9/8 or 12/8 time signature. Chords are
  allowed. Ties that stay inside a measure are merged; a tie across a
  barline is rejected with the measure number.

  Anything else is read as text notation. The first token is the time
  signature; the remaining tokens are separated by whitespace:

    PITCH:DUR         a note, e.g. D4:480, F#4:480, Bb3:480
    P1+P2+...:DUR     a chord: all notes share one start
    r:DUR             a rest (advances time, emits no note)
    |                 a barline

  DUR is in ticks at PPQ 480. Measure lengths are 4/4 = 1920, 3/4 = 1440,
  2/4 = 960, 6/8 = 2880, 9/8 = 4320 and 12/8 = 5760 ticks. Lines starting
  with '#' are comments. Example:

    4/4 D4:480 D4:480 F#4:480 A4:480 | A4:480 G4:480 F#4:480 E4:480 |

Output:
  Scores must keep every pitch inside [59,83] (B3..B5) and every note inside
  its measure; violations are reported with the measure number. The output
  file's base name must equal the piece id.

Flags:
`

type meta struct {
	id        string
	title     string
	composer  string
	key       string
	clef      string
	tempo     int
	transpose int
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "musicimport:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("musicimport", flag.ContinueOnError)
	fs.SetOutput(stderr)
	in := fs.String("in", "", "input file (\"-\" reads stdin)")
	out := fs.String("out", "", "output score JSON file (empty or \"-\" writes stdout)")
	id := fs.String("id", "", "piece id; defaults to the output (or input) base name")
	title := fs.String("title", "", "piece title")
	composer := fs.String("composer", "", "composer")
	tempo := fs.Int("tempo", 100, "tempo in beats per minute")
	key := fs.String("key", "", "key name such as C, G, D, F, Am, Em")
	clef := fs.String("clef", "treble", "clef (only treble is supported)")
	transpose := fs.Int("transpose", 0, "semitones added to every pitch")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usageText)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if *in == "" {
		fs.Usage()
		return errors.New("-in is required")
	}

	src, err := readInput(*in)
	if err != nil {
		return err
	}
	name := *in
	pieceID := *id
	if pieceID == "" {
		base := *out
		if base == "" || base == "-" {
			base = name
		}
		if base == "-" {
			return errors.New("cannot derive -id from stdin; pass -id")
		}
		pieceID = strings.ToLower(strings.TrimSuffix(filepath.Base(base), filepath.Ext(base)))
	}
	if *out != "" && *out != "-" {
		base := strings.TrimSuffix(filepath.Base(*out), filepath.Ext(*out))
		if base != pieceID {
			return fmt.Errorf("output file base name %q must equal id %q", base, pieceID)
		}
	}

	encoded, err := convert(src, name, meta{
		id:        pieceID,
		title:     *title,
		composer:  *composer,
		key:       *key,
		clef:      *clef,
		tempo:     *tempo,
		transpose: *transpose,
	})
	if err != nil {
		return err
	}
	if *out == "" || *out == "-" {
		_, err = stdout.Write(encoded)
		return err
	}
	return os.WriteFile(*out, encoded, 0o644)
}

func readInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read input: %w", err)
	}
	return data, nil
}

// convert parses either notation, applies metadata and transposition, and
// returns the deterministic JSON bytes.
func convert(src []byte, name string, m meta) ([]byte, error) {
	var (
		parsed *source
		err    error
	)
	if isMusicXML(src, name) {
		parsed, err = parseMusicXML(src)
	} else {
		parsed, err = parseText(src)
	}
	if err != nil {
		return nil, err
	}

	clef := m.clef
	if clef == "" {
		clef = "treble"
	}
	key := m.key
	if key == "" {
		key = parsed.key
	}
	if key == "" {
		key = "C"
	}
	p := &Piece{
		Version:  scoreVersion,
		ID:       m.id,
		Title:    m.title,
		Composer: m.composer,
		TempoBPM: m.tempo,
		TimeSig:  parsed.timeSig,
		Key:      key,
		Clef:     clef,
		Measures: parsed.measures,
	}
	if m.transpose != 0 {
		for mi := range p.Measures {
			for ni := range p.Measures[mi].Notes {
				p.Measures[mi].Notes[ni].Pitch += m.transpose
			}
		}
	}
	return p.Encode()
}

// isMusicXML reports whether the input should go through the XML parser.
func isMusicXML(src []byte, name string) bool {
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".xml") || strings.HasSuffix(lower, ".musicxml") {
		return true
	}
	return bytes.HasPrefix(bytes.TrimSpace(src), []byte("<"))
}
