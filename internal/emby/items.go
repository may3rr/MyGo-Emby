package emby

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

// Sort fields for ItemQuery.SortBy.
const (
	SortName            = "SortName"
	SortDateCreated     = "DateCreated"
	SortProductionYear  = "ProductionYear"
	SortPremiereDate    = "PremiereDate"
	SortCommunityRating = "CommunityRating"
	SortCriticRating    = "CriticRating"
	SortDatePlayed      = "DatePlayed"
	SortRuntime         = "Runtime"
	SortRandom          = "Random"
	SortIndex           = "ParentIndexNumber,IndexNumber" // episodes in airing order
)

// Filters for ItemQuery.Filters.
const (
	FilterIsUnplayed  = "IsUnplayed"
	FilterIsPlayed    = "IsPlayed"
	FilterIsFavorite  = "IsFavorite"
	FilterIsResumable = "IsResumable"
	FilterIsFolder    = "IsFolder"
	FilterIsNotFolder = "IsNotFolder"
)

// Field names for ItemQuery.Fields (request extra BaseItemDto fields).
const (
	FieldOverview                = "Overview"
	FieldGenres                  = "Genres"
	FieldPeople                  = "People"
	FieldMediaSources            = "MediaSources"
	FieldMediaStreams            = "MediaStreams"
	FieldChapters                = "Chapters"
	FieldStudios                 = "Studios"
	FieldProviderIDs             = "ProviderIds"
	FieldTaglines                = "Taglines"
	FieldExternalURLs            = "ExternalUrls"
	FieldPrimaryImageAspectRatio = "PrimaryImageAspectRatio"
	FieldDateCreated             = "DateCreated"
	FieldChildCount              = "ChildCount"
	FieldRecursiveItemCount      = "RecursiveItemCount"
	FieldPath                    = "Path"
	FieldSortName                = "SortName"
	FieldOriginalTitle           = "OriginalTitle"
	FieldTags                    = "Tags"
	FieldPremiereDate            = "PremiereDate"
	FieldProductionYear          = "ProductionYear"
	FieldBasicSyncInfo           = "BasicSyncInfo"
)

// ListFields are the extra fields requested by list endpoints: enough for
// cards and hero banners without the heavy media stream data.
var ListFields = []string{
	FieldPrimaryImageAspectRatio, FieldOverview, FieldProductionYear,
	FieldPremiereDate, FieldDateCreated, FieldGenres, FieldOriginalTitle,
}

// DetailFields are requested for a single item's detail page.
var DetailFields = []string{
	FieldOverview, FieldGenres, FieldPeople, FieldMediaSources, FieldMediaStreams,
	FieldChapters, FieldStudios, FieldProviderIDs, FieldTaglines, FieldExternalURLs,
	FieldPrimaryImageAspectRatio, FieldDateCreated, FieldChildCount,
	FieldRecursiveItemCount, FieldOriginalTitle, FieldTags, FieldPremiereDate,
	FieldProductionYear, "RemoteTrailers", "Width", "Height", "Trickplay",
}

// listImageTypes limits list responses to the image types cards use.
const listImageTypes = "Primary,Backdrop,Thumb,Logo,Banner"

// SortOrder is ascending or descending.
type SortOrder string

// Sort orders.
const (
	Ascending  SortOrder = "Ascending"
	Descending SortOrder = "Descending"
)

// ItemQuery is a generic item query (GET /Users/{uid}/Items).
// Zero fields are omitted.
type ItemQuery struct {
	ParentID         string
	IDs              []string
	IncludeItemTypes []string // TypeMovie, TypeSeries…
	ExcludeItemTypes []string
	MediaTypes       []string // "Video"
	Recursive        bool
	SortBy           []string // SortName…; multiple fields allowed
	SortOrder        SortOrder
	Filters          []string // FilterIsUnplayed, FilterIsFavorite…
	StartIndex       int
	Limit            int // 0 = server default (all)
	SearchTerm       string
	PersonIDs        []string
	Genres           []string
	Years            []int
	// Fields adds fields to ListFields.
	Fields []string
	// Extra is merged into the query string as-is, for parameters this
	// struct does not cover.
	Extra url.Values
}

