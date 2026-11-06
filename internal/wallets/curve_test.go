package wallets

import (
	"crypto/ecdsa"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/miguelmota/go-ethereum-hdwallet"
	"github.com/tyler-smith/go-bip39"
)

const testMnemonic = "test test test test test test test test test test test junk"

func TestImportMnemonicKeyUsesS256(t *testing.T) {
	t.Setenv("SECRETS_PASSPHRASE", "test-curve-passphrase")
	dir := t.TempDir()
	store, err := LoadWithSecrets(
		filepath.Join(dir, "wallets.json"),
		filepath.Join(dir, "secrets.json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bip39.IsMnemonicValid(testMnemonic) {
		t.Fatal("test mnemonic invalid")
	}

	w, err := store.ImportMnemonic("主", testMnemonic, "")
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.PrivateKey(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if key.Curve != crypto.S256() {
		t.Fatalf("curve want crypto.S256, got %T", key.Curve)
	}
	hash := crypto.Keccak256([]byte("anubisbot-curve-check"))
	if _, err := crypto.Sign(hash, key); err != nil {
		t.Fatalf("crypto.Sign: %v", err)
	}
}

func TestPrivateKeyNormalizesHDWalletCurve(t *testing.T) {
	t.Setenv("SECRETS_PASSPHRASE", "test-curve-passphrase")
	dir := t.TempDir()
	store, err := LoadWithSecrets(
		filepath.Join(dir, "wallets.json"),
		filepath.Join(dir, "secrets.json"),
	)
	if err != nil {
		t.Fatal(err)
	}

	wallet, err := hdwallet.NewFromMnemonic(testMnemonic)
	if err != nil {
		t.Fatal(err)
	}
	account, err := wallet.Derive(hdwallet.DefaultBaseDerivationPath, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := wallet.PrivateKey(account)
	if err != nil {
		t.Fatal(err)
	}
	if raw.Curve == crypto.S256() {
		t.Skip("hdwallet already returns crypto.S256; nothing to normalize")
	}

	id := "w-hdwallet-raw"
	store.mu.Lock()
	store.list = append(store.list, Wallet{
		ID: id, Name: "主", Address: account.Address.Hex(), Source: "mnemonic",
	})
	store.keys[id] = raw
	store.mu.Unlock()

	key, err := store.PrivateKey(id)
	if err != nil {
		t.Fatal(err)
	}
	if key.Curve != crypto.S256() {
		t.Fatalf("curve want crypto.S256 after PrivateKey, got %T", key.Curve)
	}
	hash := crypto.Keccak256([]byte("anubisbot-curve-check"))
	if _, err := crypto.Sign(hash, key); err != nil {
		t.Fatalf("crypto.Sign: %v", err)
	}
}

func TestEnsureS256Nil(t *testing.T) {
	var key *ecdsa.PrivateKey
	if _, err := ensureS256(key); err == nil {
		t.Fatal("expected error for nil key")
	}
}
