package ui

import "testing"

func TestHostedPlatformViewClosesAllRegistrations(t *testing.T) {
	v := HostPlatformView(1234)
	view := func(c *Context) { NativeView(c, v).Fill() }
	a, b := NewTester(view, 320, 240), NewTester(view, 640, 480)
	a.Frame()
	b.Frame()
	if len(a.rt.platformViews) != 1 || len(b.rt.platformViews) != 1 {
		t.Fatal("the view was not registered in both windows")
	}
	v.Close()
	v.Close()
	if v.NativeView() != 0 {
		t.Fatal("closed view still exposes a native handle")
	}
	a.Frame()
	b.Frame()
	if len(a.rt.platformViews) != 0 || len(b.rt.platformViews) != 0 {
		t.Fatal("closing retained a toolkit registration")
	}
}

func TestHostedPlatformViewCloseNotifiesOnce(t *testing.T) {
	v := HostPlatformView(42)
	calls := 0
	v.onNativeViewClose(func() { calls++ })
	v.Close()
	v.Close()
	if calls != 1 {
		t.Fatalf("close callback ran %d times", calls)
	}
}
