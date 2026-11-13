package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

// The sign-in screen: a panel in the middle of the window, as macOS's own
// sign-in panels, asking for a server's address, a user name and a
// password, or listing the servers kept to choose from. Signed in, the
// same panel shows over the app as a sheet, to switch servers or add one.

// loginMode is what the panel shows.
type loginMode int

const (
	loginForm    loginMode = iota // the server's address, the user, the password
	loginServers                  // the servers kept
)

// errField is the field an error of the form is about.
type errField int

const (
	errGeneral errField = iota
	errServer
	errUser
	errPassword
)

// loginState is the panel's state.
type loginState struct {
	mode               loginMode
	server, user, pass string
	notice             string // above the fields, as a session expired
	err                string
	errOn              errField
	busy               bool // signing in: the form waits

	// What the server at the address said of itself, once it answered
	// (probeFor is the address it answered at), and a probe due at
	// probeAt, the address having changed, or at once with probeNow.
	probe    *emby.PublicInfo
	probeFor string
	probing  bool
	probeAt  time.Time
	probeNow bool
}

func newLoginState() loginState { return loginState{} }

// probeDelay is how long the address must stay as typed before the server
// there is asked who it is.
const probeDelay = 700 * time.Millisecond

// loginErr says what went wrong signing in, and which field it is about;
// probing says it went wrong asking the server who it is, before the user
// and the password were sent.
func loginErr(err error, probing bool) (string, errField) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return str.ErrTimeout, errServer
	case errors.Is(err, emby.ErrInvalidURL):
		return str.ErrInvalidURL, errServer
	case errors.Is(err, emby.ErrUnreachable):
		return str.ErrUnreachable, errServer
	case errors.Is(err, emby.ErrBadResponse), errors.Is(err, emby.ErrNotFound):
		return str.ErrNotServer, errServer
	case errors.Is(err, emby.ErrUnauthorized):
		if probing {
			return str.ErrNotServer, errServer
		}
		return str.ErrBadCredentials, errPassword
	case errors.Is(err, emby.ErrForbidden):
		return str.ErrForbidden, errGeneral
	case errors.Is(err, emby.ErrHTTP):
		return fmt.Sprintf(str.ErrServerFmt, emby.StatusCode(err)), errGeneral
	}
	return str.ErrUnknown, errGeneral
}

// submitLogin signs in with what the form holds: it asks the server who it
// is, then sends the user and the password. Once is enough: while it
// waits, submitting again does nothing.
func (a *app) submitLogin() {
	l := &a.login
	if l.busy {
		return
	}
	base, err := emby.NormalizeServerURL(l.server)
	if err != nil {
		l.err, l.errOn = str.ErrInvalidURL, errServer
		a.serverBox.Focus()
		return
	}
	user := strings.TrimSpace(l.user)
	if user == "" {
		l.err, l.errOn = str.ErrEnterUser, errUser
		a.userBox.Focus()
		return
	}
	cl, err := a.newClient(base)
	if err != nil {
		l.err, l.errOn = loginErr(err, true)
		return
	}
	l.err, l.errOn = "", errGeneral
	l.busy = true
	l.probeAt, l.probeNow = time.Time{}, false
	a.probeGen.next() // the form's own probe answers no more
	n := a.loginGen.next()
	pass := l.pass
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
		defer cancel()
		info, err := cl.PublicInfo(ctx)
		probing := err != nil
		if err == nil {
			a.update(func() {
				if a.loginGen.is(n) {
					a.login.probe, a.login.probeFor = info, base
				}
			})
			_, err = cl.Login(ctx, user, pass)
		}
		a.update(func() {
			if !a.loginGen.is(n) {
				// Given up meanwhile: the server keeps no session of ours.
				if err == nil {
					go func() { _ = cl.Logout(context.Background()) }()
				}
				return
			}
			a.login.busy = false
			if err != nil {
				a.login.err, a.login.errOn = loginErr(err, probing)
				if a.login.errOn == errPassword {
					a.passBox.Focus()
				}
				return
			}
			a.signedIn(cl)
		})
	}()
}

// cancelLogin gives up a sign-in under way.
func (a *app) cancelLogin() {
	if a.login.busy {
		a.loginGen.next()
		a.login.busy = false
	}
}

