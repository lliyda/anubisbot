package wallets

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	"github.com/miguelmota/go-ethereum-hdwallet"
	"github.com/tyler-smith/go-bip39"
)

type Wallet struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Address       string `json:"address"`
	Source        string `json:"source"` // privateKey | mnemonic | privateKeyEnv
	PrivateKeyEnv string `json:"privateKeyEnv,omitempty"`
	MnemonicEnv   string `json:"mnemonicEnv,omitempty"`
	Path          string `json:"path,omitempty"`
}

type fileShape struct {
	Wallets []Wallet `json:"wallets"`
}

type Store struct {
	mu           sync.RWMutex
	path         string
	secretsFile  string
	list         []Wallet
	keys         map[string]*ecdsa.PrivateKey
	cachedMaster []byte
}

func Load(path string) (*Store, error) {
	return LoadWithSecrets(path, "data/secrets.local.json")
}

func LoadWithSecrets(path, secretsPath string) (*Store, error) {
	s := &Store{path: path, secretsFile: secretsPath, keys: map[string]*ecdsa.PrivateKey{}}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		// LIVE starts empty; SIM may seed from example
		seedExample := strings.Contains(path, "wallets.sim.json") || strings.Contains(path, "wallets.local.json")
		if seedExample {
			if b, err2 := os.ReadFile("config/wallets.example.json"); err2 == nil {
				_ = os.WriteFile(path, b, 0o644)
			} else {
				_ = os.WriteFile(path, []byte(`{"wallets":[]}`+"\n"), 0o644)
			}
		} else {
			_ = os.WriteFile(path, []byte(`{"wallets":[]}`+"\n"), 0o644)
		}
	}
	var f fileShape
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	s.list = f.Wallets
	for i := range s.list {
		_ = s.tryLoadKey(&s.list[i])
	}
	return s, nil
}

func (s *Store) save() error {
	b, err := json.MarshalIndent(fileShape{Wallets: s.list}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(b, '\n'), 0o644)
}

func (s *Store) ListPublic() []Wallet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Wallet, len(s.list))
	for i, w := range s.list {
		out[i] = Wallet{
			ID: w.ID, Name: w.Name, Address: w.Address, Source: w.Source,
			Path: w.Path, PrivateKeyEnv: w.PrivateKeyEnv, MnemonicEnv: w.MnemonicEnv,
		}
	}
	return out
}

func (s *Store) Get(id string) (Wallet, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, w := range s.list {
		if w.ID == id {
			return w, true
		}
	}
	return Wallet{}, false
}

func (s *Store) Add(w Wallet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.list {
		if x.ID == w.ID {
			return fmt.Errorf("wallet id exists")
		}
	}
	_ = s.tryLoadKey(&w)
	s.list = append(s.list, w)
	return s.save()
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, w := range s.list {
		if w.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("wallet not found")
	}
	s.list = append(s.list[:idx], s.list[idx+1:]...)
	delete(s.keys, id)
	_ = s.deleteSecretLocked(id)
	return s.save()
}

func (s *Store) ImportPrivateKey(name, hexKey string) (Wallet, error) {
	key, err := parseHexKey(hexKey)
	if err != nil {
		return Wallet{}, err
	}
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()
	id := "w" + uuid.NewString()[:8]
	if name == "" {
		name = "钱包 " + id
	}
	w := Wallet{
		ID: id, Name: name, Address: addr, Source: "privateKey",
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.putSecretLocked(id, normalizeHexKey(hexKey)); err != nil {
		return Wallet{}, err
	}
	s.keys[id] = key
	s.list = append(s.list, w)
	if err := s.save(); err != nil {
		return Wallet{}, err
	}
	return w, nil
}

func (s *Store) ImportMnemonic(name, mnemonic, path string) (Wallet, error) {
	mnemonic = strings.TrimSpace(mnemonic)
	if !bip39.IsMnemonicValid(mnemonic) {
		return Wallet{}, fmt.Errorf("invalid mnemonic")
	}
	if path == "" {
		path = hdwallet.DefaultBaseDerivationPath.String()
	}
	wallet, err := hdwallet.NewFromMnemonic(mnemonic)
	if err != nil {
		return Wallet{}, err
	}
	deriv, err := hdwallet.ParseDerivationPath(path)
	if err != nil {
		return Wallet{}, err
	}
	account, err := wallet.Derive(deriv, false)
	if err != nil {
		return Wallet{}, err
	}
	key, err := wallet.PrivateKey(account)
	if err != nil {
		return Wallet{}, err
	}
	hexKey := "0x" + common.Bytes2Hex(crypto.FromECDSA(key))
	addr := account.Address.Hex()
	id := "w" + uuid.NewString()[:8]
	if name == "" {
		name = "钱包 " + id
	}
	w := Wallet{
		ID: id, Name: name, Address: addr, Source: "mnemonic", Path: path,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.putSecretLocked(id, hexKey); err != nil {
		return Wallet{}, err
	}
	s.keys[id] = key
	s.list = append(s.list, w)
	if err := s.save(); err != nil {
		return Wallet{}, err
	}
	return w, nil
}

func (s *Store) PrivateKey(id string) (*ecdsa.PrivateKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.keys[id]; ok {
		return k, nil
	}
	for i := range s.list {
		if s.list[i].ID == id {
			if err := s.tryLoadKey(&s.list[i]); err != nil {
				return nil, err
			}
			if k, ok := s.keys[id]; ok {
				return k, nil
			}
		}
	}
	return nil, fmt.Errorf("no private key for wallet %s", id)
}

func (s *Store) tryLoadKey(w *Wallet) error {
	// 1) encrypted secrets file
	if plain, err := s.getSecretPlainLocked(w.ID); err == nil && plain != "" {
		key, err := parseHexKey(plain)
		if err != nil {
			return err
		}
		addr := crypto.PubkeyToAddress(key.PublicKey)
		if w.Address == "" || isPlaceholder(w.Address) {
			w.Address = addr.Hex()
		}
		s.keys[w.ID] = key
		return nil
	}
	// 2) env fallback
	envName := w.PrivateKeyEnv
	if envName == "" {
		return fmt.Errorf("no secret for wallet %s", w.ID)
	}
	raw := os.Getenv(envName)
	if raw == "" {
		return fmt.Errorf("env %s empty", envName)
	}
	key, err := parseHexKey(raw)
	if err != nil {
		return err
	}
	addr := crypto.PubkeyToAddress(key.PublicKey)
	if w.Address == "" || isPlaceholder(w.Address) {
		w.Address = addr.Hex()
	}
	s.keys[w.ID] = key
	return nil
}

func parseHexKey(raw string) (*ecdsa.PrivateKey, error) {
	return crypto.HexToECDSA(strings.TrimPrefix(strings.TrimSpace(raw), "0x"))
}

func isPlaceholder(a string) bool {
	a = strings.ToLower(a)
	return a == "" || strings.HasPrefix(a, "0x000000000000000000000000000000000000000")
}

func AddrOfKey(key *ecdsa.PrivateKey) common.Address {
	return crypto.PubkeyToAddress(key.PublicKey)
}
