package telephony

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"
)

const sipDigestAlgorithm = "SHA-256"
const sipDigestLifetime = 5 * time.Minute

type sipDigestUser struct {
	HA1     string `json:"ha1"`
	Persona string `json:"persona"`
}
type sipDigestAuth struct {
	realm  string
	users  map[string]sipDigestUser
	mu     sync.Mutex
	nonces map[string]time.Time
	counts map[string]int
}

func loadSIPDigestAuth() (*sipDigestAuth, error) {
	encoded, realm := os.Getenv("SIP_DIGEST_USERS"), strings.TrimSpace(os.Getenv("SIP_DIGEST_REALM"))
	if encoded == "" || realm == "" {
		return nil, fmt.Errorf("SIP digest auth requires SIP_DIGEST_USERS and SIP_DIGEST_REALM")
	}
	users := map[string]sipDigestUser{}
	if json.Unmarshal([]byte(encoded), &users) != nil || len(users) == 0 {
		return nil, fmt.Errorf("invalid SIP digest user database")
	}
	for username, user := range users {
		if username == "" || user.Persona == "" || len(user.HA1) != 64 {
			return nil, fmt.Errorf("invalid SIP digest user database")
		}
		if _, err := hex.DecodeString(user.HA1); err != nil {
			return nil, fmt.Errorf("invalid SIP digest user database")
		}
	}
	return &sipDigestAuth{realm: realm, users: users, nonces: map[string]time.Time{}, counts: map[string]int{}}, nil
}

func (a *sipDigestAuth) middleware(s *Server) func(sipgo.RequestHandler) sipgo.RequestHandler {
	return func(next sipgo.RequestHandler) sipgo.RequestHandler {
		return func(req *sip.Request, tx sip.ServerTransaction) {
			if req.Method != sip.INVITE {
				next(req, tx)
				return
			}
			if !s.Config.allowed(req.Source()) {
				_ = tx.Respond(sip.NewResponseFromRequest(req, sip.StatusForbidden, "Forbidden", nil))
				return
			}
			header := req.GetHeader("Authorization")
			if header == nil {
				a.challenge(req, tx)
				return
			}
			cred, err := digest.ParseCredentials(header.Value())
			if err != nil || !a.valid(req, cred) {
				a.challenge(req, tx)
				return
			}
			user := a.users[cred.Username]
			persona := s.personaForNumber(req.Recipient.User)
			if persona != "" && persona != user.Persona {
				a.challenge(req, tx)
				return
			}
			next(req, tx)
		}
	}
}

func (a *sipDigestAuth) valid(req *sip.Request, cred *digest.Credentials) bool {
	user, ok := a.users[cred.Username]
	if !ok || cred.Realm != a.realm || cred.Algorithm != sipDigestAlgorithm || cred.QOP != "auth" || cred.Nc < 1 || cred.Cnonce == "" || cred.URI != req.Recipient.String() {
		return false
	}
	a.mu.Lock()
	expiry, ok := a.nonces[cred.Nonce]
	a.mu.Unlock()
	if !ok || time.Now().After(expiry) {
		return false
	}
	challenge := &digest.Challenge{Realm: a.realm, Nonce: cred.Nonce, Algorithm: sipDigestAlgorithm, QOP: []string{"auth"}}
	expected, err := digest.Digest(challenge, digest.Options{Method: req.Method.String(), URI: cred.URI, Username: cred.Username, A1: user.HA1, Count: cred.Nc, Cnonce: cred.Cnonce})
	if err != nil || len(expected.Response) != len(cred.Response) || subtle.ConstantTimeCompare([]byte(strings.ToLower(expected.Response)), []byte(strings.ToLower(cred.Response))) != 1 {
		return false
	}
	key := cred.Nonce + "\x00" + cred.Username + "\x00" + cred.Cnonce
	a.mu.Lock()
	defer a.mu.Unlock()
	if expiry := a.nonces[cred.Nonce]; expiry.IsZero() || time.Now().After(expiry) || cred.Nc <= a.counts[key] {
		return false
	}
	a.counts[key] = cred.Nc
	return true
}

func (a *sipDigestAuth) challenge(req *sip.Request, tx sip.ServerTransaction) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		_ = tx.Respond(sip.NewResponseFromRequest(req, 500, "Internal Server Error", nil))
		return
	}
	nonce, now := hex.EncodeToString(b), time.Now()
	a.mu.Lock()
	for value, expiry := range a.nonces {
		if now.After(expiry) {
			delete(a.nonces, value)
			prefix := value + "\x00"
			for key := range a.counts {
				if strings.HasPrefix(key, prefix) {
					delete(a.counts, key)
				}
			}
		}
	}
	a.nonces[nonce] = now.Add(sipDigestLifetime)
	a.mu.Unlock()
	challenge := &digest.Challenge{Realm: a.realm, Nonce: nonce, Algorithm: sipDigestAlgorithm, QOP: []string{"auth"}}
	res := sip.NewResponseFromRequest(req, sip.StatusUnauthorized, "Unauthorized", nil)
	res.AppendHeader(sip.NewHeader("WWW-Authenticate", challenge.String()))
	_ = tx.Respond(res)
}

func (s *Server) personaForNumber(number string) string {
	if s.Settings != nil {
		return s.Settings.PersonaForNumber(number)
	}
	return s.Config.Numbers[number]
}
