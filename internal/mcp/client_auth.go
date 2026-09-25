package mcp

import (
	"crypto/x509"
	"errors"
	"net/http"
)

type ClientCertificatePolicy struct {
	Roots *x509.CertPool
}

func (policy ClientCertificatePolicy) Middleware(next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if policy.Roots == nil {
			next.ServeHTTP(w, r)
			return
		}
		if err := policy.VerifyRequest(r); err != nil {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (policy ClientCertificatePolicy) VerifyRequest(r *http.Request) error {
	if policy.Roots == nil {
		return nil
	}
	if r == nil || r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return errors.New("client certificate is required")
	}
	leaf := r.TLS.PeerCertificates[0]
	intermediates := x509.NewCertPool()
	for _, certificate := range r.TLS.PeerCertificates[1:] {
		intermediates.AddCert(certificate)
	}
	_, err := leaf.Verify(x509.VerifyOptions{
		Roots:         policy.Roots,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	return err
}
