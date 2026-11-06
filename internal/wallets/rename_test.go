package wallets

import (
	"path/filepath"
	"testing"
)

func TestRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallets.json")
	secrets := filepath.Join(dir, "secrets.json")
	st, err := LoadWithSecrets(path, secrets)
	if err != nil {
		t.Fatal(err)
	}
	w := Wallet{ID: "w1", Name: "旧名", Address: "0xabc", Source: "privateKey"}
	if err := st.Add(w); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Rename("w1", "  "); err == nil {
		t.Fatal("expected empty name error")
	}
	out, err := st.Rename("w1", " 新名 ")
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "新名" {
		t.Fatalf("got %q", out.Name)
	}
	got, ok := st.Get("w1")
	if !ok || got.Name != "新名" {
		t.Fatalf("persist failed: %+v", got)
	}
}