// signedIn uses the account cl just signed in to, and keeps it.
func (a *app) signedIn(cl *emby.Client) {
	a.remember(cl.Session())
	a.useClient(cl, connOK)
	a.login = newLoginState()
	a.persistAccounts()
	a.loadViews()
}

// serverEdited follows a change of the address: what the server at the
// old one said goes, and the new one is asked once the typing stops.
func (a *app) serverEdited(now time.Time) {
	l := &a.login
	l.probe, l.probing = nil, false
	a.probeGen.next()
	if l.errOn == errServer {
		l.err = ""
	}
	l.probeAt = now.Add(probeDelay)
}

// tickProbe asks the server at the address who it is, once a probe is due.
func (a *app) tickProbe(c *ui.Context) {
	l := &a.login
	if l.busy || (l.probeAt.IsZero() && !l.probeNow) {
		return
	}
	if !l.probeNow {
		if wait := l.probeAt.Sub(c.Now()); wait > 0 {
			c.After(wait)
			return
		}
	}
	l.probeAt, l.probeNow = time.Time{}, false
	a.probeServer()
}

// plausibleServer reports whether an address looks complete enough to ask:
// a host with a dot or a port, or localhost, not the first letters of one.
func plausibleServer(base string) bool {
	host := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	return strings.ContainsAny(host, ".:") || host == "localhost"
}

// probeServer asks the server at the form's address who it is, to show its
// name and kind under the address. What goes wrong shows only once the
// user signs in.
func (a *app) probeServer() {
	base, err := emby.NormalizeServerURL(a.login.server)
	if err != nil || !plausibleServer(base) {
		return
	}
	cl, err := a.newClient(base)
	if err != nil {
		return
	}
	n := a.probeGen.next()
	a.login.probing = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
		defer cancel()
		info, err := cl.PublicInfo(ctx)
		a.update(func() {
			if !a.probeGen.is(n) {
				return
			}
			a.login.probing = false
			if err == nil {
				a.login.probe, a.login.probeFor = info, base
			}
		})
	}()
}

// The views.

// loginScreen fills the window while no one is signed in.
func (a *app) loginScreen(c *ui.Context) {
	p := a.pal
	root := ui.Column(c.Key("login-screen")).Fill().Center().DragWindow()
	if !c.Vibrancy() || a.opaqueSidebar {
		root.Background(p.sidebar)
	}
	root.Children(func() {
		card := ui.Column(c.Key("auth-card"))
		a.styleAuthCard(card, false)
		card.Children(func() { a.authContent(c, false) })
	})
}

// styleAuthCard gives the panel its look, alone in the window or as a
// sheet over the app.
func (a *app) styleAuthCard(card ui.Element, sheet bool) {
	p := a.pal
	card.Width(400).Padding(30, 30, 24, 30).Radius(radiusPanel).Background(p.surface)
	if p.dark {
		card.Border(1, p.hairline)
	}
	if sheet {
		card.Shadow(0, 24, 60, -10, ui.RGBA(0, 0, 0, 0.35))
	} else {
		card.Shadow(0, 1, 3, 0, p.shadow).Shadow(0, 20, 48, -12, p.shadowLift)
	}
}

// serverSheet is the panel over the app, while it shows.
func (a *app) serverSheet(c *ui.Context) {
	ui.DialogBase(c.Key("server-sheet"), &a.sheetOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.28))
		a.styleAuthCard(panel, true)
		a.authContent(c, true)
	})
	if !a.sheetOpen {
		a.cancelLogin()
	}
}

// authContent is what the panel shows: the form or the servers.
func (a *app) authContent(c *ui.Context, sheet bool) {
	if a.login.mode == loginServers && len(a.servers) > 0 {
		a.serverList(c, sheet)
		return
	}
	a.loginForm(c, sheet)
}

