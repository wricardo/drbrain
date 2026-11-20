// Package merge rebuilds the unified Dr Brain GraphQL schema from the derived
// per-game fragments.
//
// graph/schema.graphqls is the canonical, hand-edited schema. cmd/split-schema
// derives games/<g>/graph/schema.graphqls from it so each game's gqlgen package
// keeps generating its local models and resolver interfaces. This package is
// the inverse check: merge(split(canonical)) must reproduce the canonical file
// byte-for-byte, which is what catches a per-game schema edited directly.
//
// The merged shape is:
//
//   - every game-defined type is prefixed with the game name, except the shared
//     Time scalar;
//   - Query and Mutation are namespaced (delivery, mazes, motorProgramming,
//     tesla);
//   - Subscription stays flat, because GraphQL only streams at the root.
package merge

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
	"github.com/vektah/gqlparser/v2/parser"
)

// CodeHeader marks the canonical schema. cmd/split-schema derives each game's
// SDL from it; cmd/merge-schema verifies the round trip.
const CodeHeader = "# Dr Brain unified GraphQL schema. Source of truth: edit this file, then run\n# `go generate ./games/... ./graph`.\n\n"

// Game describes one schema fragment and where its generated packages live.
type Game struct {
	// Name is the GraphQL type prefix, e.g. "Delivery".
	Name string
	// Namespace is the root field, e.g. "delivery".
	Namespace string
	// Dir is the module-relative game directory, e.g. "games/delivery".
	Dir string
	// ModelPkg is the module-relative Go package holding the generated models.
	ModelPkg string
	// ResolverPkg is the module-relative Go package holding *graph.Resolver.
	ResolverPkg string
}

// Games is the fixed merge order. The first entry is the global account
// namespace: it is not a game, but it participates in the same prefix and
// namespace machinery so its SDL, models and resolvers are generated with
// everything else.
var Games = []Game{
	{Name: "Global", Namespace: "account", Dir: "platform/account", ModelPkg: "platform/account/graph/model", ResolverPkg: "platform/account/graph"},
	{Name: "Delivery", Namespace: "delivery", Dir: "games/delivery", ModelPkg: "games/delivery/graph/model", ResolverPkg: "games/delivery/graph"},
	{Name: "Mazes", Namespace: "mazes", Dir: "games/mazes", ModelPkg: "games/mazes/graph/model", ResolverPkg: "games/mazes/graph"},
	{Name: "Motor", Namespace: "motorProgramming", Dir: "games/motor", ModelPkg: "games/motor/graph/model", ResolverPkg: "games/motor/graph"},
	{Name: "Tesla", Namespace: "tesla", Dir: "games/tesla", ModelPkg: "games/tesla/graph/model", ResolverPkg: "games/tesla/graph"},
}

// SchemaPath is the module-relative path of the generated SDL.
const SchemaPath = "graph/schema.graphqls"

type parsedGame struct {
	game Game
	defs ast.DefinitionList
}

// Build returns the merged SDL, header included.
func Build() ([]byte, error) {
	root, err := RepoRoot()
	if err != nil {
		return nil, err
	}
	return BuildFrom(root)
}

// BuildFrom merges the schemas under root.
func BuildFrom(root string) ([]byte, error) {
	parsed := make([]parsedGame, 0, len(Games))
	for _, g := range Games {
		path := filepath.Join(root, g.Dir, "graph", "schema.graphqls")
		defs, err := parse(path)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, parsedGame{game: g, defs: defs})
	}

	merged := &ast.SchemaDocument{}
	timeSeen := false
	for _, pg := range parsed {
		for _, d := range pg.defs {
			if d.Name == "Time" {
				timeSeen = true
				continue
			}
			if isRoot(d.Name) && d.Name != "Subscription" {
				// Query and Mutation namespaces need a prefixed copy.
				nd := *d
				nd.Name = pg.game.Name + "_" + d.Name
				rewriteDefinition(&nd, pg.game.Name)
				merged.Definitions = append(merged.Definitions, &nd)
				continue
			}
			if isRoot(d.Name) {
				// Subscription is flattened below.
				continue
			}
			nd := *d
			nd.Name = pg.game.Name + "_" + d.Name
			rewriteDefinition(&nd, pg.game.Name)
			merged.Definitions = append(merged.Definitions, &nd)
		}
	}
	if timeSeen {
		merged.Definitions = append(merged.Definitions, &ast.Definition{Kind: ast.Scalar, Name: "Time"})
	}
	merged.Definitions = append(merged.Definitions,
		namespaceRoot("Query", "Query"),
		namespaceRoot("Mutation", "Mutation"),
		subscriptionRoot(parsed),
	)

	var buf bytes.Buffer
	buf.WriteString(CodeHeader)
	formatter.NewFormatter(&buf).FormatSchemaDocument(merged)
	return buf.Bytes(), nil
}

// RepoRoot locates the module root from this source file.
func RepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("merge: cannot locate source file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..")), nil
}

func parse(path string) (ast.DefinitionList, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("merge: read %s: %w", path, err)
	}
	doc, err := parser.ParseSchemas(&ast.Source{Name: path, Input: string(b)})
	if err != nil {
		return nil, fmt.Errorf("merge: parse %s: %w", path, err)
	}
	return doc.Definitions, nil
}

func isRoot(name string) bool {
	return name == "Query" || name == "Mutation" || name == "Subscription"
}

func namespaceRoot(root, typ string) *ast.Definition {
	def := &ast.Definition{Kind: ast.Object, Name: root}
	for _, g := range Games {
		def.Fields = append(def.Fields, &ast.FieldDefinition{
			Name: g.Namespace,
			Type: ast.NonNullNamedType(g.Name+"_"+typ, nil),
		})
	}
	return def
}

func subscriptionRoot(parsed []parsedGame) *ast.Definition {
	def := &ast.Definition{Kind: ast.Object, Name: "Subscription"}
	for _, pg := range parsed {
		for _, d := range pg.defs {
			if d.Name != "Subscription" {
				continue
			}
			for _, f := range d.Fields {
				nf := *f
				nf.Name = pg.game.Namespace + strings.ToUpper(f.Name[:1]) + f.Name[1:]
				rewriteType(nf.Type, pg.game.Name)
				nf.Arguments = copyArgs(f.Arguments, pg.game.Name)
				def.Fields = append(def.Fields, &nf)
			}
		}
	}
	return def
}

func copyArgs(in ast.ArgumentDefinitionList, prefix string) ast.ArgumentDefinitionList {
	out := make(ast.ArgumentDefinitionList, len(in))
	for i, a := range in {
		na := *a
		rewriteType(na.Type, prefix)
		out[i] = &na
	}
	return out
}

func rewriteDefinition(d *ast.Definition, prefix string) {
	for i, name := range d.Interfaces {
		d.Interfaces[i] = prefix + "_" + name
	}
	for i, name := range d.Types {
		d.Types[i] = prefix + "_" + name
	}
	for _, f := range d.Fields {
		rewriteType(f.Type, prefix)
		f.Arguments = copyArgs(f.Arguments, prefix)
	}
}

func rewriteType(t *ast.Type, prefix string) {
	if t == nil {
		return
	}
	if t.Elem != nil {
		rewriteType(t.Elem, prefix)
		return
	}
	switch t.NamedType {
	case "String", "Int", "Float", "Boolean", "ID", "Time":
		return
	}
	t.NamedType = prefix + "_" + t.NamedType
}
