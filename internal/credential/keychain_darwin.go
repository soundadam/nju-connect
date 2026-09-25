//go:build darwin && cgo

package credential

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdlib.h>

static CFStringRef soundconnect_string(const void *bytes, CFIndex length) {
  return CFStringCreateWithBytes(
      kCFAllocatorDefault, bytes, length, kCFStringEncodingUTF8, false);
}

static CFDictionaryRef soundconnect_query(
    CFStringRef service, CFStringRef account, CFTypeRef return_value) {
  const void *keys[] = {
      kSecClass, kSecAttrService, kSecAttrAccount, kSecMatchLimit,
      return_value == kCFBooleanTrue ? kSecReturnData : kSecReturnAttributes};
  const void *values[] = {
      kSecClassGenericPassword, service, account, kSecMatchLimitOne,
      kCFBooleanTrue};
  return CFDictionaryCreate(
      kCFAllocatorDefault, keys, values, 5,
      &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
}

static OSStatus soundconnect_keychain_inspect(
    const void *service_bytes, CFIndex service_len,
    const void *account_bytes, CFIndex account_len) {
  CFStringRef service = soundconnect_string(service_bytes, service_len);
  CFStringRef account = soundconnect_string(account_bytes, account_len);
  if (service == NULL || account == NULL) {
    if (service != NULL) CFRelease(service);
    if (account != NULL) CFRelease(account);
    return errSecAllocate;
  }
  CFDictionaryRef query = soundconnect_query(service, account, kCFBooleanFalse);
  CFTypeRef result = NULL;
  OSStatus status = SecItemCopyMatching(query, &result);
  if (result != NULL) CFRelease(result);
  CFRelease(query);
  CFRelease(account);
  CFRelease(service);
  return status;
}

static OSStatus soundconnect_keychain_get(
    const void *service_bytes, CFIndex service_len,
    const void *account_bytes, CFIndex account_len,
    void **secret, CFIndex *secret_len) {
  CFStringRef service = soundconnect_string(service_bytes, service_len);
  CFStringRef account = soundconnect_string(account_bytes, account_len);
  if (service == NULL || account == NULL) {
    if (service != NULL) CFRelease(service);
    if (account != NULL) CFRelease(account);
    return errSecAllocate;
  }
  CFDictionaryRef query = soundconnect_query(service, account, kCFBooleanTrue);
  CFTypeRef result = NULL;
  OSStatus status = SecItemCopyMatching(query, &result);
  if (status == errSecSuccess) {
    CFDataRef data = (CFDataRef)result;
    *secret_len = CFDataGetLength(data);
    *secret = malloc((size_t)*secret_len);
    if (*secret == NULL && *secret_len > 0) {
      status = errSecAllocate;
    } else if (*secret_len > 0) {
      CFDataGetBytes(data, CFRangeMake(0, *secret_len), *secret);
    }
  }
  if (result != NULL) CFRelease(result);
  CFRelease(query);
  CFRelease(account);
  CFRelease(service);
  return status;
}

static OSStatus soundconnect_keychain_set(
    const void *service_bytes, CFIndex service_len,
    const void *account_bytes, CFIndex account_len,
    const void *secret_bytes, CFIndex secret_len) {
  CFStringRef service = soundconnect_string(service_bytes, service_len);
  CFStringRef account = soundconnect_string(account_bytes, account_len);
  CFDataRef secret = CFDataCreate(kCFAllocatorDefault, secret_bytes, secret_len);
  if (service == NULL || account == NULL || secret == NULL) {
    if (service != NULL) CFRelease(service);
    if (account != NULL) CFRelease(account);
    if (secret != NULL) CFRelease(secret);
    return errSecAllocate;
  }

  const void *query_keys[] = {kSecClass, kSecAttrService, kSecAttrAccount};
  const void *query_values[] = {kSecClassGenericPassword, service, account};
  CFDictionaryRef query = CFDictionaryCreate(
      kCFAllocatorDefault, query_keys, query_values, 3,
      &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
  const void *update_keys[] = {kSecValueData};
  const void *update_values[] = {secret};
  CFDictionaryRef update = CFDictionaryCreate(
      kCFAllocatorDefault, update_keys, update_values, 1,
      &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);

  OSStatus status = SecItemUpdate(query, update);
  if (status == errSecItemNotFound) {
    const void *add_keys[] = {
        kSecClass, kSecAttrService, kSecAttrAccount, kSecValueData};
    const void *add_values[] = {
        kSecClassGenericPassword, service, account, secret};
    CFDictionaryRef add = CFDictionaryCreate(
        kCFAllocatorDefault, add_keys, add_values, 4,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    status = SecItemAdd(add, NULL);
    CFRelease(add);
  }

  CFRelease(update);
  CFRelease(query);
  CFRelease(secret);
  CFRelease(account);
  CFRelease(service);
  return status;
}

static OSStatus soundconnect_keychain_delete(
    const void *service_bytes, CFIndex service_len,
    const void *account_bytes, CFIndex account_len) {
  CFStringRef service = soundconnect_string(service_bytes, service_len);
  CFStringRef account = soundconnect_string(account_bytes, account_len);
  if (service == NULL || account == NULL) {
    if (service != NULL) CFRelease(service);
    if (account != NULL) CFRelease(account);
    return errSecAllocate;
  }
  const void *query_keys[] = {kSecClass, kSecAttrService, kSecAttrAccount};
  const void *query_values[] = {kSecClassGenericPassword, service, account};
  CFDictionaryRef query = CFDictionaryCreate(
      kCFAllocatorDefault, query_keys, query_values, 3,
      &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
  OSStatus status = SecItemDelete(query);
  CFRelease(query);
  CFRelease(account);
  CFRelease(service);
  return status;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"unsafe"
)

const (
	errSecSuccess      = 0
	errSecUserCanceled = -128
	errSecItemNotFound = -25300
)

type keychainBackend interface {
	inspect(service, account string) error
	get(service, account string) ([]byte, error)
	set(service, account string, secret []byte) error
	delete(service, account string) error
}

type KeychainStore struct {
	service string
	account string
	backend keychainBackend
}

func newKeychainStore(service, account string, backend keychainBackend) *KeychainStore {
	return &KeychainStore{service: service, account: account, backend: backend}
}

func (s *KeychainStore) validate() error {
	if s == nil || s.backend == nil || s.service == "" || s.account == "" {
		return errors.New("keychain backend is not initialized")
	}
	return nil
}

func (s *KeychainStore) Inspect() error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.backend.inspect(s.service, s.account)
}

func (s *KeychainStore) Get() ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	secret, err := s.backend.get(s.service, s.account)
	if err != nil {
		return nil, err
	}
	if err := validateSecret(secret); err != nil {
		clearBytes(secret)
		return nil, err
	}
	return secret, nil
}

func (s *KeychainStore) Set(secret []byte) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := validateSecret(secret); err != nil {
		return err
	}
	return s.backend.set(s.service, s.account, secret)
}

// Clear removes this Keychain item. Missing items are treated as success so
// logout is idempotent.
func (s *KeychainStore) Clear() error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.backend.delete(s.service, s.account); errors.Is(err, os.ErrNotExist) {
		return nil
	} else {
		return err
	}
}