// authHeader is the panel's mark, title and line under it.
func (a *app) authHeader(c *ui.Context, icon *ui.SVG, title, subtitle string) {
	p := a.pal
	ui.Column(c.Key("head")).AlignItems(ui.Center).Gap(5).Margin(0, 0, 22, 0).Children(func() {
		ui.Box(c).Size(56, 56).Radius(14).Center().Background(p.accent).Margin(0, 0, 10, 0).
			Shadow(0, 4, 12, -2, p.accent.Alpha(0.4)).Children(func() {
			size := float32(26)
			ic := ui.Icon(c, icon).Size(size, size).TextColor(p.onAccent)
			if icon == iconPlay {
				ic.Margin(0, 0, 0, 3)
			}
		})
		ui.Text(c, title).FontSize(fsTitle).FontWeight(wSemibold).TextColor(p.text).TextAlign(ui.Center).
			LetterSpacing(-0.2)
		ui.Text(c, subtitle).FontSize(fsBody).TextColor(p.text2).TextAlign(ui.Center)
	})
}

// loginForm asks for the server's address, the user and the password.
// Enter in any field signs in.
func (a *app) loginForm(c *ui.Context, sheet bool) {
	p := a.pal
	l := &a.login
	a.authHeader(c, iconPlay, str.LoginTitle, str.LoginSubtitle)
	if l.notice != "" {
		ui.Row(c.Key("notice")).Gap(8).Padding(9, 12).Radius(radiusButton).Margin(0, 0, 16, 0).
			AlignItems(ui.Center).Background(p.accent.Alpha(0.1)).Children(func() {
			ui.Icon(c, iconInfo).Size(15, 15).TextColor(p.accent).Shrink(0)
			ui.Text(c, l.notice).FontSize(fsBody).TextColor(p.text).Grow(1).Shrink(1).MinWidth(0)
		})
	}
	submit := false
	fieldErr := func(f errField) string {
		if l.errOn == f {
			return l.err
		}
		return ""
	}
	focus := errServer
	switch {
	case strings.TrimSpace(l.server) == "":
	case strings.TrimSpace(l.user) == "":
		focus = errUser
	default:
		focus = errPassword
	}
	ui.Column(c.Key("fields")).Gap(14).Children(func() {
		ui.Column(c.Key("server-field")).Children(func() {
			ui.Field(c, str.ServerAddress, func() {
				in := ui.TextInput(c.Key("server"), &l.server).Bind(&a.serverBox).
					Placeholder(str.ServerPlaceholder).Disabled(l.busy)
				if focus == errServer {
					in.AutoFocus()
				}
				if in.Changed() {
					a.serverEdited(c.Now())
				}
				submit = in.Submitted() || submit
			}).Error(fieldErr(errServer))
			a.probeLine(c)
		})
		ui.Field(c, str.UserName, func() {
			in := ui.TextInput(c.Key("user"), &l.user).Bind(&a.userBox).Disabled(l.busy)
			if focus == errUser {
				in.AutoFocus()
			}
			if in.Changed() && l.errOn == errUser {
				l.err = ""
			}
			submit = in.Submitted() || submit
		}).Error(fieldErr(errUser))
		ui.Field(c, str.Password, func() {
			in := ui.TextInput(c.Key("password"), &l.pass).Bind(&a.passBox).Password().
				Placeholder(str.PasswordHint).Disabled(l.busy)
			if focus == errPassword {
				in.AutoFocus()
			}
			if in.Changed() && l.errOn == errPassword {
				l.err = ""
			}
			submit = in.Submitted() || submit
		}).Error(fieldErr(errPassword))
	})
	a.tickProbe(c)
	if l.err != "" && l.errOn == errGeneral {
		ui.Row(c.Key("error")).Gap(7).Margin(14, 0, 0, 0).AlignItems(ui.Start).Role(ui.RoleAlert).
			Children(func() {
				ui.Icon(c, iconAlert).Size(15, 15).TextColor(p.danger).Shrink(0).Margin(1, 0, 0, 0)
				ui.Text(c, l.err).FontSize(fsBody).TextColor(p.danger).Grow(1).Shrink(1).MinWidth(0)
			})
	}
	label := str.SignIn
	if l.busy {
		label = str.SigningIn
	}
	if a.wideButton(c, "submit", label, l.busy).Margin(22, 0, 0, 0).Clicked() {
		submit = true
	}
	if submit {
		a.submitLogin()
	}
	switch {
	case len(a.servers) > 0:
		if a.quietButton(c, "servers", str.SavedServers).Margin(10, 0, 0, 0).Clicked() {
			a.cancelLogin()
			l.mode, l.err = loginServers, ""
		}
	case sheet:
		if a.quietButton(c, "cancel", str.Cancel).Margin(10, 0, 0, 0).Clicked() {
			a.cancelLogin()
			a.sheetOpen = false
		}
	}
}

