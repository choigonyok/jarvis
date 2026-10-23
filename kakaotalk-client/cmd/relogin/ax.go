//go:build darwin

package main

/*
#cgo CFLAGS: -Wno-deprecated-declarations
#cgo LDFLAGS: -framework ApplicationServices -framework CoreFoundation -framework CoreGraphics
#include <ApplicationServices/ApplicationServices.h>
#include <stdlib.h>
#include <unistd.h>

static CFStringRef cfstr(const char *s) { return CFStringCreateWithCString(NULL, s, kCFStringEncodingUTF8); }

// Whether this process may use the accessibility API; with prompt, macOS
// asks the person at the screen to allow it (System Settings).
static int ax_trusted(int prompt) {
	if (!prompt) return AXIsProcessTrusted() ? 1 : 0;
	const void *keys[] = { kAXTrustedCheckOptionPrompt };
	const void *vals[] = { kCFBooleanTrue };
	CFDictionaryRef opts = CFDictionaryCreate(NULL, keys, vals, 1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	Boolean ok = AXIsProcessTrustedWithOptions(opts);
	CFRelease(opts);
	return ok ? 1 : 0;
}

static AXUIElementRef ax_app(pid_t pid) {
	AXUIElementRef app = AXUIElementCreateApplication(pid);
	// A busy app is not waited on for long: the next look tries again.
	if (app) AXUIElementSetMessagingTimeout(app, 2.0);
	return app;
}

static int ax_isnull(AXUIElementRef el) { return el == NULL; }
static void ax_release(AXUIElementRef el) { if (el) CFRelease(el); }

// A string attribute as malloc'd UTF-8, or NULL.
static char *ax_string(AXUIElementRef el, const char *name) {
	CFStringRef attr = cfstr(name);
	CFTypeRef v = NULL;
	AXError err = AXUIElementCopyAttributeValue(el, attr, &v);
	CFRelease(attr);
	if (err != kAXErrorSuccess || v == NULL) return NULL;
	char *out = NULL;
	if (CFGetTypeID(v) == CFStringGetTypeID()) {
		CFIndex max = CFStringGetMaximumSizeForEncoding(CFStringGetLength((CFStringRef)v), kCFStringEncodingUTF8) + 1;
		out = malloc(max);
		if (!CFStringGetCString((CFStringRef)v, out, max, kCFStringEncodingUTF8)) { free(out); out = NULL; }
	}
	CFRelease(v);
	return out;
}

// A boolean (or number) attribute: 1, 0, or -1 when there is none.
static int ax_bool(AXUIElementRef el, const char *name) {
	CFStringRef attr = cfstr(name);
	CFTypeRef v = NULL;
	AXError err = AXUIElementCopyAttributeValue(el, attr, &v);
	CFRelease(attr);
	if (err != kAXErrorSuccess || v == NULL) return -1;
	int out = -1;
	if (CFGetTypeID(v) == CFBooleanGetTypeID()) out = CFBooleanGetValue((CFBooleanRef)v) ? 1 : 0;
	else if (CFGetTypeID(v) == CFNumberGetTypeID()) { int n = 0; CFNumberGetValue((CFNumberRef)v, kCFNumberIntType, &n); out = n ? 1 : 0; }
	CFRelease(v);
	return out;
}

// The elements of an array attribute (AXWindows, AXChildren), at most max,
// each retained for the caller; *n is how many.
static AXUIElementRef *ax_list(AXUIElementRef el, const char *name, int max, int *n) {
	*n = 0;
	CFStringRef attr = cfstr(name);
	CFTypeRef v = NULL;
	AXError err = AXUIElementCopyAttributeValue(el, attr, &v);
	CFRelease(attr);
	if (err != kAXErrorSuccess || v == NULL) return NULL;
	if (CFGetTypeID(v) != CFArrayGetTypeID()) { CFRelease(v); return NULL; }
	CFIndex count = CFArrayGetCount((CFArrayRef)v);
	if (count > max) count = max;
	AXUIElementRef *out = calloc(count > 0 ? count : 1, sizeof(AXUIElementRef));
	int k = 0;
	for (CFIndex i = 0; i < count; i++) {
		CFTypeRef item = CFArrayGetValueAtIndex((CFArrayRef)v, i);
		if (item && CFGetTypeID(item) == AXUIElementGetTypeID()) { CFRetain(item); out[k++] = (AXUIElementRef)item; }
	}
	CFRelease(v);
	*n = k;
	return out;
}

static AXUIElementRef ax_at(AXUIElementRef *list, int i) { return list[i]; }

// An element attribute (AXMenuBar), retained for the caller, or NULL.
static AXUIElementRef ax_elem(AXUIElementRef el, const char *name) {
	CFStringRef attr = cfstr(name);
	CFTypeRef v = NULL;
	AXError err = AXUIElementCopyAttributeValue(el, attr, &v);
	CFRelease(attr);
	if (err != kAXErrorSuccess || v == NULL) return NULL;
	if (CFGetTypeID(v) != AXUIElementGetTypeID()) { CFRelease(v); return NULL; }
	return (AXUIElementRef)v;
}

static int ax_same(AXUIElementRef a, AXUIElementRef b) { return CFEqual(a, b) ? 1 : 0; }

// Whether this session's screen is locked: 1, 0, or -1 when there is no
// session to ask.
static int screen_locked(void) {
	CFDictionaryRef d = CGSessionCopyCurrentDictionary();
	if (d == NULL) return -1;
	int out = 0;
	CFTypeRef v = CFDictionaryGetValue(d, CFSTR("CGSSessionScreenIsLocked"));
	if (v != NULL && CFGetTypeID(v) == CFBooleanGetTypeID() && CFBooleanGetValue((CFBooleanRef)v)) out = 1;
	CFRelease(d);
	return out;
}

static int ax_action(AXUIElementRef el, const char *name) {
	CFStringRef a = cfstr(name);
	AXError err = AXUIElementPerformAction(el, a);
	CFRelease(a);
	return (int)err;
}

static int ax_set_string(AXUIElementRef el, const char *name, const char *value) {
	CFStringRef attr = cfstr(name), v = cfstr(value);
	AXError err = AXUIElementSetAttributeValue(el, attr, v);
	CFRelease(attr);
	CFRelease(v);
	return (int)err;
}

static int ax_set_bool(AXUIElementRef el, const char *name, int on) {
	CFStringRef attr = cfstr(name);
	AXError err = AXUIElementSetAttributeValue(el, attr, on ? kCFBooleanTrue : kCFBooleanFalse);
	CFRelease(attr);
	return (int)err;
}

// Characters typed into the process, a key-down and key-up for each.
static void key_text(pid_t pid, const UniChar *chars, int n) {
	for (int i = 0; i < n; i++) {
		CGEventRef down = CGEventCreateKeyboardEvent(NULL, 0, true);
		CGEventRef up = CGEventCreateKeyboardEvent(NULL, 0, false);
		CGEventKeyboardSetUnicodeString(down, 1, &chars[i]);
		CGEventKeyboardSetUnicodeString(up, 1, &chars[i]);
		CGEventPostToPid(pid, down);
		CGEventPostToPid(pid, up);
		CFRelease(down);
		CFRelease(up);
		usleep(15000);
	}
}

// One key by its virtual key code, with ⌘ when cmd.
static void key_code(pid_t pid, int code, int cmd) {
	CGEventRef down = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)code, true);
	CGEventRef up = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)code, false);
	if (cmd) {
		CGEventSetFlags(down, kCGEventFlagMaskCommand);
		CGEventSetFlags(up, kCGEventFlagMaskCommand);
	}
	CGEventPostToPid(pid, down);
	CGEventPostToPid(pid, up);
	CFRelease(down);
	CFRelease(up);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"unicode/utf16"
	"unsafe"
)

type axRef = C.AXUIElementRef

// Virtual key codes (Carbon's kVK_*).
const (
	keyA      = 0
	keyReturn = 36
	keyDelete = 51
)

func axTrusted(prompt bool) bool {
	p := 0
	if prompt {
		p = 1
	}
	return C.ax_trusted(C.int(p)) == 1
}

// look is one reading of an app's windows. Every handle it took is let go
// by close: the program reads every five seconds for as long as it runs.
type look struct {
	pid     int
	app     axRef
	windows []*elem
	held    []axRef
	// blind is a window that came back as the app itself (see view.Blind).
	blind bool
}

// limits on one look, every five seconds: the login window and a notice sit
// near the top of their window, and a logged-in window full of chats needs no
// more than enough to see there is no login form in it.
const (
	maxDepth = 8
	maxNodes = 300
)

func readApp(pid int) (*look, error) {
	app := C.ax_app(C.pid_t(pid))
	if C.ax_isnull(app) != 0 {
		return nil, errors.New("앱에 닿지 못했습니다")
	}
	l := &look{pid: pid, app: app}
	budget := maxNodes
	for _, w := range l.list(app, "AXWindows", 20) {
		if C.ax_same(w, app) != 0 {
			// The app in place of a window: that is how macOS hides windows
			// while the screen is locked. Its children are the app again, endlessly.
			l.blind = true
			continue
		}
		l.windows = append(l.windows, l.walk(w, 0, &budget))
	}
	return l, nil
}

// readMenu is what KakaoTalk's own app menu says of the login - its menu bar
// item 카카오톡, never the Apple menu. Menus are read even while the screen
// is locked and every window is hidden, and cheaply: a dozen items.
func readMenu(pid int) menuState {
	app := C.ax_app(C.pid_t(pid))
	if C.ax_isnull(app) != 0 {
		return menuUnknown
	}
	l := &look{pid: pid, app: app}
	defer l.close()
	bar, ok := l.elem(app, "AXMenuBar")
	if !ok {
		return menuUnknown
	}
	var items []*elem
	for _, it := range l.list(bar, "AXChildren", 20) {
		if !isAppMenu(axString(it, "AXTitle")) {
			continue
		}
		budget := 80
		items = append(items, l.walk(it, 0, &budget))
	}
	return menuFrom(items)
}

// screenLocked is whether the Mac's screen is locked. While it is, macOS
// hands this program the app itself for every window of every app, so no
// login window can be read, and nothing is typed.
func screenLocked() bool { return C.screen_locked() == 1 }

// elem is an element attribute of el, held until close.
func (l *look) elem(el axRef, name string) (axRef, bool) {
	cn := C.CString(name)
	defer C.free(unsafe.Pointer(cn))
	r := C.ax_elem(el, cn)
	if C.ax_isnull(r) != 0 {
		return r, false
	}
	l.held = append(l.held, r)
	return r, true
}

func (l *look) close() {
	for _, r := range l.held {
		C.ax_release(r)
	}
	l.held = nil
	C.ax_release(l.app)
}

func (l *look) list(el axRef, name string, max int) []axRef {
	cn := C.CString(name)
	defer C.free(unsafe.Pointer(cn))
	var n C.int
	arr := C.ax_list(el, cn, C.int(max), &n)
	if arr == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(arr))
	out := make([]axRef, int(n))
	for i := range out {
		out[i] = C.ax_at(arr, C.int(i))
		l.held = append(l.held, out[i])
	}
	return out
}

func axString(el axRef, name string) string {
	cn := C.CString(name)
	defer C.free(unsafe.Pointer(cn))
	p := C.ax_string(el, cn)
	if p == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(p))
	return C.GoString(p)
}

func axBool(el axRef, name string) int {
	cn := C.CString(name)
	defer C.free(unsafe.Pointer(cn))
	return int(C.ax_bool(el, cn))
}

func (l *look) walk(r axRef, depth int, budget *int) *elem {
	*budget--
	e := &elem{
		ref:     r,
		Role:    axString(r, "AXRole"),
		Subrole: axString(r, "AXSubrole"),
		Title:   axString(r, "AXTitle"),
		Desc:    axString(r, "AXDescription"),
		Enabled: axBool(r, "AXEnabled"),
	}
	switch {
	case e.Role == "AXCheckBox":
		e.Value = fmt.Sprint(axBool(r, "AXValue"))
	case e.Role == "AXStaticText", e.Role == "AXTextField" && e.Subrole != "AXSecureTextField":
		e.Value = axString(r, "AXValue")
	}
	if depth < maxDepth {
		for _, k := range l.list(r, "AXChildren", 200) {
			if *budget <= 0 {
				break
			}
			e.Kids = append(e.Kids, l.walk(k, depth+1, budget))
		}
	}
	return e
}

func axErr(code C.int, what string) error {
	if code == 0 {
		return nil
	}
	return fmt.Errorf("%s: AXError %d", what, int(code))
}

func press(e *elem) error {
	cn := C.CString("AXPress")
	defer C.free(unsafe.Pointer(cn))
	return axErr(C.ax_action(e.ref, cn), "누르기")
}

func raise(e *elem) error {
	cn := C.CString("AXRaise")
	defer C.free(unsafe.Pointer(cn))
	return axErr(C.ax_action(e.ref, cn), "앞으로")
}

func setValue(e *elem, v string) error {
	cn, cv := C.CString("AXValue"), C.CString(v)
	defer C.free(unsafe.Pointer(cn))
	defer C.free(unsafe.Pointer(cv))
	return axErr(C.ax_set_string(e.ref, cn, cv), "값 넣기")
}

func setBool(r axRef, name string, on bool) error {
	cn := C.CString(name)
	defer C.free(unsafe.Pointer(cn))
	v := 0
	if on {
		v = 1
	}
	return axErr(C.ax_set_bool(r, cn, C.int(v)), name)
}

func typeText(pid int, s string) {
	chars := utf16.Encode([]rune(s))
	if len(chars) == 0 {
		return
	}
	C.key_text(C.pid_t(pid), (*C.UniChar)(unsafe.Pointer(&chars[0])), C.int(len(chars)))
}

func key(pid, code int, cmd bool) {
	c := 0
	if cmd {
		c = 1
	}
	C.key_code(C.pid_t(pid), C.int(code), C.int(c))
}
