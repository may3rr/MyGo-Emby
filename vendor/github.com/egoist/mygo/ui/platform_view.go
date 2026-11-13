package ui

// PlatformViewHandle registers an externally owned platform view with NativeView.
// Create, show and close it on the main thread. Close detaches every toolkit
// host; the caller must then release its own native object.
type PlatformViewHandle struct {
	handle uintptr
	closed []func()
}

// HostPlatformView wraps an NSView* on macOS for NativeView. It does not retain
// the object: the caller keeps it alive until after Close.
func HostPlatformView(handle uintptr) *PlatformViewHandle {
	return &PlatformViewHandle{handle: handle}
}

func (v *PlatformViewHandle) NativeView() uintptr { return v.handle }

func (v *PlatformViewHandle) onNativeViewClose(fn func()) { v.closed = append(v.closed, fn) }

// Close detaches the view from windows before its owner releases it.
func (v *PlatformViewHandle) Close() {
	if v.handle == 0 {
		return
	}
	v.handle = 0
	for _, fn := range v.closed {
		fn()
	}
	v.closed = nil
}
