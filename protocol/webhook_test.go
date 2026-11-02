package protocol

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

// The Standard Webhooks specification's published test vector.
const (
	vectorSecret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	vectorID     = "msg_p5jXN8AQM9LWM0D4loKWxJek"
	vectorBody   = `{"test": 2432232314}`
	vectorSig    = "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE="
)

var vectorTime = time.Unix(1614265330, 0)

func TestSignMatchesTheSpecification(t *testing.T) {
	h := http.Header{}
	if err := Sign(h, vectorSecret, vectorID, []byte(vectorBody), vectorTime); err != nil {
		t.Fatal(err)
	}
	if got := h.Get(HeaderSignature); got != vectorSig {
		t.Errorf("signature = %q, want %q", got, vectorSig)
	}
	if got := h.Get(HeaderTimestamp); got != "1614265330" {
		t.Errorf("timestamp = %q", got)
	}
}

func TestVerify(t *testing.T) {
	signed := func() http.Header {
		h := http.Header{}
		if err := Sign(h, vectorSecret, vectorID, []byte(vectorBody), vectorTime); err != nil {
			t.Fatal(err)
		}
		return h
	}
	if err := Verify(signed(), vectorSecret, []byte(vectorBody), vectorTime.Add(time.Minute)); err != nil {
		t.Errorf("a signed notification: %v", err)
	}
	if err := Verify(signed(), vectorSecret, []byte(`{"test": 1}`), vectorTime); !errors.Is(err, ErrSignature) {
		t.Errorf("a changed body: %v", err)
	}
	if err := Verify(signed(), vectorSecret, []byte(vectorBody), vectorTime.Add(Tolerance+time.Second)); !errors.Is(err, ErrSignature) {
		t.Errorf("a replay: %v", err)
	}
	other, _ := NewSecret()
	if err := Verify(signed(), other, []byte(vectorBody), vectorTime); !errors.Is(err, ErrSignature) {
		t.Errorf("another secret: %v", err)
	}
	h := signed()
	h.Set(HeaderSignature, "v1,bm90IGl0 "+vectorSig)
	if err := Verify(h, vectorSecret, []byte(vectorBody), vectorTime); err != nil {
		t.Errorf("one of several signatures: %v", err)
	}
	if err := Verify(http.Header{}, vectorSecret, []byte(vectorBody), vectorTime); !errors.Is(err, ErrSignature) {
		t.Errorf("no headers: %v", err)
	}
}

func TestSecrets(t *testing.T) {
	s, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckSecret(s); err != nil {
		t.Errorf("NewSecret gave %q: %v", s, err)
	}
	for _, bad := range []string{"", "change-me", "whsec_", "whsec_not base64!"} {
		if CheckSecret(bad) == nil {
			t.Errorf("CheckSecret(%q) accepted it", bad)
		}
	}
}
