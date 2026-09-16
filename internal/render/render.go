// Package render owns template loading (docs gap 7): html/template has no
// '**' glob, so the loader walks an fs.FS and parses each page into a CLONE
// of the shared base set (layouts + partials + FuncMap). Handlers reference
// pages via typed Page constants — a template-name typo is a compile error.
// Fail fast: every page must define {{define "content"}} (checked at startup).
package render

import (
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"strings"
)

// Page is the template path under templates/pages/.
type Page string

// Renderer holds one parsed template set per page.
type Renderer struct {
	sets map[Page]*template.Template
}

// New builds the per-page template sets from layouts + partials + pages.
func New(fsys fs.FS, fm template.FuncMap) (*Renderer, error) {
	base := template.New("base").Funcs(fm)
	base, err := base.ParseFS(fsys, "templates/layouts/*.html", "templates/partials/*.html")
	if err != nil {
		return nil, err
	}
	r := &Renderer{sets: map[Page]*template.Template{}}
	entries, err := fs.ReadDir(fsys, "templates/pages")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		path := "templates/pages/" + e.Name()
		set, err := base.Clone()
		if err != nil {
			return nil, err
		}
		if _, err := set.ParseFS(fsys, path); err != nil {
			return nil, err
		}
		if set.Lookup("content") == nil {
			return nil, fmt.Errorf("template %s has no {{define \"content\"}}", path)
		}
		r.sets[Page(path)] = set
	}
	return r, nil
}

// Pages of the MVP (compile-checked names for handlers).
const (
	PageHome              Page = "templates/pages/home.html"
	PageVotations         Page = "templates/pages/votations.html"
	PageVotationDetail    Page = "templates/pages/votation_detail.html"
	PageRepresentatives   Page = "templates/pages/representatives.html"
	PageRepresentative    Page = "templates/pages/representative.html"
	PageParties           Page = "templates/pages/parties.html"
	PageParty             Page = "templates/pages/party.html"
	PageBills             Page = "templates/pages/bills.html"
	PageBill              Page = "templates/pages/bill.html"
	PartialVoteBreakdown  Page = "templates/partials/vote_breakdown.html"
)

func (r *Renderer) set(p Page) *template.Template {
	return r.sets[p]
}

func (r *Renderer) execute(w io.Writer, p Page, name string, data any) error {
	set := r.set(p)
	if set == nil {
		return fmt.Errorf("no template for page %s", p)
	}
	return set.ExecuteTemplate(w, name, data)
}

// Lookup a partial define (HTMX swap by template name).
func (r *Renderer) lookup(p Page, name string) bool {
	return r.set(p) != nil && r.set(p).Lookup(name) != nil
}

// Page executes a full page (layouts/{base}.html shell + content).
func (r *Renderer) Page(w io.Writer, p Page, data any) error {
	if r.set(p) == nil {
		return fmt.Errorf("no template for page %s", p)
	}
	if !r.lookup(p, "base") {
		return fmt.Errorf("page %s has no base define", p)
	}
	return r.set(p).ExecuteTemplate(w, "base", data)
}

// Partial executes an HTMX partial swap target by template name. Each
// partial is a single file with one define; the vote-breakdown partial is
// shared across pages.
func (r *Renderer) Partial(w io.Writer, name string, data any) error {
	for _, set := range r.sets {
		if set.Lookup(name) != nil {
			return set.ExecuteTemplate(w, name, data)
		}
	}
	return fmt.Errorf("no partial %s", name)
}
