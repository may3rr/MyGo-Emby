// Package emby is a small, dependency-free client for the Emby and Jellyfin
// media server HTTP APIs, written for the MyGo Emby desktop client.
//
// A [Client] is bound to one server and is safe for concurrent use from
// multiple goroutines. Every method that touches the network takes a
// [context.Context]; URL builders (images, subtitles) are pure and never
// block, so they can be called from view code.
//
// Typical flow:
//
//	c, err := emby.NewClient("demo.jellyfin.org/stable", emby.DeviceInfo{
//		Client: "MyGo Emby", Device: "Mac", DeviceID: id, Version: "0.1.0",
//	})
//	info, err := c.PublicInfo(ctx)          // probe before asking for a password
//	sess, err := c.Login(ctx, "demo", "")   // persist sess as JSON
//	// next launch:
//	c, err = emby.RestoreClient(sess, device)
//	views, err := c.Views(ctx)
//
// Errors returned by network methods can be classified with [errors.Is]
// against [ErrUnreachable], [ErrUnauthorized], [ErrForbidden], [ErrNotFound],
// [ErrBadResponse] and [ErrNotLoggedIn], or inspected via [errors.As] with
// [*Error] for the HTTP status code and server message.
//
// Compatibility notes (verified live against Jellyfin 12.4 and a server
// reporting Emby 4.9.3, which turned out to be an Emby-compatible proxy that
// cannot transcode; real-Emby transcoding responses are covered by unit
// tests only):
//   - Requests carry the token in "Authorization: MediaBrowser …",
//     "X-Emby-Authorization" and "X-Emby-Token". Newer Jellyfin servers
//     reject the legacy X-Emby-* headers on their own, Emby prefers them.
//   - URLs handed to the player carry the token twice, as "api_key" (Emby,
//     legacy Jellyfin) and "ApiKey" (Jellyfin with legacy auth disabled).
//     Each server ignores the parameter it does not know.
package emby