// probeLine says what the server at the address is, once it answered, or
// that it is being asked.
func (a *app) probeLine(c *ui.Context) {
	p := a.pal
	l := &a.login
	shown := l.probing || l.probe != nil
	if !shown {
		return
	}
	ui.Row(c.Key("probe")).Height(18).Gap(6).Margin(6, 0, 0, 2).AlignItems(ui.Center).
		Transition(ui.ElementTransition{Duration: quick.Duration, Enter: &ui.Motion{Collapse: true}, Exit: &ui.Motion{Collapse: true}}).
		Children(func() {
			if l.probe == nil {
				ui.Spinner(c).FontSize(11).Shrink(0)
				ui.Text(c, str.Probing).FontSize(fsCaption).TextColor(p.text2).SingleLine()
				return
			}
			info := l.probe
			ui.Icon(c, iconCheckCircle).Size(13, 13).TextColor(p.accent).Shrink(0)
			name := info.ServerName
			if name == "" {
				name = serverHost(emby.Session{ServerURL: l.probeFor})
			}
			ui.Text(c, name).FontSize(fsCaption).FontWeight(wMedium).TextColor(p.text).SingleLine().Shrink(1).MinWidth(0)
			ui.Text(c, joinNonEmpty(" ", "·", kindLabel(info.Kind()), info.Version)).FontSize(fsCaption).
				TextColor(p.text2).SingleLine().Shrink(0)
		})
}

// wideButton is the panel's main button, across it: filled with the
// accent, and a spinner in it while busy, which disables it.
func (a *app) wideButton(c *ui.Context, key, label string, busy bool) ui.Element {
	p := a.pal
	b := ui.ButtonBase(c.Key(key)).Height(34).Radius(radiusButton).Center().Gap(8).Label(label).
		Disabled(busy).DimDisabled(false).Busy(busy)
	bg, fg := p.accent, p.onAccent
	switch {
	case busy:
		bg = p.accent.Alpha(0.7)
	case b.Pressed():
		bg = bg.MixOklab(black, 0.14)
	case b.Hovered():
		bg = bg.MixOklab(white, 0.1)
	}
	b.Background(b.AnimateColor("bg", bg, quick))
	b.Children(func() {
		if busy {
			ui.Spinner(c).FontSize(12).TextColor(fg)
		}
		ui.Text(c, label).FontSize(fsBody).FontWeight(wSemibold).TextColor(fg).SingleLine()
	})
	return b
}

// quietButton is a line of accent text that acts, under the main button.
func (a *app) quietButton(c *ui.Context, key, label string) ui.Element {
	p := a.pal
	b := ui.ButtonBase(c.Key(key)).AlignSelf(ui.Center).Height(26).PaddingX(10).Radius(6).Label(label)
	bg := ui.Transparent
	switch {
	case b.Pressed():
		bg = p.press
	case b.Hovered():
		bg = p.hover
	}
	b.Background(b.AnimateColor("bg", bg, quick))
	b.Children(func() {
		ui.Text(c, label).FontSize(fsBody).FontWeight(wMedium).TextColor(p.accent).SingleLine()
	})
	return b
}

