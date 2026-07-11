package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func newTestKeystore(t *testing.T) *Keystore {
	t.Helper()
	dir := t.TempDir()
	return NewKeystore(filepath.Join(dir, "secrets.enc"), filepath.Join(dir, ".keystore.salt")).WithSeed("test-seed|user")
}

func TestKeystore_SetGetRoundtrip(t *testing.T) {
	ks := newTestKeystore(t)
	if err := ks.Set("app-xxx", "plain-secret-value"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ks.Get("app-xxx")
	if err != nil || !ok || got != "plain-secret-value" {
		t.Fatalf("Get = %q ok=%v err=%v", got, ok, err)
	}
}

func TestKeystore_GetMissing(t *testing.T) {
	ks := newTestKeystore(t)
	_, ok, err := ks.Get("nope")
	if err != nil || ok {
		t.Fatalf("missing key should return ok=false nil-err; got ok=%v err=%v", ok, err)
	}
}

func TestKeystore_Remove(t *testing.T) {
	ks := newTestKeystore(t)
	if err := ks.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	removed, err := ks.Remove("k")
	if err != nil || !removed {
		t.Fatalf("first remove should succeed: removed=%v err=%v", removed, err)
	}
	again, _ := ks.Remove("k")
	if again {
		t.Fatal("second remove should return false")
	}
}

func TestKeystore_ListSorted(t *testing.T) {
	ks := newTestKeystore(t)
	for _, id := range []string{"b", "a", "c"} {
		if err := ks.Set(id, "x"); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := ks.List()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a", "b", "c"}
	if len(ids) != 3 || ids[0] != "a" || ids[2] != "c" {
		t.Fatalf("List = %v, want %v", ids, want)
	}
}

// 同 seed + 同文件 → 两个独立 Keystore 实例能互通（验证密钥派生确定性 + 落盘持久）。
func TestKeystore_PersistAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	secrets := filepath.Join(dir, "secrets.enc")
	salt := filepath.Join(dir, ".keystore.salt")
	ks1 := NewKeystore(secrets, salt).WithSeed("same-seed")
	if err := ks1.Set("k", "secret"); err != nil {
		t.Fatal(err)
	}
	ks2 := NewKeystore(secrets, salt).WithSeed("same-seed")
	got, ok, err := ks2.Get("k")
	if err != nil || !ok || got != "secret" {
		t.Fatalf("second instance should read what first wrote: %q ok=%v err=%v", got, ok, err)
	}
}

// 不同 seed → 不同 key → GCM 认证失败。
func TestKeystore_DifferentSeedCannotDecrypt(t *testing.T) {
	dir := t.TempDir()
	secrets := filepath.Join(dir, "secrets.enc")
	salt := filepath.Join(dir, ".keystore.salt")
	ks1 := NewKeystore(secrets, salt).WithSeed("seed-a")
	if err := ks1.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	ks2 := NewKeystore(secrets, salt).WithSeed("seed-b")
	if _, _, err := ks2.Get("k"); err == nil {
		t.Fatal("different seed should fail GCM auth")
	}
}

// 同明文两次加密，IV/密文必须不同（随机 IV）。
func TestKeystore_CiphertextVariesPerCall(t *testing.T) {
	ks := newTestKeystore(t)
	if err := ks.Set("k", "same-plain"); err != nil {
		t.Fatal(err)
	}
	env1 := readEntry(t, ks.secretsFile, "k")
	if err := ks.Set("k", "same-plain"); err != nil {
		t.Fatal(err)
	}
	env2 := readEntry(t, ks.secretsFile, "k")
	if env1.IV == env2.IV || env1.Data == env2.Data {
		t.Fatal("IV/ciphertext must differ per encrypt call (random IV)")
	}
}

func TestKeystore_FileFormat_Version1(t *testing.T) {
	ks := newTestKeystore(t)
	if err := ks.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Version int `json:"version"`
		Entries map[string]struct {
			IV, Data, Tag string
		} `json:"entries"`
	}
	b, err := os.ReadFile(ks.secretsFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if raw.Version != 1 || len(raw.Entries) != 1 {
		t.Fatalf("format wrong: version=%d entries=%d", raw.Version, len(raw.Entries))
	}
	env := raw.Entries["k"]
	if env.IV == "" || env.Data == "" || env.Tag == "" {
		t.Fatal("envelope missing iv/data/tag")
	}
}

func TestKeystore_SaltFilePerm0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file perms are unix-only")
	}
	ks := newTestKeystore(t)
	if err := ks.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(ks.saltFile)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("salt file perm = %o, want 0600", fi.Mode().Perm())
	}
}

func readEntry(t *testing.T, secretsFile, id string) ksEnvelope {
	t.Helper()
	b, err := os.ReadFile(secretsFile)
	if err != nil {
		t.Fatal(err)
	}
	var s ksStoreFile
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s.Entries[id]
}
