package keymagic

import (
	"context"
	"crypto"
	"encoding/base64"
	"io/ioutil"
	"testing"

	remote_signer "github.com/quan-to/chevron"
	"github.com/quan-to/chevron/rstest"
)

// region Tests
func TestVerifySign(t *testing.T) {
	ctx := context.Background()
	valid, err := pgpMan.VerifySignature(ctx, testData, rstest.TestSignatureSignature)
	if err != nil || !valid {
		t.Errorf("Signature not valid or error found: %s", err)
	}

	valid, err = pgpMan.VerifySignatureStringData(ctx, rstest.TestSignatureData, rstest.TestSignatureSignature)
	if err != nil || !valid {
		t.Errorf("Signature not valid or error found: %s", err)
	}

	invalidTestData := []byte("huebr for the win!" + "makemeinvalid")

	valid, err = pgpMan.VerifySignature(ctx, invalidTestData, rstest.TestSignatureSignature)

	if valid || err == nil {
		t.Error("A invalid test data passed to verify has been validated!")
	}
}

func TestSign(t *testing.T) {
	ctx := context.Background()
	_, err := pgpMan.SignData(ctx, rstest.TestKeyFingerprint, testData, crypto.SHA512)
	if err != nil {
		t.Error(err)
	}
}

func TestDecrypt(t *testing.T) {
	ctx := context.Background()
	g, err := pgpMan.Decrypt(ctx, rstest.TestDecryptDataAscii, false)
	if err != nil {
		t.Error(err)
	}

	gd, err := base64.StdEncoding.DecodeString(g.Base64Data)
	if err != nil {
		t.Error(err)
	}

	if string(gd) != rstest.TestSignatureData {
		t.Errorf("Decrypted data does no match. Expected \"%s\" got \"%s\"", string(gd), rstest.TestSignatureData)
	}

	g, err = pgpMan.Decrypt(ctx, rstest.TestDecryptDataOnly, true)
	if err != nil {
		t.Error(err)
	}

	gd, err = base64.StdEncoding.DecodeString(g.Base64Data)
	if err != nil {
		t.Error(err)
	}

	if string(gd) != rstest.TestSignatureData {
		t.Errorf("Decrypted data does no match. Expected \"%s\" got \"%s\"", string(gd), rstest.TestSignatureData)
	}
}

func TestDecryptRaw(t *testing.T) {
	ctx := context.Background()
	b, err := ioutil.ReadFile("../tests/testraw.gpg")

	if err != nil {
		t.Error(err)
	}

	d := string(b)

	_, err = pgpMan.Decrypt(ctx, d, false)
	if err != nil {
		t.Error(err)
	}

}

func TestEncrypt(t *testing.T) {
	ctx := context.Background()
	d, err := pgpMan.Encrypt(ctx, "testing", rstest.TestKeyFingerprint, testData, false)

	if err != nil {
		t.Error(err)
	}

	// region Test Decrypt
	g, err := pgpMan.Decrypt(ctx, d, false)
	if err != nil {
		t.Error(err)
	}

	gd, err := base64.StdEncoding.DecodeString(g.Base64Data)
	if err != nil {
		t.Error(err)
	}

	if string(gd) != rstest.TestSignatureData {
		t.Errorf("Decrypted data does no match. Expected \"%s\" got \"%s\"", string(gd), rstest.TestSignatureData)
	}
	// endregion
	d, err = pgpMan.Encrypt(ctx, "testing", rstest.TestKeyFingerprint, testData, true)

	if err != nil {
		t.Error(err)
	}

	// region Test Decrypt
	g, err = pgpMan.Decrypt(ctx, d, true)
	if err != nil {
		t.Error(err)
	}

	gd, err = base64.StdEncoding.DecodeString(g.Base64Data)
	if err != nil {
		t.Error(err)
	}

	if string(gd) != rstest.TestSignatureData {
		t.Errorf("Decrypted data does no match. Expected \"%s\" got \"%s\"", string(gd), rstest.TestSignatureData)
	}
	// endregion
}

func TestGenerateKey(t *testing.T) {
	ctx := context.Background()
	key, err := pgpMan.GeneratePGPKey(ctx, "HUE", rstest.TestKeyFingerprint, pgpMan.MinKeyBits())

	if err != nil {
		t.Error(err)
	}

	// Load key
	err, _ = pgpMan.LoadKey(ctx, key)
	if err != nil {
		t.Error(err)
	}

	fp, _ := remote_signer.GetFingerPrintFromKey(key)

	// Unlock Key
	err = pgpMan.UnlockKey(ctx, fp, rstest.TestKeyFingerprint)
	if err != nil {
		t.Error(err)
	}

	// Try sign
	signature, err := pgpMan.SignData(ctx, fp, testData, crypto.SHA512)
	if err != nil {
		t.Error(err)
	}
	// Try verify
	valid, err := pgpMan.VerifySignature(ctx, testData, signature)
	if err != nil {
		t.Error(err)
	}
	if !valid {
		t.Error("Generated signature is not valid!")
	}
}

// endregion
// region Benchmarks
func BenchmarkSign(b *testing.B) {
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		_, err := pgpMan.SignData(ctx, rstest.TestKeyFingerprint, testData, crypto.SHA512)
		if err != nil {
			b.Error(err)
		}
	}
}
func BenchmarkVerifySignature(b *testing.B) {
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		_, err := pgpMan.VerifySignature(ctx, testData, rstest.TestSignatureSignature)
		if err != nil {
			b.Error(err)
		}
	}
}
func BenchmarkVerifySignatureStringData(b *testing.B) {
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		_, err := pgpMan.VerifySignatureStringData(ctx, rstest.TestSignatureData, rstest.TestSignatureSignature)
		if err != nil {
			b.Error(err)
		}
	}
}
func BenchmarkEncryptASCII(b *testing.B) {
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		_, err := pgpMan.Encrypt(ctx, "", rstest.TestKeyFingerprint, testData, false)
		if err != nil {
			b.Error(err)
		}
	}
}
func BenchmarkEncryptDataOnly(b *testing.B) {
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		_, err := pgpMan.Encrypt(ctx, "", rstest.TestKeyFingerprint, testData, true)
		if err != nil {
			b.Error(err)
		}
	}
}
func BenchmarkKeyGenerate2048(b *testing.B) {
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		_, err := pgpMan.GeneratePGPKey(ctx, "", "123456789", 2048)
		if err != nil {
			b.Error(err)
		}
	}
}
func BenchmarkKeyGenerate3072(b *testing.B) {
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		_, err := pgpMan.GeneratePGPKey(ctx, "", "123456789", 3072)
		if err != nil {
			b.Error(err)
		}
	}
}
func BenchmarkKeyGenerate4096(b *testing.B) {
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		_, err := pgpMan.GeneratePGPKey(ctx, "", "123456789", 4096)
		if err != nil {
			b.Error(err)
		}
	}
}

// endregion
