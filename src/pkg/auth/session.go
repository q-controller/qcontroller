package auth

import (
	"encoding/json"
	"io"
	"time"

	authv1 "github.com/q-controller/qcontroller/src/generated/services/auth/v1"
)

// session is the browser session. IDToken/Provider are kept for
// federated logout (passing id_token_hint to the IdP).
// RefreshToken and TokenExpiry are used by Gate.browser to refresh the ID
// token when it expires.
type session struct {
	Identity     *authv1.Identity `json:"id"`
	IDToken      string           `json:"idt,omitempty"`
	RefreshToken string           `json:"rt,omitempty"`
	Provider     string           `json:"p,omitempty"`
	TokenExpiry  time.Time        `json:"exp"`
}

func writeJSONCookie(wc io.WriteCloser, v any) error {
	if err := json.NewEncoder(wc).Encode(v); err != nil {
		return err
	}
	return wc.Close()
}
