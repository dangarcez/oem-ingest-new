package auth

import (
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePasswordAndApplyBasicAuth(t *testing.T) {
	creds, err := Resolve(Options{
		User:     "oem_user",
		Password: "direct-secret",
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, "http://oem.example/em/api", nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	if err := creds.Apply(req); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	user, password, ok := req.BasicAuth()
	if !ok {
		t.Fatal("expected Basic Auth header")
	}
	if user != "oem_user" || password != "direct-secret" {
		t.Fatalf("unexpected Basic Auth credentials: %q / %q", user, password)
	}
}

func TestLegacyTokenRoundTrip(t *testing.T) {
	hashFile := writeHashFile(t, "legacy script bytes")
	hashHex, err := FileSHA256Hex(hashFile)
	if err != nil {
		t.Fatalf("FileSHA256Hex returned error: %v", err)
	}
	if hashHex != "3e0ca2d57f033033ae20b758458ece78ae8038867fc205a447fe2a14ceca61c9" {
		t.Fatalf("FileSHA256Hex = %q", hashHex)
	}
	token := encodeLegacyToken(t, hashHex, "Senha$123")

	got, err := DecodeLegacyToken(hashHex, token)
	if err != nil {
		t.Fatalf("DecodeLegacyToken returned error: %v", err)
	}
	if got != "Senha$123" {
		t.Fatalf("DecodeLegacyToken = %q", got)
	}

	creds, err := Resolve(Options{
		User:          "oem_user",
		Token:         token,
		TokenHashFile: hashFile,
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if creds.Password != "Senha$123" {
		t.Fatalf("Resolve password = %q", creds.Password)
	}
}

func TestDecodeLegacyTokenMatchesPythonFixture(t *testing.T) {
	const (
		// Produced by old_code/oem/tools/xisou.py semantics for password
		// "Senha$123" with SHA-256 hex of "legacy script bytes".
		hashHex = "3e0ca2d57f033033ae20b758458ece78ae8038867fc205a447fe2a14ceca61c9"
		token   = "YABeCwAWVQcE"
	)

	got, err := DecodeLegacyToken(hashHex, token)
	if err != nil {
		t.Fatalf("DecodeLegacyToken returned error: %v", err)
	}
	if got != "Senha$123" {
		t.Fatalf("DecodeLegacyToken = %q", got)
	}
}

func TestResolvePrefersPasswordOverToken(t *testing.T) {
	creds, err := Resolve(Options{
		User:     "oem_user",
		Password: "direct-secret",
		Token:    "not-a-valid-token",
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if creds.Password != "direct-secret" {
		t.Fatalf("Resolve password = %q", creds.Password)
	}
}

func TestResolveTokenRequiresHashFile(t *testing.T) {
	_, err := Resolve(Options{
		User:  "oem_user",
		Token: "token",
	})
	assertAuthErrorContains(t, err, "OEM_AUTH_TOKEN_HASH_FILE")
}

func TestResolveRequiresUserAndCredential(t *testing.T) {
	_, err := Resolve(Options{Password: "secret"})
	assertAuthErrorContains(t, err, "OEM_USER")

	_, err = Resolve(Options{User: "oem_user"})
	assertAuthErrorContains(t, err, "OEM_PASSWORD ou OEM_TOKEN")
}

func TestDecodeLegacyTokenErrorDoesNotLeakToken(t *testing.T) {
	const token = "invalid-token-value"

	_, err := DecodeLegacyToken("abc123", token)
	if err == nil {
		t.Fatal("expected invalid token error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error leaks token: %v", err)
	}
}

func TestApplyRejectsNilRequest(t *testing.T) {
	err := (Credentials{User: "u", Password: "p"}).Apply(nil)
	assertAuthErrorContains(t, err, "request HTTP")
}

func writeHashFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "hash-source.py")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write hash file: %v", err)
	}
	return path
}

func encodeLegacyToken(t *testing.T, hashHex, password string) string {
	t.Helper()

	key := []byte(hashHex)
	if len(key) == 0 {
		t.Fatal("hashHex cannot be empty")
	}
	plain := []byte(password)
	encoded := make([]byte, len(plain))
	for i, b := range plain {
		encoded[i] = b ^ key[i%len(key)]
	}
	return base64.URLEncoding.EncodeToString(encoded)
}

func assertAuthErrorContains(t *testing.T, err error, want string) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error containing %q, got %v", want, err)
	}
}
