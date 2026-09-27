package secretstore

import (
	"errors"
	"strings"
	"testing"
)

func TestStoreMemoryRoundTrip(t *testing.T) {
	cleanup := UseMemoryForTesting()
	defer cleanup()
	store := New(t.TempDir())
	name := Name("tunnel", "runtime")
	if err := store.Set(name, "secret"); err != nil {
		t.Fatal(err)
	}
	if value, err := store.Get(name); err != nil || value != "secret" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	if err := store.Set(name, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestStoreApplyRollsBack(t *testing.T) {
	backend := &failingBackend{memoryBackend: newMemoryBackend(), failAccount: Name("second")}
	store := &Store{service: "test", backend: backend}
	if err := store.Set(Name("first"), "old"); err != nil {
		t.Fatal(err)
	}
	err := store.Apply([]Change{{Name: Name("first"), Value: "new"}, {Name: Name("second"), Value: "value"}})
	if err == nil {
		t.Fatal("expected failure")
	}
	if value, getErr := store.Get(Name("first")); getErr != nil || value != "old" {
		t.Fatalf("rollback value=%q err=%v", value, getErr)
	}
}

func TestCanonicalServiceNamespaceAndAccountNames(t *testing.T) {
	store := New(t.TempDir())
	if !strings.HasPrefix(store.service, "codemcp/") {
		t.Fatalf("service namespace=%q", store.service)
	}
	if strings.Contains(store.service, "chatgpt-mcp") {
		t.Fatalf("legacy service namespace leaked into %q", store.service)
	}

	tests := []struct {
		domain Domain
		parts  []string
	}{
		{DomainAuth, []string{"mcp-token"}},
		{DomainTunnel, []string{"runtime-key"}},
		{DomainOAuth, []string{"provider-id", "access-token"}},
		{DomainUpstream, []string{"server-id", "header", "Authorization"}},
		{DomainCluster, []string{"relay-token"}},
	}
	for _, test := range tests {
		got := AccountName(test.domain, test.parts...)
		wantParts := append([]string{string(test.domain)}, test.parts...)
		if want := Name(wantParts...); got != want {
			t.Fatalf("account name domain=%q got=%q want=%q", test.domain, got, want)
		}
		if strings.Contains(got, "raw-secret-value") {
			t.Fatalf("account name contains secret material: %q", got)
		}
	}
}

func TestStoreRejectsEmptyRootAsUnavailable(t *testing.T) {
	store := New("   ")
	_, err := store.Get(AccountName(DomainTunnel, "runtime-key"))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err=%v", err)
	}
	var storeErr *Error
	if !errors.As(err, &storeErr) || storeErr.Operation != "read" {
		t.Fatalf("typed error=%#v err=%v", storeErr, err)
	}
}

func TestStoreBackendFailureIsTypedAndDiagnosable(t *testing.T) {
	account := AccountName(DomainOAuth, "provider-id", "access-token")
	store := &Store{service: "codemcp/test", backend: readFailBackend{err: errors.New("backend offline")}}
	_, err := store.Get(account)
	var storeErr *Error
	if !errors.As(err, &storeErr) {
		t.Fatalf("error type=%T err=%v", err, err)
	}
	if storeErr.Operation != "read" || storeErr.Account != account {
		t.Fatalf("typed error=%#v", storeErr)
	}
	if !strings.Contains(storeErr.Error(), "backend offline") {
		t.Fatalf("diagnostic=%q", storeErr.Error())
	}
}

type failingBackend struct {
	*memoryBackend
	failAccount string
}

func (f *failingBackend) Set(service, account, value string) error {
	if account == f.failAccount {
		return errors.New("boom")
	}
	return f.memoryBackend.Set(service, account, value)
}

type readFailBackend struct{ err error }

func (b readFailBackend) Set(string, string, string) error { return b.err }
func (b readFailBackend) Get(string, string) (string, error) {
	return "", b.err
}
func (b readFailBackend) Delete(string, string) error { return b.err }
