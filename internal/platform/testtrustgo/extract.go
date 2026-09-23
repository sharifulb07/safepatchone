// Package testtrustgo extracts static Go test facts without executing repository code.
package testtrustgo

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"safepatch/internal/evidence"
	"safepatch/internal/proof/testtrust"
)

type File struct {
	Path    string
	Content []byte
}

// Extract requires the complete, pinned _test.go manifest from a trusted
// repository reader. Paths are logical only; no files or symlinks are opened.
// complete is a manifest guarantee, never inferred from the supplied file count.
func Extract(ctx context.Context, org string, subject evidence.Subject, files []File, complete bool, coverage testtrust.Coverage) (testtrust.Snapshot, error) {
	if ctx == nil || len(files) > 256 {
		return testtrust.Snapshot{}, testtrust.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return testtrust.Snapshot{}, err
	}
	fields := testtrust.Fields{Schema: "safepatch.testtrust-snapshot.v1", OrganizationID: org, Subject: subject, Producer: "go-testtrust", ProducerVersion: "1.0.0", Complete: complete, Tests: []testtrust.Test{}, Coverage: coverage}
	hashes := map[string]string{}
	total := 0
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return testtrust.Snapshot{}, err
		}
		total += len(file.Content)
		if !evidence.ValidPath(file.Path) || len(file.Path) > 512 || !strings.HasSuffix(file.Path, "_test.go") || hashes[file.Path] != "" || total > testtrust.MaxBytes {
			return testtrust.Snapshot{}, testtrust.ErrInvalid
		}
		hashes[file.Path] = evidence.Digest(file.Content)
		fs := token.NewFileSet()
		root, err := parser.ParseFile(fs, file.Path, file.Content, parser.ParseComments)
		if err != nil {
			return testtrust.Snapshot{}, testtrust.ErrInvalid
		}
		testingName := ""
		for _, imp := range root.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if p == "testing" {
				testingName = "testing"
				if imp.Name != nil {
					testingName = imp.Name.Name
				}
			}
		}
		for _, decl := range root.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			if suffix := strings.TrimPrefix(fn.Name.Name, "Test"); suffix != "" {
				r, _ := utf8.DecodeRuneInString(suffix)
				if unicode.IsLower(r) {
					continue
				}
			}
			if fn.Recv != nil || fn.Body == nil || fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
				fields.Complete = false
				continue
			}
			if fn.Type.Results != nil || fn.Type.TypeParams != nil {
				fields.Complete = false
				continue
			}
			param := fn.Type.Params.List[0]
			if len(param.Names) != 1 {
				fields.Complete = false
				continue
			}
			if param.Names[0].Name == "_" || param.Names[0].Obj == nil {
				fields.Complete = false
				continue
			}
			star, ok := param.Type.(*ast.StarExpr)
			if !ok {
				fields.Complete = false
				continue
			}
			typ, ok := star.X.(*ast.SelectorExpr)
			if !ok {
				fields.Complete = false
				continue
			}
			pkg, ok := typ.X.(*ast.Ident)
			if !ok || pkg.Name != testingName || typ.Sel.Name != "T" {
				fields.Complete = false
				continue
			}
			start, end := fs.Position(fn.Pos()), fs.Position(fn.End())
			t := testtrust.Test{ID: evidence.Digest([]byte(file.Path + "\n" + fn.Name.Name)), Source: evidence.SourceLocation{Path: file.Path, StartLine: start.Line, StartColumn: start.Column, EndLine: end.Line, EndColumn: end.Column}, Complete: true, Skip: "ENABLED", Negative: "UNKNOWN", Assertions: []testtrust.Assertion{}}
			if fn.Doc != nil {
				for _, comment := range fn.Doc.List {
					switch strings.TrimSpace(comment.Text) {
					case "// safepatch:testtrust negative":
						t.Negative = "NEGATIVE"
					case "// safepatch:testtrust positive":
						t.Negative = "POSITIVE"
					}
				}
			}
			// Explicit test classification is a source claim, not proof of behavior.
			names := map[string]int{}
			recognized := map[*ast.CallExpr]bool{}
			callName := func(call *ast.CallExpr) string {
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return ""
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok || id.Obj != param.Names[0].Obj {
					return ""
				}
				return sel.Sel.Name
			}
			for _, statement := range fn.Body.List {
				if conditional, ok := statement.(*ast.IfStmt); ok && conditional.Init == nil && conditional.Else == nil && len(conditional.Body.List) == 1 {
					expression, ok := conditional.Body.List[0].(*ast.ExprStmt)
					if !ok {
						continue
					}
					call, ok := expression.X.(*ast.CallExpr)
					if !ok {
						continue
					}
					name := callName(call)
					if name == "Skip" || name == "Skipf" || name == "SkipNow" {
						t.Skip = "CONDITIONAL"
						recognized[call] = true
						continue
					}
					if name != "Fatal" && name != "Fatalf" && name != "Error" && name != "Errorf" {
						continue
					}
					a := assertion(fs, conditional.Cond)
					names[a.SubjectDigest]++
					a.ID = evidence.Digest([]byte(a.SubjectDigest + "\n" + strconv.Itoa(names[a.SubjectDigest])))
					t.Assertions = append(t.Assertions, a)
					recognized[call] = true
				}
				if expression, ok := statement.(*ast.ExprStmt); ok {
					if call, ok := expression.X.(*ast.CallExpr); ok {
						switch callName(call) {
						case "Skip", "Skipf", "SkipNow":
							t.Skip = "SKIPPED"
							recognized[call] = true
						}
					}
				}
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.CallExpr:
					if !recognized[v] {
						t.Complete = false
						if name := callName(v); name == "Skip" || name == "Skipf" || name == "SkipNow" {
							if t.Skip != "SKIPPED" {
								t.Skip = "CONDITIONAL"
							}
						}
					}
				case *ast.FuncLit, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.SelectStmt, *ast.GoStmt, *ast.DeferStmt, *ast.ReturnStmt:
					t.Complete = false
				}
				return true
			})
			fields.Tests = append(fields.Tests, t)
			if len(fields.Tests) > testtrust.MaxTests {
				return testtrust.Snapshot{}, testtrust.ErrInvalid
			}
		}
	}
	// Sorted file hashes bind the entire manifest, including files with no tests.
	paths := make([]string, 0, len(hashes))
	for p := range hashes {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	manifest := []struct{ Path, Hash string }{}
	for _, p := range paths {
		manifest = append(manifest, struct{ Path, Hash string }{p, hashes[p]})
	}
	raw, _ := json.Marshal(manifest)
	fields.SourceDigest = evidence.Digest(raw)
	if err := ctx.Err(); err != nil {
		return testtrust.Snapshot{}, err
	}
	return testtrust.New(fields)
}
func text(fs *token.FileSet, n ast.Node) string {
	var b bytes.Buffer
	if format.Node(&b, fs, n) != nil {
		return ""
	}
	return b.String()
}
func assertion(fs *token.FileSet, condition ast.Expr) testtrust.Assertion {
	raw := text(fs, condition)
	a := testtrust.Assertion{SubjectDigest: evidence.Digest([]byte(raw)), PredicateDigest: evidence.Digest([]byte(raw)), Relation: "UNKNOWN"}
	binary, ok := condition.(*ast.BinaryExpr)
	if !ok {
		return a
	}
	a.SubjectDigest = evidence.Digest([]byte(text(fs, binary.X)))
	// Native tests fail when the condition is true, so invert that predicate.
	relations := map[token.Token]string{token.NEQ: "EQ", token.EQL: "NE", token.LSS: "GE", token.LEQ: "GT", token.GTR: "LE", token.GEQ: "LT"}
	relation, ok := relations[binary.Op]
	if !ok {
		return a
	}
	bound := text(fs, binary.Y)
	// Decimal constants only. Named values, nil, strings and compound expressions
	// retain their predicate hash but have no invented numeric strength ordering.
	if len(bound) == 0 || len(bound) > 96 {
		return a
	}
	for i, c := range bound {
		if !(c >= '0' && c <= '9' || c == '.' || c == '-' && i == 0) {
			return a
		}
	}
	if !testtrust.ValidNumericBound(bound) {
		return a
	}
	a.Relation = relation
	a.Bound = bound
	return a
}