type systemKeychainBackend struct{}

func (systemKeychainBackend) inspect(service, account string) error {
	serviceBytes := []byte(service)
	accountBytes := []byte(account)
	status := C.soundconnect_keychain_inspect(
		unsafe.Pointer(&serviceBytes[0]), C.CFIndex(len(serviceBytes)),
		unsafe.Pointer(&accountBytes[0]), C.CFIndex(len(accountBytes)),
	)
	return keychainStatus("inspect", int32(status))
}

func (systemKeychainBackend) get(service, account string) ([]byte, error) {
	serviceBytes := []byte(service)
	accountBytes := []byte(account)
	var secretLength C.CFIndex
	var secretPointer unsafe.Pointer
	status := C.soundconnect_keychain_get(
		unsafe.Pointer(&serviceBytes[0]), C.CFIndex(len(serviceBytes)),
		unsafe.Pointer(&accountBytes[0]), C.CFIndex(len(accountBytes)),
		&secretPointer, &secretLength,
	)
	if err := keychainStatus("read", int32(status)); err != nil {
		return nil, err
	}
	defer C.free(secretPointer)
	if uint64(secretLength) > uint64(maxCredentialBytes) {
		return nil, ErrCredentialTooLarge
	}
	return C.GoBytes(secretPointer, C.int(secretLength)), nil
}

func (systemKeychainBackend) set(service, account string, secret []byte) error {
	serviceBytes := []byte(service)
	accountBytes := []byte(account)
	status := C.soundconnect_keychain_set(
		unsafe.Pointer(&serviceBytes[0]), C.CFIndex(len(serviceBytes)),
		unsafe.Pointer(&accountBytes[0]), C.CFIndex(len(accountBytes)),
		unsafe.Pointer(&secret[0]), C.CFIndex(len(secret)),
	)
	return keychainStatus("store", int32(status))
}

func (systemKeychainBackend) delete(service, account string) error {
	serviceBytes := []byte(service)
	accountBytes := []byte(account)
	status := C.soundconnect_keychain_delete(
		unsafe.Pointer(&serviceBytes[0]), C.CFIndex(len(serviceBytes)),
		unsafe.Pointer(&accountBytes[0]), C.CFIndex(len(accountBytes)),
	)
	return keychainStatus("delete", int32(status))
}

func keychainStatus(operation string, status int32) error {
	switch status {
	case errSecSuccess:
		return nil
	case errSecItemNotFound:
		return fmt.Errorf("%s Keychain credential: %w", operation, os.ErrNotExist)
	case errSecUserCanceled:
		return fmt.Errorf("%s Keychain credential: %w", operation, ErrKeychainAccessCanceled)
	default:
		return fmt.Errorf("%s Keychain credential: OSStatus %d", operation, status)
	}
}
