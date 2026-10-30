package data

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
)

// encryptedSecretPrefix marks a value written by a future version that
// encrypts secrets at rest.
const encryptedSecretPrefix = "enc:"

// Secret is a column holding an OAuth client secret or token. It is stored as
// plain text, like the server's other secrets (API keys, app config, the OIDC
// client secret). Its Scan and Value are the one place to add encryption at
// rest later, for every secret column at once, without changing the schema.
type Secret string

// Value implements driver.Valuer.
func (s Secret) Value() (driver.Value, error) {
	return string(s), nil
}

// Scan implements sql.Scanner.
func (s *Secret) Scan(value any) error {
	var v string
	switch value := value.(type) {
	case nil:
	case string:
		v = value
	case []byte:
		v = string(value)
	default:
		return fmt.Errorf("unsupported secret column type %T", value)
	}
	// Refuse rather than hand ciphertext to a provider as if it were a token.
	if strings.HasPrefix(v, encryptedSecretPrefix) {
		return errors.New("secret is encrypted, but this server version cannot decrypt it")
	}
	*s = Secret(v)
	return nil
}
