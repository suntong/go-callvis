package main

import (
	"errors"
	"fmt"
	"go/types"
	"os"
	"strings"
	"net/http"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

//==[ type def/func: analysis   ]===============================================
type renderOpts struct {
	focus   string
	group   []string
	ignore  []string
	include []string
	limit   []string
	nointer bool
	nostd   bool
}

// mainPackages returns the main packages to analyze.
// Each resulting package is named "main" and has a main function.
func mainPackages(pkgs []*ssa.Package) ([]*ssa.Package, error) {
	var mains []*ssa.Package
	for _, p := range pkgs {
		if p != nil && p.Pkg.Name() == "main" && p.Func("main") != nil {
			mains = append(mains, p)
		}
	}
	if len(mains) == 0 {
		return nil, fmt.Errorf("no main packages")
	}
	return mains, nil
}

//==[ type def/func: analysis   ]===============================================
type analysis struct {
	opts   *renderOpts
	prog   *ssa.Program
	pkgs   []*ssa.Package
	mains  []*ssa.Package
	result *pointer.Result
}

var Analysis *analysis

func (a *analysis) DoAnalysis(
	dir string,
	tests bool,
	args []string,
) error {
	cfg := &packages.Config{
		Mode:  packages.LoadAllSyntax,
		Tests: tests,
		Dir:   dir,
	}

	initial, err := packages.Load(cfg, args...)
	if err != nil {
		return err
	}

	if packages.PrintErrors(initial) > 0 {
		return fmt.Errorf("packages contain errors")
	}

	// Create and build SSA-form program representation.
	prog, pkgs := ssautil.AllPackages(initial, 0)
	prog.Build()

	mains, err := mainPackages(pkgs)
	if err != nil {
		return err
	}

	config := &pointer.Config{
		Mains:          mains,
		BuildCallGraph: true,
	}

	result, err := pointer.Analyze(config)
	if err != nil {
		return err // internal error in pointer analysis
	}
	//cg.DeleteSyntheticNodes()
	/*
	Analysis = &analysis{
		prog:   prog,
		pkgs:   pkgs,
		mains:  mains,
		result: result,
	}
	*/

	a.prog   = prog
	a.pkgs   = pkgs
	a.mains  = mains
	a.result = result
	return nil
}

func (a *analysis) OptsSetup() {
	a.opts = &renderOpts{
		focus:   *focusFlag,
		group:   []string{*groupFlag},
		ignore:  []string{*ignoreFlag},
		include: []string{*includeFlag},
		limit:   []string{*limitFlag},
		nointer: *nointerFlag,
		nostd:   *nostdFlag,
	}
}

func (a *analysis) ProcessListArgs() (e error) {
	var groupBy      []string
	var ignorePaths  []string
	var includePaths []string
	var limitPaths   []string

	for _, g := range strings.Split(a.r.group[0], ",") {
		g := strings.TrimSpace(g)
		if g == "" {
			continue
		}
		if g != "pkg" && g != "type" {
			e = errors.New("invalid group option")
			return
		}
		groupBy = append(groupBy, g)
	}

	for _, p := range strings.Split(a.r.ignore[0], ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			ignorePaths = append(ignorePaths, p)
		}
	}

	for _, p := range strings.Split(a.r.include[0], ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			includePaths = append(includePaths, p)
		}
	}

	for _, p := range strings.Split(a.r.limit[0], ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			limitPaths = append(limitPaths, p)
		}
	}

	a.r.group = groupBy
	a.r.ignore = ignorePaths
	a.r.include = includePaths
	a.r.limit = limitPaths

	return
}

func (a *analysis) OverrideByHTTP(r *http.Request) () {
	if f := r.FormValue("f"); f == "all" {
		a.opts.focus = ""
	} else if f != "" {
		opts.focus = f
	}
	if std := r.FormValue("std"); std != "" {
		a.opts.nostd = false
	}
	if inter := r.FormValue("nointer"); inter != "" {
		a.opts.nointer = true
	}
	if g := r.FormValue("group"); g != "" {
		a.opts.group[0] = g
	}
	if l := r.FormValue("limit"); l != "" {
		a.opts.limit[0] = l
	}
	if ign := r.FormValue("ignore"); ign != "" {
		a.opts.ignore[0] = ign
	}
	if inc := r.FormValue("include"); inc != "" {
		a.opts.include[0] = inc
	}
	return
}

// basically do printOutput() with previously checking
// focus option and respective package
func (a *analysis) Render() ([]byte, error) {
	var (
		err      error
		ssaPkg   *ssa.Package
		focusPkg *types.Package
	)

	if a.opts.focus != "" {
		if ssaPkg = a.prog.ImportedPackage(a.opts.focus); ssaPkg == nil {
			if strings.Contains(a.opts.focus, "/") {
				return nil, fmt.Errorf("focus failed: %v", err)
			}
			// try to find package by name
			var foundPaths []string
			for _, p := range a.pkgs {
				if p.Pkg.Name() == a.opts.focus {
					foundPaths = append(foundPaths, p.Pkg.Path())
				}
			}
			if len(foundPaths) == 0 {
				return nil, fmt.Errorf("focus failed, could not find package: %v", a.opts.focus)
			} else if len(foundPaths) > 1 {
				for _, p := range foundPaths {
					fmt.Fprintf(os.Stderr, " - %s\n", p)
				}
				return nil, fmt.Errorf("focus failed, found multiple packages with name: %v", a.opts.focus)
			}
			// found single package
			if ssaPkg = a.prog.ImportedPackage(foundPaths[0]); ssaPkg == nil {
				return nil, fmt.Errorf("focus failed: %v", err)
			}
		}
		focusPkg = ssaPkg.Pkg
		logf("focusing: %v", focusPkg.Path())
	}

	dot, err := printOutput(
		a.prog,
		a.mains[0].Pkg,
		a.result.CallGraph,
		focusPkg,
		a.opts.limit,
		a.opts.ignore,
		a.opts.include,
		a.opts.group,
		a.opts.nostd,
		a.opts.nointer,
	)
	if err != nil {
		return nil, fmt.Errorf("processing failed: %v", err)
	}

	return dot, nil
}

