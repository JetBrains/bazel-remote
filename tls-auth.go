package main

import (
	"fmt"
	"net/http"

	"github.com/abbot/go-http-auth"
)

type TLSAuthenticator struct {
}

func (t *TLSAuthenticator) Wrap(wrapped auth.AuthenticatedHandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if username, problem := t.VerifyCert(r); problem != "" {
			t.RequireAuth(w, problem)
		} else {
			ar := &auth.AuthenticatedRequest{Request: *r, Username: username}
			wrapped(w, ar)
		}
	}
}

func (t *TLSAuthenticator) RequireAuth(w http.ResponseWriter, problem string) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(fmt.Sprintf("%d %s: %s\n", http.StatusUnauthorized, "TLS Certificate Required", problem)))
}

func (t *TLSAuthenticator) VerifyCert(r *http.Request) (string, string) {
	if r.TLS == nil {
		return "", "missing TLS connection info"
	}

	if len(r.TLS.VerifiedChains) == 0 || len(r.TLS.VerifiedChains[0]) == 0 {
		return "", "no valid client certificate"
	}

	return r.TLS.VerifiedChains[0][0].Subject.CommonName, ""
}
