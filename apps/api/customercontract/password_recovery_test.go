package customercontract_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
)

func TestPasswordRecoveryPinsTheTenantAndBrowserBeforeMailboxProof(t *testing.T) {
	good := customercontract.PasswordRecoveryStart{
		ID: uuid.Must(uuid.NewV7()).String(), TenantID: uuid.NewString(),
		BrowserHash: strings.Repeat("a", 64), SourceHash: strings.Repeat("b", 64), Email: "staff@example.test",
	}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*customercontract.PasswordRecoveryStart){
		func(v *customercontract.PasswordRecoveryStart) { v.ID = uuid.NewString() },
		func(v *customercontract.PasswordRecoveryStart) { v.TenantID = uuid.Nil.String() },
		func(v *customercontract.PasswordRecoveryStart) { v.TenantID = "other" },
		func(v *customercontract.PasswordRecoveryStart) { v.BrowserHash = strings.Repeat("A", 64) },
		func(v *customercontract.PasswordRecoveryStart) { v.SourceHash = "" },
		func(v *customercontract.PasswordRecoveryStart) { v.Email = "Staff <staff@example.test>" },
		func(v *customercontract.PasswordRecoveryStart) { v.Email = " staff@example.test" },
	} {
		v := good
		change(&v)
		if v.Validate() == nil {
			t.Fatal("invalid recovery binding accepted")
		}
	}
}

func TestPasswordRecoveryProofAndPasswordStayPrivateAndBounded(t *testing.T) {
	id := uuid.Must(uuid.NewV7()).String()
	proof := customercontract.PasswordRecoveryProof{ID: id, BrowserHash: strings.Repeat("a", 64), Code: "12345678"}
	password := customercontract.PasswordRecoveryPassword{ID: id, BrowserHash: proof.BrowserHash, Password: "synthetic-password"}
	if proof.Validate() != nil || password.Validate() != nil {
		t.Fatal("valid recipient input refused")
	}
	for _, code := range []string{"", "1234567", "123456789", "１２３４５６７８", "abcdefgh", "1234567\n"} {
		v := proof
		v.Code = code
		if v.Validate() == nil {
			t.Fatal("invalid recovery code accepted")
		}
	}
	for _, value := range []string{"", strings.Repeat("a", 201), string([]byte{0xff})} {
		v := password
		v.Password = value
		if v.Validate() == nil {
			t.Fatal("invalid password bounds accepted")
		}
	}
	for _, v := range []any{proof, &proof, password, &password} {
		if text := fmt.Sprintf("%v %+v %#v", v, v, v); strings.Contains(text, proof.Code) || strings.Contains(text, password.Password) || strings.Contains(text, proof.BrowserHash) {
			t.Fatal("private recovery input exposed in diagnostics")
		}
	}
}
