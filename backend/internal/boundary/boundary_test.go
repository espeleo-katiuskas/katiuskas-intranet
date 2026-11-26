// Package boundary holds one test: the line between the catalogue of places and the rest of the intranet.
//
// The catalogue is going to be an independent project in a repository of its own (docs/catalogue-extraction.md),
// so nothing of it may depend on what stays here, and what stays here may touch it only at the few points of
// the contract. The test reads the imports of every package (and of its tests) and fails for any that crosses
// the line, so that it cannot be undone without noticing. When a package of the catalogue really needs
// something new, the answer is to put it in the contract, not to widen the lists below.
package boundary

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const module = "github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/"

// catalogue are the packages that move to the new repository as they are.
var catalogue = set("access", "accounts", "areas", "osmmirror", "places", "publicplaces", "publicsite")

// contract is the vocabulary that the catalogue and the activities share (placelinks). It stays on both
// sides until the catalogue is out, and then it is only the activities' (they refer to places by id and ask
// the catalogue for their names over HTTP).
var contract = set("placelinks")

// shared are the small pieces of infrastructure that the catalogue also needs: they are copied into the
// new repository, not shared (there is no common module).
var shared = set("auth", "httpapi", "platform/db", "platform/clubtime", "platform/logging")

// testHelpers are the only things of testutil that the tests of the catalogue may use: a transaction that is
// rolled back, sessions for people that exist nowhere, and an account of the catalogue. The rest of testutil
// makes rows of the intranet's tables (people, memberships, activities…), which the catalogue does not have.
var testHelpers = set("NewTx", "NewTestAuthenticator", "FakeResolver", "TokenFor", "NewPerson", "Person", "CatalogueAccount", "Identified")

// reachIn are the points where something that is not the catalogue imports a package of it, with what it may
// import. Production code first, then tests.
var reachIn = map[string]map[string]bool{
	"cmd/api":     set(append(sorted(catalogue), "placelinks")...), // the composition root wires everything
	"cmd/osmsync": set("osmmirror"),
	"auth":        set("access"), // the viewer is an assertion about a person
	"users":       set("access"), // the issuer of the intranet
	"activities":  set("placelinks"),
	"testutil":    set("access", "accounts"), // fixtures
}

var reachInTests = map[string]map[string]bool{
	"cmd/api":           set(sorted(catalogue)...),
	"activities":        set("places", "accounts"), // the activities against the real catalogue
	"activityresources": set("places", "accounts"),
}

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

func sorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type pkg struct {
	name       string          // "places", "cmd/api"
	prod, test map[string]bool // internal packages imported, by production files and by tests
	testutil   map[string]bool // what of testutil the tests use
}

func root(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// packages reads the imports of every directory under internal/ and cmd/.
func packages(t *testing.T) []pkg {
	t.Helper()
	base := root(t)
	var out []pkg
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(base, top), func(path string, d os.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return err
			}
			files, _ := filepath.Glob(filepath.Join(path, "*.go"))
			if len(files) == 0 {
				return nil
			}
			rel, _ := filepath.Rel(base, path)
			name := filepath.ToSlash(rel)
			name = strings.TrimPrefix(name, "internal/")
			p := pkg{name: name, prod: map[string]bool{}, test: map[string]bool{}, testutil: map[string]bool{}}
			fset := token.NewFileSet()
			for _, file := range files {
				f, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
				if err != nil {
					t.Fatalf("%s: %v", file, err)
				}
				isTest := strings.HasSuffix(file, "_test.go")
				for _, imp := range f.Imports {
					ip := strings.Trim(imp.Path.Value, `"`)
					if !strings.HasPrefix(ip, module) {
						continue
					}
					dep := strings.TrimPrefix(ip, module)
					if isTest {
						p.test[dep] = true
					} else {
						p.prod[dep] = true
					}
				}
				if isTest {
					full, err := parser.ParseFile(fset, file, nil, 0)
					if err != nil {
						t.Fatalf("%s: %v", file, err)
					}
					ast.Inspect(full, func(n ast.Node) bool {
						if sel, ok := n.(*ast.SelectorExpr); ok {
							if id, ok := sel.X.(*ast.Ident); ok && id.Name == "testutil" {
								p.testutil[sel.Sel.Name] = true
							}
						}
						return true
					})
				}
			}
			out = append(out, p)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func in(m map[string]bool, name string) bool { return m[name] }

// Nothing of the catalogue depends on the rest of the intranet: its packages import each other, the shared
// infrastructure and the contract, and nothing else.
func TestTheCatalogueDependsOnNothingOfTheIntranet(t *testing.T) {
	for _, p := range packages(t) {
		if !in(catalogue, p.name) {
			continue
		}
		for _, imports := range []map[string]bool{p.prod, p.test} {
			for dep := range imports {
				if !in(catalogue, dep) && !in(contract, dep) && !in(shared, dep) && dep != "testutil" && dep != "boundary" {
					t.Errorf("%s imports %s: the catalogue must not depend on the intranet (see docs/catalogue-extraction.md)", p.name, dep)
				}
			}
		}
		// The tests of the catalogue use no fixture of the intranet's tables.
		for use := range p.testutil {
			if !in(testHelpers, use) {
				t.Errorf("a test of %s uses testutil.%s, which makes rows of the intranet: use testutil.NewPerson and the like", p.name, use)
			}
		}
	}
}

// What is not the catalogue reaches into it only at the points of the contract.
func TestTheIntranetTouchesTheCatalogueOnlyWhereTheContractSays(t *testing.T) {
	for _, p := range packages(t) {
		if in(catalogue, p.name) || p.name == "boundary" {
			continue
		}
		check := func(imports, allowed map[string]bool, what string) {
			for dep := range imports {
				if (in(catalogue, dep) || in(contract, dep)) && !in(allowed, dep) {
					t.Errorf("%s (%s) imports %s, which is not one of the points where the intranet may touch the catalogue (docs/catalogue-extraction.md)", p.name, what, dep)
				}
			}
		}
		check(p.prod, reachIn[p.name], "code")
		// A test may touch what its package may, and what the list of tests adds.
		allowedTests := map[string]bool{}
		for k := range reachIn[p.name] {
			allowedTests[k] = true
		}
		for k := range reachInTests[p.name] {
			allowedTests[k] = true
		}
		check(p.test, allowedTests, "tests")
	}
}

// The lists above name packages that exist (a rename would otherwise turn a rule into nothing).
func TestTheListsNamePackagesThatExist(t *testing.T) {
	exists := map[string]bool{}
	for _, p := range packages(t) {
		exists[p.name] = true
	}
	for _, m := range []map[string]bool{catalogue, contract, shared} {
		for name := range m {
			if !exists[name] {
				t.Errorf("%s is named in the boundary test but is not a package", name)
			}
		}
	}
	for _, m := range []map[string]map[string]bool{reachIn, reachInTests} {
		for from, tos := range m {
			if !exists[from] {
				t.Errorf("%s is named in the boundary test but is not a package", from)
			}
			for to := range tos {
				if !exists[to] {
					t.Errorf("%s is named in the boundary test but is not a package", to)
				}
			}
		}
	}
}
