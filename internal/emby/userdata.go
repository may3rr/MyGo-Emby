package emby

import (
	"context"
	"net/http"
	"net/url"
)

// SetFavorite marks (true) or unmarks (false) an item as favorite
// (POST/DELETE /Users/{uid}/FavoriteItems/{id}) and returns the updated
// user data.
func (c *Client) SetFavorite(ctx context.Context, itemID string, favorite bool) (*UserData, error) {
	return c.userDataCall(ctx, "FavoriteItems", itemID, favorite)
}

// SetPlayed marks (true) or unmarks (false) an item as played
// (POST/DELETE /Users/{uid}/PlayedItems/{id}) and returns the updated user
// data. Marking a series or season applies to all its episodes.
func (c *Client) SetPlayed(ctx context.Context, itemID string, played bool) (*UserData, error) {
	return c.userDataCall(ctx, "PlayedItems", itemID, played)
}

func (c *Client) userDataCall(ctx context.Context, kind, itemID string, on bool) (*UserData, error) {
	method := http.MethodPost
	if !on {
		method = http.MethodDelete
	}
	uid, err := c.requireUser(method + " /Users/{id}/" + kind)
	if err != nil {
		return nil, err
	}
	var ud UserData
	path := "/Users/" + url.PathEscape(uid) + "/" + kind + "/" + url.PathEscape(itemID)
	if err := c.do(ctx, method, path, nil, nil, &ud); err != nil {
		return nil, err
	}
	return &ud, nil
}