// serverList lists the servers kept, the one used last first: a click uses
// one, or asks to sign in to one signed out of.
func (a *app) serverList(c *ui.Context, sheet bool) {
	p := a.pal
	a.authHeader(c, iconServer, str.ChooseServer, str.ChooseSubtitle)
	var chosen, removed *savedServer
	list := ui.Scroll(c.Key("servers")).MaxHeight(300).Margin(0, -8).ScrollbarAxes(false, true)
	list.Children(func() {
		ui.Column(c).Gap(2).PaddingX(8).FocusGroup(ui.Vertical).Children(func() {
			for i := range a.servers {
				s := a.servers[i]
				switch a.serverRow(c, s) {
				case rowChosen:
					chosen = &s
				case rowRemoved:
					removed = &s
				}
			}
			add := ui.ButtonBase(c.Key("add")).Height(46).PaddingX(10).Gap(12).Radius(10).
				AlignItems(ui.Center).Justify(ui.Start).Label(str.AddServer)
			a.rowBackground(add)
			add.Children(func() {
				ui.Box(c).Size(28, 28).Radius(14).Center().Background(p.fill).Shrink(0).Children(func() {
					ui.Icon(c, iconPlus).Size(15, 15).TextColor(p.text2)
				})
				ui.Text(c, str.AddServer).FontSize(fsBody).FontWeight(wMedium).TextColor(p.text).SingleLine()
			})
			if add.Clicked() {
				a.login = newLoginState()
			}
		})
	})
	if l := &a.login; l.err != "" {
		ui.Text(c.Key("error"), l.err).FontSize(fsBody).TextColor(p.danger).Margin(10, 0, 0, 0).Role(ui.RoleAlert)
	}
	if sheet {
		if a.quietButton(c, "cancel", str.Cancel).Margin(14, 0, 0, 0).Clicked() {
			a.sheetOpen = false
		}
	}
	switch {
	case chosen != nil:
		a.switchTo(*chosen)
	case removed != nil:
		a.removeServer(*removed)
	}
}

type rowAction int

const (
	rowNone rowAction = iota
	rowChosen
	rowRemoved
)

// rowBackground gives a row of the panel its washes under the pointer.
func (a *app) rowBackground(b ui.Element) {
	p := a.pal
	bg := ui.Transparent
	switch {
	case b.Pressed():
		bg = p.press
	case b.Hovered():
		bg = p.hover
	}
	b.Background(b.AnimateColor("bg", bg, quick))
}

// serverRow is a server kept, in the panel's list.
func (a *app) serverRow(c *ui.Context, s savedServer) rowAction {
	p := a.pal
	current := a.isCurrent(s.Session) && s.signedIn()
	key := s.ServerURL + "\x00" + s.UserID + "\x00" + s.UserName
	row := ui.ButtonBase(c.Key(key)).Height(52).PaddingX(10).Gap(12).Radius(10).
		AlignItems(ui.Center).Justify(ui.Start).Label(serverTitle(s.Session) + "，" + s.UserName)
	a.rowBackground(row)
	hovered := row.Hovered() || row.FocusWithin()
	action := rowNone
	row.Children(func() {
		ui.Avatar(c, s.UserName, a.img.Get(a.avatarURL(s.Session, 28))).FontSize(14).Shrink(0)
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, serverTitle(s.Session)).FontSize(fsBody).FontWeight(wMedium).TextColor(p.text).SingleLine()
			ui.Text(c, s.UserName+" · "+serverHost(s.Session)).FontSize(fsCaption).TextColor(p.text2).SingleLine()
		})
		switch {
		case current:
			ui.Row(c).Height(20).PaddingX(8).Radius(10).AlignItems(ui.Center).Background(p.accent.Alpha(0.13)).
				Shrink(0).Children(func() {
				ui.Text(c, str.CurrentServer).FontSize(fsSmall).FontWeight(wSemibold).TextColor(p.accent).SingleLine()
			})
		case hovered:
			rm := ui.ButtonBase(c.Key("remove")).Size(24, 24).Radius(12).Center().Label(str.RemoveServer).
				Tooltip(str.RemoveServer).Shrink(0)
			a.rowBackground(rm)
			rm.Children(func() { ui.Icon(c, iconX).Size(13, 13).TextColor(p.text2) })
			if rm.Clicked() {
				action = rowRemoved
			}
		case !s.signedIn():
			ui.Text(c, str.SignedOutServer).FontSize(fsCaption).TextColor(p.text3).SingleLine().Shrink(0)
		}
	})
	if action == rowNone && row.Clicked() {
		action = rowChosen
	}
	return action
}

// avatarURL is the URL of the picture of the user of s, shown dips wide,
// or "" where they have none.
func (a *app) avatarURL(s emby.Session, dips float32) string {
	if s.UserImageTag == "" || s.UserID == "" {
		return ""
	}
	cl := a.client
	if cl == nil || cl.BaseURL() != s.ServerURL {
		var err error
		if cl, err = a.newClient(s.ServerURL); err != nil {
			return ""
		}
	}
	return cl.UserImageURL(s.UserID, s.UserImageTag, emby.ImageOptions{MaxWidth: a.imgPx(dips), Quality: 90})
}
