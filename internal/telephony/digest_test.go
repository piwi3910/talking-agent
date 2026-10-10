package telephony

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"
)

func testSIPHA1(username, realm, password string) string {
	sum := sha256.Sum256([]byte(username + ":" + realm + ":" + password))
	return hex.EncodeToString(sum[:])
}
func setTestSIPDigest(t *testing.T, realm string, users map[string]sipDigestUser) {
	t.Helper()
	raw, err := json.Marshal(users)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SIP_DIGEST_USERS", string(raw))
	t.Setenv("SIP_DIGEST_REALM", realm)
}

func TestSIPDigestSHA256ReplayAndExpiry(t *testing.T) {
	const username, realm, nonce = "handset", "agent.test", "test-nonce"
	ha1 := testSIPHA1(username, realm, "secret")
	a := &sipDigestAuth{realm: realm, users: map[string]sipDigestUser{username: {HA1: ha1, Persona: "a"}}, nonces: map[string]time.Time{nonce: time.Now().Add(time.Minute)}, counts: map[string]int{}}
	req := sip.NewRequest(sip.INVITE, sip.Uri{User: "500", Host: "agent.test", Port: 5060})
	makeCred := func(algorithm string, nc int) *digest.Credentials {
		challenge := &digest.Challenge{Realm: realm, Nonce: nonce, Algorithm: algorithm, QOP: []string{"auth"}}
		cred, err := digest.Digest(challenge, digest.Options{Method: req.Method.String(), URI: req.Recipient.String(), Username: username, A1: ha1, Count: nc, Cnonce: "client"})
		if err != nil {
			t.Fatal(err)
		}
		return cred
	}
	first := makeCred(sipDigestAlgorithm, 1)
	if !a.valid(req, first) || a.valid(req, first) {
		t.Fatal("valid digest or replay check failed")
	}
	if a.valid(req, makeCred("MD5", 2)) {
		t.Fatal("unsupported algorithm accepted")
	}
	if !a.valid(req, makeCred(sipDigestAlgorithm, 2)) {
		t.Fatal("increasing nonce count rejected")
	}
	a.nonces[nonce] = time.Now().Add(-time.Second)
	if a.valid(req, makeCred(sipDigestAlgorithm, 3)) {
		t.Fatal("expired nonce accepted")
	}
}
