//go:build darwin

package main

/*
#cgo CFLAGS: -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <stdlib.h>
#include <string.h>

static CFStringRef kc_str(const char *s) { return CFStringCreateWithCString(NULL, s, kCFStringEncodingUTF8); }

// Replaces the item: the one there before (if any) goes, the new one is
// made by this binary - which is then the one application it trusts.
static OSStatus kc_put(const char *service, const char *account, const char *label, const void *secret, int n) {
	CFStringRef svc = kc_str(service), acct = kc_str(account), lab = kc_str(label);
	CFDataRef data = CFDataCreate(NULL, secret, n);
	const void *qk[] = { kSecClass, kSecAttrService, kSecUseAuthenticationUI };
	const void *qv[] = { kSecClassGenericPassword, svc, kSecUseAuthenticationUIFail };
	CFDictionaryRef q = CFDictionaryCreate(NULL, qk, qv, 3, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	SecItemDelete(q);
	CFRelease(q);
	const void *ak[] = { kSecClass, kSecAttrService, kSecAttrAccount, kSecAttrLabel, kSecValueData };
	const void *av[] = { kSecClassGenericPassword, svc, acct, lab, data };
	CFDictionaryRef a = CFDictionaryCreate(NULL, ak, av, 5, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	OSStatus st = SecItemAdd(a, NULL);
	CFRelease(a);
	CFRelease(data);
	CFRelease(lab);
	CFRelease(acct);
	CFRelease(svc);
	return st;
}

// The item's account and secret, malloc'd. Never asks the person at the
// screen: an item this binary may not read fails instead of putting up a
// dialog that would hold the program until someone answered it.
static OSStatus kc_get(const char *service, char **account, char **secret, int *n) {
	*account = NULL; *secret = NULL; *n = 0;
	CFStringRef svc = kc_str(service);
	const void *qk[] = { kSecClass, kSecAttrService, kSecReturnData, kSecReturnAttributes, kSecMatchLimit, kSecUseAuthenticationUI };
	const void *qv[] = { kSecClassGenericPassword, svc, kCFBooleanTrue, kCFBooleanTrue, kSecMatchLimitOne, kSecUseAuthenticationUIFail };
	CFDictionaryRef q = CFDictionaryCreate(NULL, qk, qv, 6, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFTypeRef out = NULL;
	OSStatus st = SecItemCopyMatching(q, &out);
	CFRelease(q);
	CFRelease(svc);
	if (st != errSecSuccess || out == NULL) return st;
	CFDictionaryRef d = (CFDictionaryRef)out;
	CFDataRef data = CFDictionaryGetValue(d, kSecValueData);
	CFStringRef acct = CFDictionaryGetValue(d, kSecAttrAccount);
	if (data) {
		*n = (int)CFDataGetLength(data);
		*secret = malloc(*n + 1);
		memcpy(*secret, CFDataGetBytePtr(data), *n);
		(*secret)[*n] = 0;
	}
	if (acct) {
		CFIndex max = CFStringGetMaximumSizeForEncoding(CFStringGetLength(acct), kCFStringEncodingUTF8) + 1;
		*account = malloc(max);
		if (!CFStringGetCString(acct, *account, max, kCFStringEncodingUTF8)) (*account)[0] = 0;
	}
	CFRelease(out);
	return errSecSuccess;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// The keychain item the password is kept in (the login keychain).
const (
	// An item belongs to the program that made it: the app signed with a
	// lasting identity (scripts/kakaotalk-relogin) keeps it across rebuilds,
	// the first ad-hoc build's item ("jarvis-kakaotalk-login") it cannot read.
	kcService = "jarvis-kakaotalk-relogin"
	kcLabel   = "jarvis 카톡 자동 로그인"
)

var errNoPassword = errors.New("저장된 비밀번호가 없습니다")

// errSecItemNotFound, as Security.framework numbers it.
const errSecItemNotFound = -25300

func putPassword(account, password string) error {
	cs, ca, cl := C.CString(kcService), C.CString(account), C.CString(kcLabel)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(ca))
	defer C.free(unsafe.Pointer(cl))
	secret := C.CBytes([]byte(password))
	defer func() {
		// The copy handed to C is wiped before it is let go.
		C.memset(secret, 0, C.size_t(len(password)))
		C.free(secret)
	}()
	if st := C.kc_put(cs, ca, cl, secret, C.int(len(password))); st != 0 {
		return fmt.Errorf("키체인에 넣지 못했습니다 (OSStatus %d)", int(st))
	}
	return nil
}

func getPassword() (account, password string, err error) {
	cs := C.CString(kcService)
	defer C.free(unsafe.Pointer(cs))
	var ca, cp *C.char
	var n C.int
	st := C.kc_get(cs, &ca, &cp, &n)
	if ca != nil {
		account = C.GoString(ca)
		C.free(unsafe.Pointer(ca))
	}
	if cp != nil {
		password = C.GoStringN(cp, n)
		C.memset(unsafe.Pointer(cp), 0, C.size_t(n))
		C.free(unsafe.Pointer(cp))
	}
	switch {
	case int(st) == errSecItemNotFound:
		return "", "", errNoPassword
	case st != 0:
		return "", "", fmt.Errorf("키체인에서 읽지 못했습니다 (OSStatus %d) - 다시 저장하세요", int(st))
	}
	return account, password, nil
}