func (q ItemQuery) values() url.Values {
	v := url.Values{}
	setList := func(k string, l []string) {
		if len(l) > 0 {
			v.Set(k, strings.Join(l, ","))
		}
	}
	if q.ParentID != "" {
		v.Set("ParentId", q.ParentID)
	}
	setList("Ids", q.IDs)
	setList("IncludeItemTypes", q.IncludeItemTypes)
	setList("ExcludeItemTypes", q.ExcludeItemTypes)
	setList("MediaTypes", q.MediaTypes)
	if q.Recursive {
		v.Set("Recursive", "true")
	}
	setList("SortBy", q.SortBy)
	if q.SortOrder != "" {
		v.Set("SortOrder", string(q.SortOrder))
	}
	setList("Filters", q.Filters)
	if q.StartIndex > 0 {
		v.Set("StartIndex", strconv.Itoa(q.StartIndex))
	}
	if q.Limit > 0 {
		v.Set("Limit", strconv.Itoa(q.Limit))
	}
	if q.SearchTerm != "" {
		v.Set("SearchTerm", q.SearchTerm)
	}
	setList("PersonIds", q.PersonIDs)
	if len(q.Genres) > 0 {
		v.Set("Genres", strings.Join(q.Genres, "|"))
	}
	if len(q.Years) > 0 {
		ys := make([]string, len(q.Years))
		for i, y := range q.Years {
			ys[i] = strconv.Itoa(y)
		}
		v.Set("Years", strings.Join(ys, ","))
	}
	v.Set("Fields", joinFields(ListFields, q.Fields))
	v.Set("EnableImageTypes", listImageTypes)
	v.Set("ImageTypeLimit", "1")
	v.Set("EnableUserData", "true")
	v.Set("EnableTotalRecordCount", "true")
	for k, vals := range q.Extra {
		v[k] = vals
	}
	return v
}

