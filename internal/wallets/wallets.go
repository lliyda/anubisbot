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

// Rename updates the display name of a wallet. Address and keys are unchanged.
func (s *Store) Rename(id, name string) (Wallet, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Wallet{}, fmt.Errorf("name required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if s.list[i].ID != id {
			continue
		}
		s.list[i].Name = name
		if err := s.save(); err != nil {
			return Wallet{}, err
		}
		w := s.list[i]
		return Wallet{
			ID: w.ID, Name: w.Name, Address: w.Address, Source: w.Source,
			Path: w.Path, PrivateKeyEnv: w.PrivateKeyEnv, MnemonicEnv: w.MnemonicEnv,
		}, nil
	}
	return Wallet{}, fmt.Errorf("wallet not found")
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
		name = defaultWalletName(addr)
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
	// hdwallet may return btcec curve; go-ethereum Sign requires crypto.S256().
	key, err = parseHexKey(hexKey)
	if err != nil {
		return Wallet{}, err
	}
	addr := account.Address.Hex()
	id := "w" + uuid.NewString()[:8]
	if name == "" {
		name = defaultWalletName(addr)
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
		norm, err := ensureS256(k)
		if err != nil {
			return nil, err
		}
		s.keys[id] = norm
		return norm, nil
	}
	for i := range s.list {
		if s.list[i].ID == id {
			if err := s.tryLoadKey(&s.list[i]); err != nil {
				return nil, err
			}
			if k, ok := s.keys[id]; ok {
				norm, err := ensureS256(k)
				if err != nil {
					return nil, err
				}
				s.keys[id] = norm
				return norm, nil
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

// ensureS256 rebuilds the key with crypto.S256 so types.SignTx / crypto.Sign accept it.
// go-ethereum-hdwallet may return a mathematically identical secp256k1 key whose Curve
// pointer is btcec, which fails the prv.Curve != S256() check.
func ensureS256(key *ecdsa.PrivateKey) (*ecdsa.PrivateKey, error) {
	if key == nil {
		return nil, fmt.Errorf("nil private key")
	}
	if key.Curve == crypto.S256() {
		return key, nil
	}
	return crypto.ToECDSA(crypto.FromECDSA(key))
}

// defaultWalletName uses "钱包" + last 4 hex chars of the address when import name is empty.
func defaultWalletName(addr string) string {
	a := strings.TrimSpace(addr)
	a = strings.TrimPrefix(a, "0x")
	a = strings.TrimPrefix(a, "0X")
	if len(a) > 4 {
		a = a[len(a)-4:]
	}
	return "钱包" + a
}

func isPlaceholder(a string) bool {
	a = strings.ToLower(a)
	return a == "" || strings.HasPrefix(a, "0x000000000000000000000000000000000000000")
}

func AddrOfKey(key *ecdsa.PrivateKey) common.Address {
	return crypto.PubkeyToAddress(key.PublicKey)
}
