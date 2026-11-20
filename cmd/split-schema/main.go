// Command split-schema derives each game's gqlgen schema from the canonical
// unified schema at graph/schema.graphqls.
//
// The unified file is the only hand-edited SDL. Each games/<g>/graph/schema.graphqls
// is generated so that game's gqlgen package keeps generating its existing
// models and resolver interfaces without knowing about namespaces or prefixes.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/wricardo/drbrain/graph/merge"
)

const header = "# Code generated from graph/schema.graphqls by cmd/split-schema. DO NOT EDIT.\n\n"

func main() {
	check := flag.Bool("check", false, "verify the derived schemas are current instead of writing")
	flag.Parse()

	root, err := merge.RepoRoot()
	if err != nil {
		fail(err)
	}
	outputs, err := splitAll(root)
	if err != nil {
		fail(err)
	}
	for _, out := range outputs {
		if *check {
			got, err := os.ReadFile(out.path)
			if err != nil {
				fail(err)
			}
			if string(got) != out.sdl {
				fail(fmt.Errorf("%s is stale: run go run ./cmd/split-schema", out.path))
			}
			fmt.Printf("%s is current\n", out.path)
			continue
		}
		if err := os.WriteFile(out.path, []byte(out.sdl), 0o644); err != nil {
			fail(err)
		}
		fmt.Printf("wrote %s\n", out.path)
	}
}

type output struct {
	path string
	sdl  string
}

func splitAll(root string) ([]output, error) {
	canonical := filepath.Join(root, merge.SchemaPath)
	sdl, err := os.ReadFile(canonical)
	if err != nil {
		return nil, err
	}
	doc, err := parser.ParseSchemas(&ast.Source{Name: canonical, Input: string(sdl)})
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", canonical, err)
	}

	out := make([]output, 0, len(merge.Games))
	for _, g := range merge.Games {
		gameSDL, err := build(doc.Definitions, g)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", g.Name, err)
		}
		out = append(out, output{
			path: filepath.Join(root, g.Dir, "graph", "schema.graphqls"),
			sdl:  gameSDL,
		})
	}
	return out, nil
}

func build(defs ast.DefinitionList, g merge.Game) (string, error) {
	prefix := g.Name + "_"
	var (
		body     ast.DefinitionList
		query    *ast.Definition
		mutation *ast.Definition
		usesTime bool
	)
	for _, d := range defs {
		switch {
		case d.Name == "Time":
			// Added at the end only if a derived type uses it.
		case d.Name == "Query" || d.Name == "Mutation" || d.Name == "Subscription":
			// Global roots are handled after the game's own types.
		case strings.HasPrefix(d.Name, prefix):
			nd := cloneDefinition(d)
			nd.Name = strings.TrimPrefix(d.Name, prefix)
			rewriteDefinition(nd, prefix, &usesTime)
			switch nd.Name {
			case "Query":
				query = nd
			case "Mutation":
				mutation = nd
			default:
				body = append(body, nd)
			}
		}
	}

	var subs []*ast.FieldDefinition
	for _, d := range defs {
		if d.Name != "Subscription" {
			continue
		}
		for _, f := range d.Fields {
			if !strings.HasPrefix(f.Name, g.Namespace) {
				continue
			}
			nf := cloneField(f)
			rest := strings.TrimPrefix(f.Name, g.Namespace)
			nf.Name = lowerFirst(rest)
			rewriteType(nf.Type, prefix, &usesTime)
			rewriteArgs(nf, prefix, &usesTime)
			subs = append(subs, nf)
		}
	}

	doc := &ast.SchemaDocument{Definitions: body}
	if usesTime {
		doc.Definitions = append(doc.Definitions, &ast.Definition{Kind: ast.Scalar, Name: "Time"})
	}
	if query != nil {
		doc.Definitions = append(doc.Definitions, query)
	}
	if mutation != nil {
		doc.Definitions = append(doc.Definitions, mutation)
	}
	if len(subs) > 0 {
		doc.Definitions = append(doc.Definitions, &ast.Definition{Kind: ast.Object, Name: "Subscription", Fields: subs})
	}

	var b strings.Builder
	b.WriteString(header)
	formatter.NewFormatter(&b).FormatSchemaDocument(doc)
	return b.String(), nil
}

func cloneDefinition(d *ast.Definition) *ast.Definition {
	nd := *d
	nd.Interfaces = append([]string(nil), d.Interfaces...)
	nd.Types = append([]string(nil), d.Types...)
	nd.Fields = make(ast.FieldList, len(d.Fields))
	for i, f := range d.Fields {
		nd.Fields[i] = cloneField(f)
	}
	nd.EnumValues = append(ast.EnumValueList(nil), d.EnumValues...)
	return &nd
}

func cloneField(f *ast.FieldDefinition) *ast.FieldDefinition {
	nf := *f
	nf.Arguments = make(ast.ArgumentDefinitionList, len(f.Arguments))
	for i, a := range f.Arguments {
		na := *a
		nf.Arguments[i] = &na
	}
	return &nf
}

func rewriteDefinition(d *ast.Definition, prefix string, usesTime *bool) {
	for i, name := range d.Interfaces {
		d.Interfaces[i] = stripName(name, prefix)
	}
	for i, name := range d.Types {
		d.Types[i] = stripName(name, prefix)
	}
	for _, f := range d.Fields {
		rewriteType(f.Type, prefix, usesTime)
		rewriteArgs(f, prefix, usesTime)
	}
}

func rewriteArgs(f *ast.FieldDefinition, prefix string, usesTime *bool) {
	for _, a := range f.Arguments {
		rewriteType(a.Type, prefix, usesTime)
	}
}

func rewriteType(t *ast.Type, prefix string, usesTime *bool) {
	if t == nil {
		return
	}
	if t.Elem != nil {
		rewriteType(t.Elem, prefix, usesTime)
		return
	}
	if t.NamedType == "Time" {
		*usesTime = true
		return
	}
	t.NamedType = stripName(t.NamedType, prefix)
}

func stripName(name, prefix string) string {
	if strings.HasPrefix(name, prefix) {
		return strings.TrimPrefix(name, prefix)
	}
	return name
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[size:]
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "split-schema: %v\n", err)
	os.Exit(1)
}
