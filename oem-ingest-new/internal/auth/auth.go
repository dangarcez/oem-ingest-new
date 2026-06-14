package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"unicode/utf8"
)

// Options contains the environment-derived values used to resolve OEM
// credentials.
type Options struct {
	User          string
	Password      string
	Token         string
	TokenHashFile string
}

// Credentials is the username/password pair used for OEM Basic Auth.
type Credentials struct {
	User     string
	Password string
}

// Resolve returns OEM credentials from direct password or legacy token options.
//
// Direct password has priority over token because it is the explicit, current
// configuration path. A token requires TokenHashFile because legacy tokens are
// bound to the SHA-256 hash of a chosen file.
func Resolve(opts Options) (Credentials, error) {
	user := strings.TrimSpace(opts.User)
	if user == "" {
		return Credentials{}, errors.New("OEM_USER: campo obrigatorio para autenticacao")
	}

	if opts.Password != "" {
		return Credentials{User: user, Password: opts.Password}, nil
	}

	if opts.Token == "" {
		return Credentials{}, errors.New("OEM_PASSWORD ou OEM_TOKEN: informe uma credencial para autenticacao")
	}
	if strings.TrimSpace(opts.TokenHashFile) == "" {
		return Credentials{}, errors.New("OEM_AUTH_TOKEN_HASH_FILE: campo obrigatorio ao usar OEM_TOKEN")
	}

	hashHex, err := FileSHA256Hex(opts.TokenHashFile)
	if err != nil {
		return Credentials{}, fmt.Errorf("calcular hash de OEM_AUTH_TOKEN_HASH_FILE: %w", err)
	}
	password, err := DecodeLegacyToken(hashHex, opts.Token)
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{User: user, Password: password}, nil
}

// FileSHA256Hex returns the SHA-256 hex digest used by the legacy Python token
// algorithm.
func FileSHA256Hex(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// DecodeLegacyToken decodes an OEM_TOKEN compatible with old_code/oem/tools/xisou.py.
func DecodeLegacyToken(hashHex, token string) (string, error) {
	key := []byte(hashHex)
	if len(key) == 0 {
		return "", errors.New("hash do token legado nao pode ser vazio")
	}

	encoded, err := decodeURLSafeBase64(token)
	if err != nil {
		return "", err
	}
	decoded := make([]byte, len(encoded))
	for i, b := range encoded {
		decoded[i] = b ^ key[i%len(key)]
	}
	if !utf8.Valid(decoded) {
		return "", errors.New("decodificar OEM_TOKEN legado: senha resultante nao e UTF-8 valida")
	}
	return string(decoded), nil
}

// AuthorizationHeader returns the HTTP Basic Authorization header value.
func (c Credentials) AuthorizationHeader() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(c.User+":"+c.Password))
}

// Apply sets HTTP Basic Auth on req.
func (c Credentials) Apply(req *http.Request) error {
	if req == nil {
		return errors.New("request HTTP nao pode ser nil")
	}
	req.SetBasicAuth(c.User, c.Password)
	return nil
}

func decodeURLSafeBase64(token string) ([]byte, error) {
	value := strings.TrimSpace(token)
	if value == "" {
		return nil, errors.New("decodificar OEM_TOKEN legado: token vazio")
	}

	decoded, err := base64.URLEncoding.DecodeString(value)
	if err == nil {
		return decoded, nil
	}
	if decoded, rawErr := base64.RawURLEncoding.DecodeString(value); rawErr == nil {
		return decoded, nil
	}
	return nil, errors.New("decodificar OEM_TOKEN legado: token base64 URL-safe invalido")
}
