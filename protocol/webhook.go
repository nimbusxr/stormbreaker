package protocol

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Notifications are signed as Standard Webhooks (https://www.standardwebhooks.com): a
// webhook-id, a webhook-timestamp, and a webhook-signature of "v1," and the base64
// HMAC-SHA256 of "<id>.<timestamp>.<body>", keyed with the secret after "whsec_",
// base64-decoded.
const (
	HeaderID        = "webhook-id"
	HeaderTimestamp = "webhook-timestamp"
	HeaderSignature = "webhook-signature"

	secretPrefix = "whsec_"
)

// Tolerance is how far a notification's timestamp may be from the receiver's clock before the
// receiver refuses it as a replay.
const Tolerance = 5 * time.Minute

// NewSecret returns a fresh signing secret: "whsec_" and 32 random bytes in base64.
func NewSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return secretPrefix + base64.StdEncoding.EncodeToString(b), nil
}

// CheckSecret reports whether secret is a Standard Webhooks secret.
func CheckSecret(secret string) error {
	_, err := secretKey(secret)
	return err
}

func secretKey(secret string) ([]byte, error) {
	s := strings.TrimSpace(secret)
	if !strings.HasPrefix(s, secretPrefix) {
		return nil, errors.New(`a signing secret is "whsec_" and the key in base64`)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, secretPrefix))
	if err != nil || len(key) == 0 {
		return nil, errors.New(`a signing secret is "whsec_" and the key in base64`)
	}
	return key, nil
}

func signature(key []byte, id, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id))
	mac.Write([]byte{'.'})
	mac.Write([]byte(timestamp))
	mac.Write([]byte{'.'})
	mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// Sign sets the Standard Webhooks headers of a notification with id, sent at now.
func Sign(h http.Header, secret, id string, body []byte, now time.Time) error {
	key, err := secretKey(secret)
	if err != nil {
		return err
	}
	ts := strconv.FormatInt(now.Unix(), 10)
	h.Set(HeaderID, id)
	h.Set(HeaderTimestamp, ts)
	h.Set(HeaderSignature, signature(key, id, ts, body))
	return nil
}

// ErrSignature is returned by Verify for a notification its secret did not sign.
var ErrSignature = errors.New("the notification's signature does not match")

// Verify checks a notification's Standard Webhooks headers against secret: one of the
// signatures webhook-signature lists must match, and webhook-timestamp must be within
// Tolerance of now.
func Verify(h http.Header, secret string, body []byte, now time.Time) error {
	key, err := secretKey(secret)
	if err != nil {
		return err
	}
	id, ts, sigs := h.Get(HeaderID), h.Get(HeaderTimestamp), h.Get(HeaderSignature)
	if id == "" || ts == "" || sigs == "" {
		return fmt.Errorf("%w: it has no %s, %s and %s", ErrSignature, HeaderID, HeaderTimestamp, HeaderSignature)
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: %s %q is not a Unix time", ErrSignature, HeaderTimestamp, ts)
	}
	if d := now.Sub(time.Unix(sec, 0)); d > Tolerance || d < -Tolerance {
		return fmt.Errorf("%w: it was signed at %s, more than %s from now", ErrSignature, time.Unix(sec, 0).UTC().Format(time.RFC3339), Tolerance)
	}
	want := signature(key, id, ts, body)
	for _, sig := range strings.Fields(sigs) {
		if hmac.Equal([]byte(sig), []byte(want)) {
			return nil
		}
	}
	return ErrSignature
}