func joinFields(base []string, extra []string) string {
	seen := make(map[string]bool, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, l := range [][]string{base, extra} {
		for _, f := range l {
			if f != "" && !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return strings.Join(out, ",")
}

// listQuery returns the common query for list endpoints.
func listQuery(limit int, extraFields ...string) url.Values {
	v := url.Values{}
	if limit > 0 {
		v.Set("Limit", strconv.Itoa(limit))
	}
	v.Set("Fields", joinFields(ListFields, extraFields))
	v.Set("EnableImageTypes", listImageTypes)
	v.Set("ImageTypeLimit", "1")
	v.Set("EnableUserData", "true")
	return v
}

// ItemsResult is a page of items plus the total count for paging.
type ItemsResult struct {
	Items            []Item
	TotalRecordCount int
	StartIndex       int
}

// Items runs a generic item query.
func (c *Client) Items(ctx context.Context, q ItemQuery) (*ItemsResult, error) {
	uid, err := c.requireUser("GET /Users/{id}/Items")
	if err != nil {
		return nil, err
	}
	var r QueryResult
	if err := c.do(ctx, http.MethodGet, "/Users/"+url.PathEscape(uid)+"/Items", q.values(), nil, &r); err != nil {
		return nil, err
	}
	if r.TotalRecordCount == 0 && len(r.Items) > 0 {
		r.TotalRecordCount = q.StartIndex + len(r.Items) // server omitted the count
	}
	return &ItemsResult{Items: r.Items, TotalRecordCount: r.TotalRecordCount, StartIndex: q.StartIndex}, nil
}

// Views returns the user's libraries (GET /Users/{uid}/Views), in the
// user's configured order.
func (c *Client) Views(ctx context.Context) ([]Item, error) {
	uid, err := c.requireUser("GET /Users/{id}/Views")
	if err != nil {
		return nil, err
	}
	var r QueryResult
	if err := c.do(ctx, http.MethodGet, "/Users/"+url.PathEscape(uid)+"/Views", listQuery(0), nil, &r); err != nil {
		return nil, err
	}
	return r.Items, nil
}

// ResumeItems returns partly watched videos, most recent first
// (GET /Users/{uid}/Items/Resume).
func (c *Client) ResumeItems(ctx context.Context, limit int) ([]Item, error) {
	uid, err := c.requireUser("GET /Users/{id}/Items/Resume")
	if err != nil {
		return nil, err
	}
	q := listQuery(limit)
	q.Set("MediaTypes", "Video")
	q.Set("Recursive", "true")
	var r QueryResult
	if err := c.do(ctx, http.MethodGet, "/Users/"+url.PathEscape(uid)+"/Items/Resume", q, nil, &r); err != nil {
		return nil, err
	}
	return r.Items, nil
}

// NextUp returns the next unwatched episode of each series in progress
// (GET /Shows/NextUp). seriesID restricts it to one series ("" for all).
// Episodes that already have a resume position are excluded on Jellyfin so
// they do not duplicate ResumeItems.
func (c *Client) NextUp(ctx context.Context, seriesID string, limit int) ([]Item, error) {
	uid, err := c.requireUser("GET /Shows/NextUp")
	if err != nil {
		return nil, err
	}
	q := listQuery(limit)
	q.Set("UserId", uid)
	if seriesID != "" {
		q.Set("SeriesId", seriesID)
	}
	q.Set("EnableResumable", "false")
	q.Set("EnableTotalRecordCount", "false")
	var r QueryResult
	if err := c.do(ctx, http.MethodGet, "/Shows/NextUp", q, nil, &r); err != nil {
		return nil, err
	}
	return r.Items, nil
}

// Latest returns recently added items of a library (GET
// /Users/{uid}/Items/Latest). parentID is a view id ("" for all
// libraries). Episodes are grouped into their series.
func (c *Client) Latest(ctx context.Context, parentID string, limit int) ([]Item, error) {
	uid, err := c.requireUser("GET /Users/{id}/Items/Latest")
	if err != nil {
		return nil, err
	}
	q := listQuery(limit)
	if parentID != "" {
		q.Set("ParentId", parentID)
	}
	q.Set("GroupItems", "true")
	var items []Item // this endpoint returns a bare array
	if err := c.do(ctx, http.MethodGet, "/Users/"+url.PathEscape(uid)+"/Items/Latest", q, nil, &items); err != nil {
		return nil, err
	}
	return items, nil
}

// Item returns one item with all detail fields (DetailFields).
func (c *Client) Item(ctx context.Context, id string) (*Item, error) {
	uid, err := c.requireUser("GET /Users/{id}/Items/{item}")
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("Fields", strings.Join(DetailFields, ","))
	var it Item
	if err := c.do(ctx, http.MethodGet, "/Users/"+url.PathEscape(uid)+"/Items/"+url.PathEscape(id), q, nil, &it); err != nil {
		return nil, err
	}
	return &it, nil
}

// Seasons returns the seasons of a series (GET /Shows/{id}/Seasons).
func (c *Client) Seasons(ctx context.Context, seriesID string) ([]Item, error) {
	uid, err := c.requireUser("GET /Shows/{id}/Seasons")
	if err != nil {
		return nil, err
	}
	q := listQuery(0, FieldChildCount, FieldRecursiveItemCount)
	q.Set("UserId", uid)
	var r QueryResult
	if err := c.do(ctx, http.MethodGet, "/Shows/"+url.PathEscape(seriesID)+"/Seasons", q, nil, &r); err != nil {
		return nil, err
	}
	return r.Items, nil
}

// Episodes returns the episodes of one season of a series (GET
// /Shows/{id}/Episodes). An empty seasonID returns all episodes of the
// series in airing order.
func (c *Client) Episodes(ctx context.Context, seriesID, seasonID string) ([]Item, error) {
	uid, err := c.requireUser("GET /Shows/{id}/Episodes")
	if err != nil {
		return nil, err
	}
	q := listQuery(0)
	q.Set("UserId", uid)
	if seasonID != "" {
		q.Set("SeasonId", seasonID)
	}
	var r QueryResult
	if err := c.do(ctx, http.MethodGet, "/Shows/"+url.PathEscape(seriesID)+"/Episodes", q, nil, &r); err != nil {
		return nil, err
	}
	return r.Items, nil
}

// NextEpisode returns the episode that follows ep in its series (crossing
// season boundaries), or nil when ep is the last one. Missing (virtual)
// episodes are skipped.
func (c *Client) NextEpisode(ctx context.Context, ep *Item) (*Item, error) {
	uid, err := c.requireUser("GET /Shows/{id}/Episodes")
	if err != nil {
		return nil, err
	}
	if ep == nil || ep.Type != TypeEpisode || ep.SeriesID == "" {
		return nil, nil
	}
	q := listQuery(3)
	q.Set("UserId", uid)
	q.Set("StartItemId", ep.ID)
	q.Set("IsMissing", "false")
	var r QueryResult
	if err := c.do(ctx, http.MethodGet, "/Shows/"+url.PathEscape(ep.SeriesID)+"/Episodes", q, nil, &r); err != nil {
		return nil, err
	}
	// StartItemId returns the episode itself first, then its successors.
	for i, it := range r.Items {
		if it.ID == ep.ID {
			for _, n := range r.Items[i+1:] {
				if !n.IsVirtual() {
					return &n, nil
				}
			}
			return nil, nil
		}
	}
	// Server ignored StartItemId: fall back to AdjacentTo (prev, self, next).
	q.Del("StartItemId")
	q.Set("AdjacentTo", ep.ID)
	r = QueryResult{}
	if err := c.do(ctx, http.MethodGet, "/Shows/"+url.PathEscape(ep.SeriesID)+"/Episodes", q, nil, &r); err != nil {
		return nil, err
	}
	for i, it := range r.Items {
		if it.ID == ep.ID && i+1 < len(r.Items) {
			n := r.Items[i+1]
			return &n, nil
		}
	}
	return nil, nil
}

// Similar returns items similar to id (GET /Items/{id}/Similar).
func (c *Client) Similar(ctx context.Context, id string, limit int) ([]Item, error) {
	uid, err := c.requireUser("GET /Items/{id}/Similar")
	if err != nil {
		return nil, err
	}
	q := listQuery(limit)
	q.Set("UserId", uid)
	var r QueryResult
	if err := c.do(ctx, http.MethodGet, "/Items/"+url.PathEscape(id)+"/Similar", q, nil, &r); err != nil {
		return nil, err
	}
	return r.Items, nil
}

// PersonItems returns the movies and series a person appears in, newest
// first.
func (c *Client) PersonItems(ctx context.Context, personID string, start, limit int) (*ItemsResult, error) {
	return c.Items(ctx, ItemQuery{
		PersonIDs:        []string{personID},
		IncludeItemTypes: []string{TypeMovie, TypeSeries},
		Recursive:        true,
		SortBy:           []string{SortPremiereDate, SortProductionYear, SortName},
		SortOrder:        Descending,
		StartIndex:       start,
		Limit:            limit,
	})
}

// Favorites returns the user's favorite items of the given types (all
// types when none are given), sorted by name.
func (c *Client) Favorites(ctx context.Context, types ...string) ([]Item, error) {
	r, err := c.Items(ctx, ItemQuery{
		IncludeItemTypes: types,
		Recursive:        true,
		Filters:          []string{FilterIsFavorite},
		SortBy:           []string{SortName},
		SortOrder:        Ascending,
	})
	if err != nil {
		return nil, err
	}
	return r.Items, nil
}

// SearchResults groups search hits by kind.
type SearchResults struct {
	Movies   []Item
	Series   []Item
	Episodes []Item
	People   []Item
	BoxSets  []Item
}

// Empty reports whether nothing was found.
func (r *SearchResults) Empty() bool {
	return len(r.Movies)+len(r.Series)+len(r.Episodes)+len(r.People)+len(r.BoxSets) == 0
}

// Search looks term up across movies, series, episodes, box sets and
// people, with up to limitPerGroup hits per group. The groups are queried
// concurrently. A failing people search (some servers lack /Persons) is
// ignored; any other failure fails the call.
func (c *Client) Search(ctx context.Context, term string, limitPerGroup int) (*SearchResults, error) {
	uid, err := c.requireUser("search")
	if err != nil {
		return nil, err
	}
	term = strings.TrimSpace(term)
	res := &SearchResults{}
	if term == "" {
		return res, nil
	}
	if limitPerGroup <= 0 {
		limitPerGroup = 24
	}
	groups := []struct {
		typ string
		dst *[]Item
	}{
		{TypeMovie, &res.Movies},
		{TypeSeries, &res.Series},
		{TypeEpisode, &res.Episodes},
		{TypeBoxSet, &res.BoxSets},
	}
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		firstEr error
	)
	for _, g := range groups {
		wg.Go(func() {
			r, err := c.Items(ctx, ItemQuery{
				SearchTerm:       term,
				IncludeItemTypes: []string{g.typ},
				Recursive:        true,
				Limit:            limitPerGroup,
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstEr == nil {
					firstEr = err
				}
				return
			}
			*g.dst = r.Items
		})
	}
	wg.Go(func() {
		q := listQuery(limitPerGroup)
		q.Set("UserId", uid)
		q.Set("SearchTerm", term)
		q.Set("Recursive", "true")
		var r QueryResult
		if err := c.do(ctx, http.MethodGet, "/Persons", q, nil, &r); err == nil {
			mu.Lock()
			res.People = r.Items
			mu.Unlock()
		}
	})
	wg.Wait()
	if firstEr != nil {
		return nil, firstEr
	}
	return res, nil
}
