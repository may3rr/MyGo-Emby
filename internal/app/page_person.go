package app

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

// A person's page (DESIGN.md §4.5): their portrait, what the server says of
// them, and their films and shows in a grid. A server that cannot list a
// person's works (this Emby answers PersonIds with HTTP 500) leaves the
// grid out and says so; the person still shows.

const personWorksLimit = 200

// personState is a person loaded for their page.
type personState struct {
	person   *emby.Item
	works    []emby.Item
	worksErr error // the works could not be listed: the server's lack
	started  bool
	loading  bool
	err      error
	reload   int
	gen      gen
}

// person returns the page state of the person with the ID id, loading it
// when it is not, or again after ⌘R.
func (a *app) person(id string) *personState {
	ps := a.people[id]
	if ps == nil {
		ps = &personState{}
		a.people[id] = ps
	}
	if a.client != nil && !ps.loading && (!ps.started || ps.reload != a.reload) {
		a.loadPerson(ps, id)
	}
	return ps
}

func (a *app) loadPerson(ps *personState, id string) {
	ps.started, ps.loading, ps.reload = true, true, a.reload
	n := ps.gen.next()
	epoch := a.epoch
	type result struct {
		person   *emby.Item
		works    []emby.Item
		worksErr error
	}
	load(a, func(ctx context.Context, cl *emby.Client) (result, error) {
		var (
			r   result
			err error
			wg  sync.WaitGroup
		)
		wg.Go(func() { r.person, err = cl.Item(ctx, id) })
		wg.Go(func() {
			res, err := cl.PersonItems(ctx, id, 0, personWorksLimit)
			if err != nil {
				r.worksErr = err
				return
			}
			r.works = res.Items
		})
		wg.Wait()
		if errors.Is(r.worksErr, emby.ErrUnauthorized) {
			err = r.worksErr
		}
		return r, err
	}, func(r result, err error) {
		if a.epoch != epoch || !ps.gen.is(n) {
			return
		}
		ps.loading = false
		if err != nil {
			ps.err = err
			return
		}
		ps.person, ps.works, ps.worksErr, ps.err = r.person, r.works, r.worksErr, nil
	})
}

func (a *app) personPage(c *ui.Context, r *ui.Route, id string) {
	ps := a.person(id)
	title := str.Person
	if ps.person != nil {
		title = ps.person.Name
	}
	spec := pageSpec{Title: title, Eyebrow: str.Person,
		Actions: []barAction{{Icon: iconRefresh, Label: str.MenuRefresh, Do: a.refresh}}}
	if ps.person == nil {
		a.scrollPage(c, r, spec, func() {
			if ps.err != nil {
				a.errorState(c, a.errText(ps.err), func() { a.loadPerson(ps, id) })
				return
			}
			a.loadingState(c)
		})
		return
	}
	works := ps.works
	const headH = 220
	a.gridPage(c, r, spec, gridSpec{
		Count:    len(works),
		MinWidth: 150,
		Key:      func(i int) any { return works[i].ID },
		Label:    func(i int) string { return works[i].Name },
		Item: func(i int, width float32) {
			a.itemCard(c, &works[i], cardOpts{shown: width})
		},
		HeaderHeight: headH,
		Header:       func() { a.personHeader(c, ps, headH) },
		Empty: func() {
			msg := str.WorksNone
			if ps.worksErr != nil {
				msg = str.WorksUnsupported
			}
			a.emptyState(c, emptySpec{Icon: iconFilm, Title: str.Filmography, Message: msg})
		},
	})
}

// personHeader is a person's portrait, birth and overview, over their
// works.
func (a *app) personHeader(c *ui.Context, ps *personState, height float32) {
	p := a.pal
	person := ps.person
	const photo = 132
	ui.Row(c.Key("person")).Height(height).Gap(24).AlignItems(ui.Start).Children(func() {
		url := ""
		if _, ok := person.ImageTags[string(emby.ImagePrimary)]; ok {
			url = a.itemImage(person, emby.RolePrimary, photo)
		}
		ui.Box(c.Key("photo")).Size(photo, photo).Radius(photo/2).Shrink(0).Shadow(0, 2, 8, 0, p.shadow).
			Children(func() { a.artwork(c, url, iconUser, photo/2) })
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(8).Children(func() {
			if !person.PremiereDate.IsZero() {
				ui.Text(c, fmt.Sprintf(str.BornFmt, person.PremiereDate.Format("2006-01-02"))).
					FontSize(fsBody).TextColor(p.text2)
			}
			if person.Overview != "" {
				ui.Text(c, person.Overview).FontSize(fsBody).TextColor(p.text).LineHeight(1.5).MaxLines(5).MaxWidth(760)
			}
			if len(ps.works) > 0 {
				ui.Text(c, str.Filmography+" · "+itemCountText(len(ps.works))).FontSize(fsTitle).
					FontWeight(wSemibold).TextColor(p.text).Margin(12, 0, 0, 0)
			}
		})
	})
}
