package mcp

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientCertificateAuthenticationIsOpenAIProfileOnly(t *testing.T) {
	if RequiresClientCertificateAuthentication(BaseProfile()) {
		t.Fatal("base profile unexpectedly enables client certificate authentication")
	}
	if !RequiresClientCertificateAuthentication(OpenAIProfile()) {
		t.Fatal("OpenAI profile did not opt into client certificate authentication")
	}
}

func TestClientCertificatePolicyAuthenticatesHostWithoutReplacingBearerAuthorization(t *testing.T) {
	ca, caKey := testCertificateAuthority(t)
	client := testClientCertificate(t, ca, caKey)
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	bearerReached := false
	bearer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearerReached = true
		if r.Header.Get("Authorization") != "Bearer user-token" {
			http.Error(w, "bearer required", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler := (ClientCertificatePolicy{Roots: roots}).Middleware(bearer)

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if missing.Code != http.StatusUnauthorized || bearerReached {
		t.Fatalf("missing certificate status=%d bearer_reached=%t", missing.Code, bearerReached)
	}

	bearerReached = false
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{client}}
	withoutBearer := httptest.NewRecorder()
	handler.ServeHTTP(withoutBearer, request)
	if withoutBearer.Code != http.StatusUnauthorized || !bearerReached {
		t.Fatalf("client certificate replaced bearer auth: status=%d bearer_reached=%t", withoutBearer.Code, bearerReached)
	}

	request = httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{client}}
	request.Header.Set("Authorization", "Bearer user-token")
	accepted := httptest.NewRecorder()
	handler.ServeHTTP(accepted, request)
	if accepted.Code != http.StatusNoContent {
		t.Fatalf("combined host+bearer auth status=%d", accepted.Code)
	}
}

func testCertificateAuthority(t *testing.T) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, key
}

func testClientCertificate(t *testing.T, ca *x509.Certificate, caKey *rsa.PrivateKey) *x509.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "openai-host"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}
