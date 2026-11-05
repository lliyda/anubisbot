package wallets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/scrypt"
)

type secretsFile struct {
	Version int                     `json:"version"`
	Keys    map[string]secretEntry  `json:"keys"`
}

type secretEntry struct {
	// Encrypted fields (preferred)
	Ciphertext string `json:"ciphertext,omitempty"`
	Nonce      string `json:"nonce,omitempty"`
	// Legacy plaintext (migrated away on next save)
	PrivateKey string `json:"privateKey,omitempty"`
}

func (s *Store) secretsPath() string {
	if s.secretsFile != "" {
		return s.secretsFile
	}
	return "data/secrets.local.json"
}

func (s *Store) masterKeyPath() string {
	return "data/master.key"
}

// masterKey returns 32-byte AES key.
// Priority: SECRETS_PASSPHRASE (scrypt) → data/master.key (create if missing).
func (s *Store) masterKey() ([]byte, error) {
	if s.cachedMaster != nil {
		return s.cachedMaster, nil
	}
	if pass := strings.TrimSpace(os.Getenv("SECRETS_PASSPHRASE")); pass != "" {
		// fixed salt for local ops (machine-local file also ok; passphrase is secret)
		key, err := scrypt.Key([]byte(pass), []byte("anubisbot-secrets-v1"), 1<<15, 8, 1, 32)
		if err != nil {
			return nil, err
		}
		s.cachedMaster = key
		return key, nil
	}
	path := s.masterKeyPath()
	b, err := os.ReadFile(path)
	if err == nil && len(b) >= 32 {
		key := make([]byte, 32)
		copy(key, b[:32])
		s.cachedMaster = key
		return key, nil
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	s.cachedMaster = key
	return key, nil
}

func (s *Store) encrypt(plain string) (ciphertextB64, nonceB64 string, err error) {
	key, err := s.masterKey()
	if err != nil {
		return "", "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", "", err
	}
	out := gcm.Seal(nil, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(out), base64.StdEncoding.EncodeToString(nonce), nil
}

func (s *Store) decrypt(ciphertextB64, nonceB64 string) (string, error) {
	key, err := s.masterKey()
	if err != nil {
		return "", err
	}
	ct, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return "", err
	}
	nonce, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt failed (wrong SECRETS_PASSPHRASE / master.key?): %w", err)
	}
	return string(plain), nil
}

func (s *Store) loadSecretsLocked() secretsFile {
	path := s.secretsPath()
	b, err := os.ReadFile(path)
	if err != nil {
		return secretsFile{Version: 1, Keys: map[string]secretEntry{}}
	}
	var f secretsFile
	if err := json.Unmarshal(b, &f); err != nil || f.Keys == nil {
		return secretsFile{Version: 1, Keys: map[string]secretEntry{}}
	}
	if f.Version == 0 {
		f.Version = 1
	}
	return f
}

func (s *Store) saveSecretsLocked(f secretsFile) error {
	path := s.secretsPath()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if f.Keys == nil {
		f.Keys = map[string]secretEntry{}
	}
	f.Version = 1
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

func (s *Store) putSecretLocked(id, hexKey string) error {
	plain := normalizeHexKey(hexKey)
	ct, nonce, err := s.encrypt(plain)
	if err != nil {
		return err
	}
	f := s.loadSecretsLocked()
	f.Keys[id] = secretEntry{Ciphertext: ct, Nonce: nonce}
	return s.saveSecretsLocked(f)
}

func (s *Store) getSecretPlainLocked(id string) (string, error) {
	f := s.loadSecretsLocked()
	entr, ok := f.Keys[id]
	if !ok {
		return "", fmt.Errorf("no secret for %s", id)
	}
	// encrypted
	if entr.Ciphertext != "" && entr.Nonce != "" {
		return s.decrypt(entr.Ciphertext, entr.Nonce)
	}
	// legacy plaintext → migrate
	if entr.PrivateKey != "" {
		plain := normalizeHexKey(entr.PrivateKey)
		_ = s.putSecretLocked(id, plain)
		return plain, nil
	}
	return "", fmt.Errorf("empty secret for %s", id)
}

func (s *Store) deleteSecretLocked(id string) error {
	f := s.loadSecretsLocked()
	delete(f.Keys, id)
	return s.saveSecretsLocked(f)
}

func (s *Store) HasSecret(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f := s.loadSecretsLocked()
	entr, ok := f.Keys[id]
	if !ok {
		return false
	}
	return entr.Ciphertext != "" || entr.PrivateKey != ""
}

func normalizeHexKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "0x") && !strings.HasPrefix(raw, "0X") {
		return "0x" + raw
	}
	return "0x" + strings.TrimPrefix(strings.TrimPrefix(raw, "0x"), "0X")
}
